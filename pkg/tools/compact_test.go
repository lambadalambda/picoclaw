package tools

import (
	"context"
	"testing"
)

func TestCompactTool_Name(t *testing.T) {
	tool := NewCompactTool()
	if tool.Name() != "compact" {
		t.Errorf("Expected name 'compact', got '%s'", tool.Name())
	}
}

func TestCompactTool_Description(t *testing.T) {
	tool := NewCompactTool()
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if len(desc) < 10 {
		t.Errorf("Description seems too short: %s", desc)
	}
}

func TestCompactTool_Parameters(t *testing.T) {
	tool := NewCompactTool()
	params := tool.Parameters()
	if params == nil {
		t.Error("Parameters should not be nil")
	}

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Error("Properties should be a map")
	}

	if _, exists := props["mode"]; !exists {
		t.Error("Should have 'mode' parameter")
	}
}

func TestCompactTool_Execute_NoCallback(t *testing.T) {
	tool := NewCompactTool()
	args := map[string]interface{}{
		"mode":                  "soft",
		"__context_session_key": "test-session",
	}

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("Should return error when callback is not set")
	}
}

func TestCompactTool_Execute_NoSessionKey(t *testing.T) {
	tool := NewCompactTool()
	tool.SetCallback(func(sessionKey string, mode string) error {
		return nil
	})

	args := map[string]interface{}{
		"mode": "soft",
	}

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("Should return error when session key is not available")
	}
}

func TestCompactTool_Execute_SoftMode(t *testing.T) {
	tool := NewCompactTool()
	var receivedSessionKey string
	var receivedMode string

	tool.SetCallback(func(sessionKey string, mode string) error {
		receivedSessionKey = sessionKey
		receivedMode = mode
		return nil
	})

	args := map[string]interface{}{
		"mode":                  "soft",
		"__context_session_key": "test-session-123",
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("Should not return error: %v", err)
	}

	if receivedSessionKey != "test-session-123" {
		t.Errorf("Expected session key 'test-session-123', got '%s'", receivedSessionKey)
	}

	if receivedMode != "soft" {
		t.Errorf("Expected mode 'soft', got '%s'", receivedMode)
	}

	if result == "" {
		t.Error("Result should not be empty")
	}
}

func TestCompactTool_Execute_HardMode(t *testing.T) {
	tool := NewCompactTool()
	var receivedMode string

	tool.SetCallback(func(sessionKey string, mode string) error {
		receivedMode = mode
		return nil
	})

	args := map[string]interface{}{
		"mode":                  "hard",
		"__context_session_key": "test-session-456",
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("Should not return error: %v", err)
	}

	if receivedMode != "hard" {
		t.Errorf("Expected mode 'hard', got '%s'", receivedMode)
	}

	if result == "" {
		t.Error("Result should not be empty")
	}
}

func TestCompactTool_Execute_DefaultMode(t *testing.T) {
	tool := NewCompactTool()
	var receivedMode string

	tool.SetCallback(func(sessionKey string, mode string) error {
		receivedMode = mode
		return nil
	})

	args := map[string]interface{}{
		"__context_session_key": "test-session-789",
	}

	_, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("Should not return error: %v", err)
	}

	if receivedMode != "soft" {
		t.Errorf("Expected default mode 'soft', got '%s'", receivedMode)
	}
}

func TestCompactTool_Execute_InvalidMode(t *testing.T) {
	tool := NewCompactTool()
	tool.SetCallback(func(sessionKey string, mode string) error {
		return nil
	})

	args := map[string]interface{}{
		"mode":                  "invalid",
		"__context_session_key": "test-session",
	}

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("Should return error for invalid mode")
	}
}

func TestCompactTool_Execute_CallbackError(t *testing.T) {
	tool := NewCompactTool()
	tool.SetCallback(func(sessionKey string, mode string) error {
		return context.DeadlineExceeded
	})

	args := map[string]interface{}{
		"mode":                  "soft",
		"__context_session_key": "test-session",
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("Should not return error from Execute: %v", err)
	}

	if result == "" {
		t.Error("Should return error message in result")
	}
}

func TestCompactTool_Execute_CaseInsensitiveMode(t *testing.T) {
	tool := NewCompactTool()
	var receivedMode string

	tool.SetCallback(func(sessionKey string, mode string) error {
		receivedMode = mode
		return nil
	})

	args := map[string]interface{}{
		"mode":                  "HARD",
		"__context_session_key": "test-session",
	}

	_, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("Should not return error: %v", err)
	}

	if receivedMode != "hard" {
		t.Errorf("Expected mode 'hard' (lowercase), got '%s'", receivedMode)
	}
}
