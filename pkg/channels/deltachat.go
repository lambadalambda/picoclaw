package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type DeltaChatChannel struct {
	*BaseChannel
	conn      *websocket.Conn
	config    config.DeltaChatConfig
	url       string
	mu        sync.Mutex
	connected bool
}

func NewDeltaChatChannel(cfg config.DeltaChatConfig, bus *bus.MessageBus) (*DeltaChatChannel, error) {
	base := NewBaseChannel("deltachat", cfg, bus, cfg.AllowFrom)

	return &DeltaChatChannel{
		BaseChannel: base,
		config:      cfg,
		url:         cfg.BridgeURL,
		connected:   false,
	}, nil
}

func (c *DeltaChatChannel) Start(ctx context.Context) error {
	logger.InfoCF("deltachat", "Starting DeltaChat channel", map[string]interface{}{"url": c.url})

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to DeltaChat bridge: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	c.setRunning(true)
	logger.InfoCF("deltachat", "DeltaChat channel connected", nil)

	go c.listen(ctx)

	return nil
}

func (c *DeltaChatChannel) Stop(ctx context.Context) error {
	logger.InfoCF("deltachat", "Stopping DeltaChat channel", nil)

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

	return nil
}

func (c *DeltaChatChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("deltachat connection not established")
	}

	payload := map[string]interface{}{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}
	if len(msg.Media) > 0 {
		payload["media"] = msg.Media
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
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
				time.Sleep(1 * time.Second)
				continue
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				logger.ErrorCF("deltachat", "DeltaChat read error", map[string]interface{}{"error": err.Error()})
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

			if msgType == "message" {
				c.handleIncomingMessage(msg)
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

	if content == "" && len(mediaPaths) == 0 {
		content = "[empty message]"
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
}
