package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

var deltaReactionCommandPattern = regexp.MustCompile(`(?i)^/react\s+([0-9]+)\s+(.+)$`)
var deltaSetProfilePictureCommandPattern = regexp.MustCompile(`(?i)^/(?:set_profile_picture|set_profile_photo)(?:\s+(.+))?$`)

type typingCancel struct {
	fn context.CancelFunc
}

func (c *typingCancel) Cancel() {
	if c == nil || c.fn == nil {
		return
	}
	c.fn()
}

type deltaBridgeAck struct {
	RequestID string
	OK        bool
	Error     string
}

type DeltaChatChannel struct {
	*BaseChannel
	conn           *websocket.Conn
	config         config.DeltaChatConfig
	url            string
	mu             sync.Mutex
	stopTyping     sync.Map // chatID -> typingCancel
	pendingAcks    sync.Map // requestID -> chan deltaBridgeAck
	ackSeq         atomic.Uint64
	typingInterval time.Duration
	ackTimeout     time.Duration
	thinkingDelay  time.Duration
	thinkingText   string
	connected      bool
}

func (c *DeltaChatChannel) connect() error {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to DeltaChat bridge: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := conn.Close(); err != nil {
			logger.ErrorCF("deltachat", "Error closing redundant DeltaChat connection", map[string]interface{}{"error": err.Error()})
		}
		return nil
	}

	c.conn = conn
	c.connected = true

	logger.InfoCF("deltachat", "DeltaChat channel connected", nil)
	return nil
}

func (c *DeltaChatChannel) markDisconnected(conn *websocket.Conn) {
	c.mu.Lock()
	if conn != nil && c.conn != conn {
		c.mu.Unlock()
		return
	}

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			logger.ErrorCF("deltachat", "Error closing DeltaChat connection", map[string]interface{}{"error": err.Error()})
		}
		c.conn = nil
	}

	c.connected = false
	c.mu.Unlock()

	c.failPendingAcks("DeltaChat bridge disconnected")
}

func NewDeltaChatChannel(cfg config.DeltaChatConfig, bus *bus.MessageBus) (*DeltaChatChannel, error) {
	base := NewBaseChannel("deltachat", cfg, bus, cfg.AllowFrom)

	return &DeltaChatChannel{
		BaseChannel:    base,
		config:         cfg,
		url:            cfg.BridgeURL,
		typingInterval: 4 * time.Second,
		ackTimeout:     15 * time.Second,
		thinkingDelay:  3 * time.Second,
		thinkingText:   "thinking...",
		connected:      false,
	}, nil
}

func (c *DeltaChatChannel) Start(ctx context.Context) error {
	logger.InfoCF("deltachat", "Starting DeltaChat channel", map[string]interface{}{"url": c.url})

	if err := c.connect(); err != nil {
		return err
	}

	c.setRunning(true)

	go c.listen(ctx)

	return nil
}

func (c *DeltaChatChannel) Stop(ctx context.Context) error {
	logger.InfoCF("deltachat", "Stopping DeltaChat channel", nil)

	c.stopTyping.Range(func(_, value interface{}) bool {
		if cf, ok := value.(*typingCancel); ok && cf != nil {
			cf.Cancel()
		}
		return true
	})

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			logger.ErrorCF("deltachat", "Error closing DeltaChat connection", map[string]interface{}{"error": err.Error()})
		}
		c.conn = nil
	}

	c.connected = false
	c.setRunning(false)

	c.failPendingAcks("DeltaChat channel stopped")

	return nil
}

func parseDeltaReactionCommand(content string) (string, string, bool) {
	trimmed := strings.TrimSpace(content)
	matches := deltaReactionCommandPattern.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return "", "", false
	}

	reaction := strings.TrimSpace(matches[2])
	if reaction == "" {
		return "", "", false
	}

	return matches[1], reaction, true
}

func parseDeltaSetProfilePictureCommand(content string, media []string) (string, bool, error) {
	trimmed := strings.TrimSpace(content)
	matches := deltaSetProfilePictureCommandPattern.FindStringSubmatch(trimmed)
	if len(matches) == 0 {
		return "", false, nil
	}

	path := ""
	if len(matches) > 1 {
		path = strings.TrimSpace(matches[1])
	}
	if path == "" {
		for _, item := range media {
			candidate := strings.TrimSpace(item)
			if candidate != "" {
				path = candidate
				break
			}
		}
	}

	if path == "" {
		return "", true, fmt.Errorf("DeltaChat profile picture command requires a file path or media attachment")
	}

	return path, true, nil
}

func (c *DeltaChatChannel) nextAckRequestID() string {
	seq := c.ackSeq.Add(1)
	return fmt.Sprintf("req-%d", seq)
}

func (c *DeltaChatChannel) failPendingAcks(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "DeltaChat bridge unavailable"
	}

	c.pendingAcks.Range(func(key, value interface{}) bool {
		requestID, ok := key.(string)
		if !ok {
			return true
		}

		ackCh, ok := value.(chan deltaBridgeAck)
		if !ok {
			c.pendingAcks.Delete(requestID)
			return true
		}

		ack := deltaBridgeAck{
			RequestID: requestID,
			OK:        false,
			Error:     reason,
		}
		select {
		case ackCh <- ack:
		default:
		}

		c.pendingAcks.Delete(requestID)
		return true
	})
}

func (c *DeltaChatChannel) sendPayloadWithAck(ctx context.Context, payload map[string]interface{}) error {
	requestID := c.nextAckRequestID()
	requestPayload := make(map[string]interface{}, len(payload)+2)
	for key, value := range payload {
		requestPayload[key] = value
	}
	requestPayload["request_id"] = requestID
	requestPayload["require_ack"] = true

	ackCh := make(chan deltaBridgeAck, 1)
	c.pendingAcks.Store(requestID, ackCh)
	defer c.pendingAcks.Delete(requestID)

	if err := c.sendPayload(requestPayload); err != nil {
		return err
	}

	ackTimeout := c.ackTimeout
	if ackTimeout <= 0 {
		ackTimeout = 15 * time.Second
	}

	ackTimer := time.NewTimer(ackTimeout)
	defer ackTimer.Stop()

	payloadType, _ := payload["type"].(string)
	if payloadType == "" {
		payloadType = "payload"
	}

	select {
	case ack := <-ackCh:
		if ack.OK {
			return nil
		}

		ackErr := strings.TrimSpace(ack.Error)
		if ackErr == "" {
			ackErr = "bridge rejected request"
		}
		return fmt.Errorf("DeltaChat %s send failed: %s", payloadType, ackErr)
	case <-ackTimer.C:
		return fmt.Errorf("timed out waiting for DeltaChat bridge acknowledgement for %s", payloadType)
	case <-ctx.Done():
		return fmt.Errorf("waiting for DeltaChat bridge acknowledgement interrupted: %w", ctx.Err())
	}
}

func (c *DeltaChatChannel) handleBridgeAck(msg map[string]interface{}) {
	requestID, ok := msg["request_id"].(string)
	if !ok {
		return
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}

	ack := deltaBridgeAck{
		RequestID: requestID,
		OK:        false,
	}

	if okRaw, exists := msg["ok"]; exists {
		if parsedOK, ok := okRaw.(bool); ok {
			ack.OK = parsedOK
		}
	}

	if errRaw, exists := msg["error"]; exists {
		if errText, ok := errRaw.(string); ok {
			ack.Error = strings.TrimSpace(errText)
		}
	}

	if !ack.OK && ack.Error == "" {
		ack.Error = "bridge send failed"
	}

	value, loaded := c.pendingAcks.LoadAndDelete(requestID)
	if !loaded {
		return
	}

	ackCh, ok := value.(chan deltaBridgeAck)
	if !ok {
		return
	}

	select {
	case ackCh <- ack:
	default:
	}
}

func (c *DeltaChatChannel) sendPayload(payload map[string]interface{}) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		if err := c.connect(); err != nil {
			return err
		}

		c.mu.Lock()
		conn = c.conn
		c.mu.Unlock()

		if conn == nil {
			return fmt.Errorf("deltachat connection not established")
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("deltachat connection not established")
	}

	conn = c.conn
	err = conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		if c.conn == conn {
			c.conn = nil
			c.connected = false
		}
	}
	c.mu.Unlock()

	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.ErrorCF("deltachat", "Error closing DeltaChat connection after send failure", map[string]interface{}{"error": closeErr.Error()})
		}
		return fmt.Errorf("failed to send payload: %w", err)
	}

	return nil
}

func (c *DeltaChatChannel) sendTyping(chatID string, isTyping bool) error {
	payload := map[string]interface{}{
		"type":   "typing",
		"to":     chatID,
		"typing": isTyping,
	}
	if isTyping {
		payload["content"] = "..."
	}
	return c.sendPayload(payload)
}

func (c *DeltaChatChannel) stopTypingIndicator(chatID string) {
	stopped := false
	if stop, ok := c.stopTyping.Load(chatID); ok {
		if cf, ok := stop.(*typingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopTyping.Delete(chatID)
		stopped = true
	}

	if !stopped {
		return
	}

	if err := c.sendTyping(chatID, false); err != nil {
		logger.DebugCF("deltachat", "Failed to send stop typing signal", map[string]interface{}{"chat_id": chatID, "error": err.Error()})
	}
}

func (c *DeltaChatChannel) startTypingIndicator(chatID string) {
	c.stopTypingIndicator(chatID)

	interval := c.typingInterval
	if interval <= 0 {
		interval = 4 * time.Second
	}

	typingCtx, typingCancelFn := context.WithTimeout(context.Background(), 5*time.Minute)
	c.stopTyping.Store(chatID, &typingCancel{fn: typingCancelFn})

	if err := c.sendTyping(chatID, true); err != nil {
		logger.DebugCF("deltachat", "Failed to send typing signal", map[string]interface{}{"chat_id": chatID, "error": err.Error()})
	}

	thinkingText := strings.TrimSpace(c.thinkingText)
	if c.thinkingDelay > 0 && thinkingText != "" {
		go func(typingCtx context.Context, targetChatID string, delay time.Duration, content string) {
			timer := time.NewTimer(delay)
			defer timer.Stop()

			select {
			case <-typingCtx.Done():
				return
			case <-timer.C:
			}

			select {
			case <-typingCtx.Done():
				return
			default:
			}

			payload := map[string]interface{}{
				"type":    "message",
				"to":      targetChatID,
				"content": content,
			}
			if err := c.sendPayload(payload); err != nil {
				logger.DebugCF("deltachat", "Failed to send thinking fallback message", map[string]interface{}{"chat_id": targetChatID, "error": err.Error()})
			}
		}(typingCtx, chatID, c.thinkingDelay, thinkingText)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				if err := c.sendTyping(chatID, true); err != nil {
					logger.DebugCF("deltachat", "Failed to refresh typing signal", map[string]interface{}{"chat_id": chatID, "error": err.Error()})
				}
			}
		}
	}()
}

func (c *DeltaChatChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	c.stopTypingIndicator(msg.ChatID)

	if profileImagePath, isCommand, commandErr := parseDeltaSetProfilePictureCommand(msg.Content, msg.Media); isCommand {
		if commandErr != nil {
			return commandErr
		}

		profileImagePayload := map[string]interface{}{
			"type": "profile_image",
			"path": profileImagePath,
		}
		return c.sendPayloadWithAck(ctx, profileImagePayload)
	}

	if messageID, reaction, ok := parseDeltaReactionCommand(msg.Content); ok && len(msg.Media) == 0 {
		reactionPayload := map[string]interface{}{
			"type":       "reaction",
			"to":         msg.ChatID,
			"message_id": messageID,
			"reaction":   reaction,
		}
		return c.sendPayloadWithAck(ctx, reactionPayload)
	}

	payload := map[string]interface{}{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}
	if len(msg.Media) > 0 {
		payload["media"] = msg.Media
	}

	return c.sendPayloadWithAck(ctx, payload)
}

func (c *DeltaChatChannel) listen(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()

			if conn == nil {
				if err := c.connect(); err != nil {
					logger.ErrorCF("deltachat", "DeltaChat reconnect failed", map[string]interface{}{"error": err.Error()})
					time.Sleep(2 * time.Second)
				}
				continue
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				logger.ErrorCF("deltachat", "DeltaChat read error", map[string]interface{}{"error": err.Error()})
				c.markDisconnected(conn)
				time.Sleep(2 * time.Second)
				continue
			}

			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				logger.ErrorCF("deltachat", "Failed to unmarshal DeltaChat message", map[string]interface{}{"error": err.Error()})
				continue
			}

			msgType, ok := msg["type"].(string)
			if !ok {
				continue
			}

			switch msgType {
			case "message":
				c.handleIncomingMessage(msg)
			case "reaction":
				c.handleIncomingReaction(msg)
			case "ack":
				c.handleBridgeAck(msg)
			}
		}
	}
}

func (c *DeltaChatChannel) handleIncomingMessage(msg map[string]interface{}) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	content, ok := msg["content"].(string)
	if !ok {
		content = ""
	}

	var mediaPaths []string
	if mediaData, ok := msg["media"].([]interface{}); ok {
		mediaPaths = make([]string, 0, len(mediaData))
		for _, m := range mediaData {
			if path, ok := m.(string); ok {
				mediaPaths = append(mediaPaths, path)
			}
		}
	}

	if content == "" {
		if len(mediaPaths) > 0 {
			content = "[file]"
		} else {
			content = "[empty message]"
		}
	}

	metadata := make(map[string]string)
	if messageID, ok := msg["id"].(string); ok {
		metadata["message_id"] = messageID
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	logger.DebugCF("deltachat", "Received message", map[string]interface{}{"sender": senderID, "preview": utils.Truncate(content, 50)})

	c.HandleMessage(senderID, chatID, content, mediaPaths, metadata)
	c.startTypingIndicator(chatID)
}

func (c *DeltaChatChannel) handleIncomingReaction(msg map[string]interface{}) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	reactedMessageID := ""
	if messageID, ok := msg["message_id"].(string); ok {
		reactedMessageID = messageID
	} else if messageID, ok := msg["id"].(string); ok {
		reactedMessageID = messageID
	}

	reaction := ""
	if singleReaction, ok := msg["reaction"].(string); ok {
		reaction = strings.TrimSpace(singleReaction)
	}
	if reaction == "" {
		if reactionList, ok := msg["reactions"].([]interface{}); ok {
			emojis := make([]string, 0, len(reactionList))
			for _, item := range reactionList {
				if emoji, ok := item.(string); ok && strings.TrimSpace(emoji) != "" {
					emojis = append(emojis, strings.TrimSpace(emoji))
				}
			}
			reaction = strings.Join(emojis, " ")
		}
	}

	content := "[reaction]"
	if reactedMessageID != "" {
		if reaction != "" {
			content = fmt.Sprintf("[reaction to %s] %s", reactedMessageID, reaction)
		} else {
			content = fmt.Sprintf("[reaction to %s]", reactedMessageID)
		}
	} else if reaction != "" {
		content = fmt.Sprintf("[reaction] %s", reaction)
	}

	metadata := map[string]string{
		"event": "reaction",
	}
	if reactedMessageID != "" {
		metadata["reacted_message_id"] = reactedMessageID
	}
	if reaction != "" {
		metadata["reaction"] = reaction
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	logger.DebugCF("deltachat", "Received reaction", map[string]interface{}{"sender": senderID, "reaction": reaction, "message_id": reactedMessageID})

	c.HandleMessage(senderID, chatID, content, nil, metadata)
}
