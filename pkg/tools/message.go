package tools

import (
	"context"
	"fmt"
	"sync"
)

type SendCallback func(channel, chatID, content string, media []string) error

type MessageTool struct {
	mu           sync.RWMutex
	sendCallback SendCallback
}

func NewMessageTool() *MessageTool {
	return &MessageTool{}
}

func (t *MessageTool) Name() string {
	return "message"
}

func (t *MessageTool) Description() string {
	return "Send a message (and optionally files/images) to a user on a chat channel. " +
		"Use this when you want to communicate something or share files."
}

func (t *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The message content to send",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel (telegram, whatsapp, etc.)",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
			"media": map[string]interface{}{
				"type":        "array",
				"description": "Optional: list of file paths to send as attachments (images, documents, etc.)",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		"required": []string{"content"},
	}
}

func (t *MessageTool) SetSendCallback(callback SendCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendCallback = callback
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)
	ctxChannel, ctxChatID := getExecutionContext(args)
	if channel == "" {
		channel = ctxChannel
	}
	if chatID == "" {
		chatID = ctxChatID
	}

	t.mu.RLock()
	callback := t.sendCallback
	t.mu.RUnlock()

	if channel == "" || chatID == "" {
		return "Error: No target channel/chat specified", nil
	}

	if callback == nil {
		return "Error: Message sending not configured", nil
	}

	// Extract media paths
	var media []string
	if rawMedia, ok := args["media"]; ok {
		if mediaList, ok := rawMedia.([]interface{}); ok {
			for _, item := range mediaList {
				if path, ok := item.(string); ok {
					media = append(media, path)
				}
			}
		}
	}
	if media == nil {
		media = []string{}
	}

	if err := callback(channel, chatID, content, media); err != nil {
		return fmt.Sprintf("Error sending message: %v", err), nil
	}

	return fmt.Sprintf("Message sent to %s:%s", channel, chatID), nil
}
