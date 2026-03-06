package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/session"
)

type SessionInfoTool struct {
	sessions      SessionInfoProvider
	model         string
	contextWindow int
	maxTokens     int
	isSubagent    bool
	isHeartbeat   bool
}

type SessionInfoProvider interface {
	GetSessionInfo(sessionKey string) *session.SessionInfo
}

func NewSessionInfoTool(sessions SessionInfoProvider, model string, contextWindow, maxTokens int, isSubagent, isHeartbeat bool) *SessionInfoTool {
	return &SessionInfoTool{
		sessions:      sessions,
		model:         model,
		contextWindow: contextWindow,
		maxTokens:     maxTokens,
		isSubagent:    isSubagent,
		isHeartbeat:   isHeartbeat,
	}
}

func (t *SessionInfoTool) Name() string {
	return "session_info"
}

func (t *SessionInfoTool) Description() string {
	return "Get metadata about the current session: channel, chat_id, model, message count, token estimate, compaction count, and whether this is a subagent or heartbeat session."
}

func (t *SessionInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional: explicit session key (defaults to current channel/chat context)",
			},
		},
	}
}

func (t *SessionInfoTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	sessionKey, _ := args["session_key"].(string)
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = strings.TrimSpace(getExecutionSessionKey(args))
	}
	if sessionKey == "" {
		ch, chatID := getExecutionContext(args)
		if ch != "" && chatID != "" {
			sessionKey = fmt.Sprintf("%s:%s", ch, chatID)
		}
	}
	if sessionKey == "" {
		return "", fmt.Errorf("session_key is required (or run within a chat context)")
	}

	info := t.sessions.GetSessionInfo(sessionKey)
	if info == nil {
		return "", fmt.Errorf("session not found: %s", sessionKey)
	}

	output := SessionInfoOutput{
		Channel:         extractChannelFromKey(sessionKey),
		ChatID:          extractChatIDFromKey(sessionKey),
		Model:           t.model,
		SessionKey:      sessionKey,
		MessageCount:    info.MessageCount,
		TokenEstimate:   info.MessageCount * 4,
		MaxTokens:       t.maxTokens,
		ContextWindow:   t.contextWindow,
		CompactionCount: info.CompactionCount,
		SessionStart:    info.Created,
		IsSubagent:      t.isSubagent,
		IsHeartbeat:     t.isHeartbeat,
	}

	if output.TokenEstimate > t.contextWindow {
		output.TokenEstimate = t.contextWindow
	}

	data, err := json.Marshal(output)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

type SessionInfoOutput struct {
	Channel         string    `json:"channel"`
	ChatID          string    `json:"chat_id"`
	Model           string    `json:"model"`
	SessionKey      string    `json:"session_key"`
	MessageCount    int       `json:"message_count"`
	TokenEstimate   int       `json:"token_estimate"`
	MaxTokens       int       `json:"max_tokens"`
	ContextWindow   int       `json:"context_window"`
	CompactionCount int       `json:"compaction_count"`
	SessionStart    time.Time `json:"session_start"`
	IsSubagent      bool      `json:"is_subagent"`
	IsHeartbeat     bool      `json:"is_heartbeat"`
}

func extractChannelFromKey(key string) string {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

func extractChatIDFromKey(key string) string {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
