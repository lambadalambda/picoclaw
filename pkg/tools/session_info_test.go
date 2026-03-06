package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/session"
)

type mockSessionInfoProvider struct {
	info        *session.SessionInfo
	expectedKey string
}

func (m *mockSessionInfoProvider) GetSessionInfo(sessionKey string) *session.SessionInfo {
	if m.expectedKey != "" && sessionKey != m.expectedKey {
		return nil
	}
	return m.info
}

func TestSessionInfoTool_ReturnsJSON(t *testing.T) {
	now := time.Now()
	info := &session.SessionInfo{
		Key:             "test:slack:chat-1",
		MessageCount:    5,
		CompactionCount: 1,
		Created:         now,
		Updated:         now,
	}
	provider := &mockSessionInfoProvider{info: info, expectedKey: "test:slack:chat-1"}
	tool := NewSessionInfoTool(provider, "test-model", 4000, 1000, false, false)

	args := map[string]interface{}{
		"session_key": "test:slack:chat-1",
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result == "" {
		t.Fatal("Expected non-empty result")
	}

	if !json.Valid([]byte(result)) {
		t.Errorf("Expected valid JSON, got: %s", result)
	}
}

func TestSessionInfoTool_RequiredFields(t *testing.T) {
	now := time.Now()
	info := &session.SessionInfo{
		Key:             "test:slack:chat-1",
		MessageCount:    10,
		CompactionCount: 2,
		Created:         now,
		Updated:         now,
	}
	provider := &mockSessionInfoProvider{info: info, expectedKey: "test:slack:chat-1"}
	tool := NewSessionInfoTool(provider, "test-model", 4000, 100, false, false)

	args := map[string]interface{}{
		"session_key": "test:slack:chat-1",
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var output SessionInfoOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("Failed to unmarshal output: %v", err)
	}

	if output.Channel != "test" {
		t.Errorf("expected channel 'test', got %q", output.Channel)
	}
	if output.ChatID != "slack:chat-1" {
		t.Errorf("expected chatID 'slack:chat-1' (split), got %q", output.ChatID)
	}
	if output.SessionKey != "test:slack:chat-1" {
		t.Errorf("expected session_key 'test:slack:chat-1', got %q", output.SessionKey)
	}
	if output.MessageCount != 10 {
		t.Errorf("expected message_count 10, got %d", output.MessageCount)
	}
	if output.TokenEstimate <= 0 {
		t.Errorf("expected token_estimate > 0, got %d", output.TokenEstimate)
	}
	if output.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", output.Model)
	}
	if output.ContextWindow != 4000 {
		t.Errorf("expected context_window 4000, got %d", output.ContextWindow)
	}
	if output.MaxTokens != 100 {
		t.Errorf("expected max_tokens 100, got %d", output.MaxTokens)
	}
	if output.CompactionCount != 2 {
		t.Errorf("expected compaction_count 2, got %d", output.CompactionCount)
	}
	if output.SessionStart.IsZero() {
		t.Errorf("expected session_start to be non-zero")
	}
}

func TestSessionInfoTool_NoParameters(t *testing.T) {
	tool := NewSessionInfoTool(nil, "test-model", 4000, 100, false, false)

	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Error("expected error when no session context, got nil")
	}
}

func TestSessionInfoTool_SessionNotFound(t *testing.T) {
	provider := &mockSessionInfoProvider{info: nil, expectedKey: "different:key"}
	tool := NewSessionInfoTool(provider, "test-model", 4000, 100, false, false)

	args := map[string]interface{}{
		"session_key": "test:slack:chat-1",
	}
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error when session not found, got nil")
	}
}
