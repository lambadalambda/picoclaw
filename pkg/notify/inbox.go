package notify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	defaultSource               = "local"
	defaultPollInterval         = time.Second
	defaultMinIntervalPerSource = time.Minute
)

var enqueueSeq uint64

type QueueMessage struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Content   string `json:"content"`
	Channel   string `json:"channel,omitempty"`
	ChatID    string `json:"chat_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

type ServiceOptions struct {
	PollInterval         time.Duration
	MinIntervalPerSource time.Duration
}

type InboxService struct {
	workspace string
	bus       *bus.MessageBus

	pollInterval         time.Duration
	minIntervalPerSource time.Duration
	now                  func() time.Time

	mu           sync.Mutex
	running      bool
	stopChan     chan struct{}
	lastBySource map[string]time.Time
}

func InboxDir(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, "inbox")
}

func Enqueue(workspace string, msg QueueMessage) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", fmt.Errorf("workspace is empty")
	}

	msg.Content = strings.TrimSpace(msg.Content)
	if msg.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	msg.Source = normalizeSource(msg.Source)
	msg.Channel = strings.TrimSpace(msg.Channel)
	msg.ChatID = strings.TrimSpace(msg.ChatID)
	if (msg.Channel == "") != (msg.ChatID == "") {
		return "", fmt.Errorf("channel and chat_id must be provided together")
	}

	now := time.Now().UTC()
	if strings.TrimSpace(msg.ID) == "" {
		msg.ID = generateMessageID(now)
	}
	msg.CreatedAt = now.Format(time.RFC3339Nano)

	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%020d-%s.msg", now.UnixNano(), sanitizeFilenamePart(msg.ID))
	path := filepath.Join(InboxDir(workspace), filename)
	if err := utils.AtomicWriteFile(path, data, 0644); err != nil {
		return "", err
	}

	return msg.ID, nil
}

func NewInboxService(workspace string, msgBus *bus.MessageBus, opts ServiceOptions) *InboxService {
	poll := opts.PollInterval
	if poll <= 0 {
		poll = defaultPollInterval
	}

	minInterval := opts.MinIntervalPerSource
	if minInterval < 0 {
		minInterval = 0
	}
	if minInterval == 0 {
		minInterval = defaultMinIntervalPerSource
	}

	return &InboxService{
		workspace:            strings.TrimSpace(workspace),
		bus:                  msgBus,
		pollInterval:         poll,
		minIntervalPerSource: minInterval,
		now:                  time.Now,
		stopChan:             make(chan struct{}),
		lastBySource:         make(map[string]time.Time),
	}
}

func (s *InboxService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}
	if s.bus == nil {
		return fmt.Errorf("message bus not configured")
	}
	if strings.TrimSpace(s.workspace) == "" {
		return fmt.Errorf("workspace is empty")
	}

	s.stopChan = make(chan struct{})
	s.running = true
	go s.runLoop(s.stopChan)
	return nil
}

func (s *InboxService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}
	s.running = false
	if s.stopChan != nil {
		close(s.stopChan)
	}
}

func (s *InboxService) runLoop(stopChan <-chan struct{}) {
	// Process queued messages immediately on startup.
	s.processPending()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			s.processPending()
		}
	}
}

func (s *InboxService) processPending() {
	if s.bus == nil {
		return
	}
	inboxDir := InboxDir(s.workspace)
	if inboxDir == "" {
		return
	}

	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		logger.WarnCF("notify", "Failed reading local inbox",
			map[string]interface{}{"path": inboxDir, "error": err.Error()})
		return
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".msg") {
			continue
		}
		paths = append(paths, filepath.Join(inboxDir, e.Name()))
	}
	sort.Strings(paths)

	for _, path := range paths {
		msg, err := loadQueueMessage(path)
		if err != nil {
			logger.WarnCF("notify", "Dropping malformed inbox message",
				map[string]interface{}{"path": path, "error": err.Error()})
			quarantineMalformedMessage(path)
			continue
		}

		channel, chatID, ok := s.resolveTarget(msg)
		if !ok {
			continue
		}

		source := normalizeSource(msg.Source)
		if !s.allowSource(source) {
			continue
		}

		content := strings.TrimSpace(msg.Content)
		if content == "" {
			_ = os.Remove(path)
			continue
		}

		s.bus.PublishInbound(bus.InboundMessage{
			Channel:    channel,
			SenderID:   "local:" + source,
			ChatID:     chatID,
			Content:    fmt.Sprintf("[local:%s] %s", source, content),
			SessionKey: routing.EncodeSystemRoute(channel, chatID),
			Metadata: map[string]string{
				"local_notify":     "1",
				"local_source":     source,
				"local_message_id": msg.ID,
				"local_created_at": msg.CreatedAt,
			},
		})

		s.markSourceSent(source)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("notify", "Failed deleting delivered inbox message",
				map[string]interface{}{"path": path, "error": err.Error()})
		}

		logger.InfoCF("notify", "Delivered local notification",
			map[string]interface{}{
				"source":  source,
				"channel": channel,
				"chat_id": chatID,
				"id":      msg.ID,
			})
	}
}

func (s *InboxService) resolveTarget(msg QueueMessage) (channel, chatID string, ok bool) {
	channel = strings.TrimSpace(msg.Channel)
	chatID = strings.TrimSpace(msg.ChatID)
	if channel != "" || chatID != "" {
		if channel == "" || chatID == "" {
			return "", "", false
		}
		return channel, chatID, true
	}

	lastTargetPath := cron.LastTargetPath(s.workspace)
	channel, chatID, ok, err := cron.ResolveLastTarget(lastTargetPath)
	if err != nil {
		logger.WarnCF("notify", "Failed loading last active target",
			map[string]interface{}{"path": lastTargetPath, "error": err.Error()})
		return "", "", false
	}
	return channel, chatID, ok
}

func (s *InboxService) allowSource(source string) bool {
	if s.minIntervalPerSource <= 0 {
		return true
	}

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, ok := s.lastBySource[source]; ok {
		if now.Sub(last) < s.minIntervalPerSource {
			return false
		}
	}
	return true
}

func (s *InboxService) markSourceSent(source string) {
	if s.minIntervalPerSource <= 0 {
		return
	}

	s.mu.Lock()
	s.lastBySource[source] = s.now()
	s.mu.Unlock()
}

func loadQueueMessage(path string) (QueueMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QueueMessage{}, err
	}

	var msg QueueMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return QueueMessage{}, err
	}

	msg.ID = strings.TrimSpace(msg.ID)
	if msg.ID == "" {
		msg.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	msg.Source = normalizeSource(msg.Source)
	msg.Content = strings.TrimSpace(msg.Content)
	msg.Channel = strings.TrimSpace(msg.Channel)
	msg.ChatID = strings.TrimSpace(msg.ChatID)
	if msg.Content == "" {
		return QueueMessage{}, fmt.Errorf("content is empty")
	}
	if (msg.Channel == "") != (msg.ChatID == "") {
		return QueueMessage{}, fmt.Errorf("channel/chat_id must be provided together")
	}

	return msg, nil
}

func quarantineMalformedMessage(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}

	badPath := path + ".bad"
	if err := os.Rename(path, badPath); err == nil {
		return
	}
	_ = os.Remove(path)
}

func generateMessageID(now time.Time) string {
	seq := atomic.AddUint64(&enqueueSeq, 1)
	return strconv.FormatInt(now.UnixNano(), 10) + "-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatUint(seq, 10)
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return defaultSource
	}
	return source
}

func sanitizeFilenamePart(part string) string {
	part = strings.TrimSpace(part)
	if part == "" {
		return "msg"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '-'
		}
	}, part)
}
