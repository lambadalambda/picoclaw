package agent

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// statusNotifier sends periodic status messages to a channel when tool
// execution takes longer than a configured delay. It resets its timer
// each time the active tool changes, and repeats the notification every
// interval until stopped.
type statusNotifier struct {
	bus     *bus.MessageBus
	channel string
	chatID  string
	delay   time.Duration

	mu        sync.Mutex
	toolName  string
	completed int
	total     int
	timer     *time.Timer
	done      chan struct{}
	stopped   bool
}

// newStatusNotifier creates a notifier that will publish status messages
// to the given bus/channel/chatID after delay elapses without a reset.
func newStatusNotifier(b *bus.MessageBus, channel, chatID string, delay time.Duration) *statusNotifier {
	return &statusNotifier{
		bus:     b,
		channel: channel,
		chatID:  chatID,
		delay:   delay,
		done:    make(chan struct{}),
	}
}

// start begins the status timer for the given tool name.
func (sn *statusNotifier) start(toolName string) {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	sn.toolName = toolName
	sn.completed = 0
	if totalHint := parseToolBatchHint(toolName); totalHint > 0 {
		sn.total = totalHint
	}
	sn.stopped = false
	sn.timer = time.NewTimer(sn.delay)

	go sn.loop()
}

// reset restarts the timer with a new tool name. If the notifier has
// already been stopped this is a no-op.
func (sn *statusNotifier) reset(toolName string) {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	if sn.stopped {
		return
	}

	sn.toolName = toolName
	if totalHint := parseToolBatchHint(toolName); totalHint > 0 {
		sn.total = totalHint
	}

	// Stop and drain the existing timer, then reset it.
	if !sn.timer.Stop() {
		select {
		case <-sn.timer.C:
		default:
		}
	}
	sn.timer.Reset(sn.delay)
}

func (sn *statusNotifier) setProgress(completed, total int) {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	if sn.stopped {
		return
	}

	if total > 0 {
		sn.total = total
	}

	if completed < 0 {
		completed = 0
	}
	if sn.total > 0 && completed > sn.total {
		completed = sn.total
	}
	sn.completed = completed
}

// stop terminates the notifier. It is safe to call multiple times.
func (sn *statusNotifier) stop() {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	if sn.stopped {
		return
	}
	sn.stopped = true
	close(sn.done)
	sn.timer.Stop()
}

// loop runs in a goroutine, waiting for the timer to fire or stop to be called.
func (sn *statusNotifier) loop() {
	for {
		select {
		case <-sn.done:
			return
		case <-sn.timer.C:
			sn.mu.Lock()
			if sn.stopped {
				sn.mu.Unlock()
				return
			}
			tool := sn.toolName
			completed := sn.completed
			total := sn.total
			sn.timer.Reset(sn.delay)
			sn.mu.Unlock()

			msg := "Still working on it..."
			if total > 0 {
				msg = fmt.Sprintf("Still working... (%d/%d steps done)", completed, total)
			}
			logger.DebugCF("agent", msg, map[string]interface{}{
				"tool":    tool,
				"done":    completed,
				"total":   total,
				"channel": sn.channel,
				"chat_id": sn.chatID,
			})
			sn.bus.PublishOutbound(bus.OutboundMessage{
				Channel: sn.channel,
				ChatID:  sn.chatID,
				Content: msg,
			})
		}
	}
}

func parseToolBatchHint(toolName string) int {
	parts := strings.Fields(strings.TrimSpace(toolName))
	if len(parts) < 2 {
		return 0
	}

	if parts[1] != "tools" && parts[1] != "tool" {
		return 0
	}

	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return 0
	}

	return n
}
