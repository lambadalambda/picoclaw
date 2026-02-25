package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCompactTool_Name(t *testing.T) {
	tool := NewCompactTool(nil)
	if tool.Name() != "compact" {
		t.Errorf("expected name 'compact', got %q", tool.Name())
	}
}

func TestCompactTool_Description(t *testing.T) {
	tool := NewCompactTool(nil)
	desc := tool.Description()
	if !strings.Contains(desc, "compaction") {
		t.Errorf("expected description to mention compaction, got: %s", desc)
	}
}

func TestCompactTool_Parameters(t *testing.T) {
	tool := NewCompactTool(nil)
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["mode"]; !ok {
		t.Error("expected 'mode' parameter")
	}
	if _, ok := props["keep_last"]; !ok {
		t.Error("expected 'keep_last' parameter")
	}
}

func TestCompactTool_NoCallback(t *testing.T) {
	tool := NewCompactTool(nil)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"__context_channel": "telegram",
		"__context_chat_id": "123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "not configured") {
		t.Errorf("expected 'not configured' message, got: %s", result)
	}
}

func TestCompactTool_SoftMode(t *testing.T) {
	called := false
	var capturedMode CompactMode
	var capturedKeepLast int

	callback := func(sessionKey string, mode CompactMode, keepLast int) (string, error) {
		called = true
		capturedMode = mode
		capturedKeepLast = keepLast
		return "Test summary", nil
	}

	tool := NewCompactTool(callback)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"__context_channel": "telegram",
		"__context_chat_id": "123",
		"mode":              "soft",
		"keep_last":         10,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("callback was not called")
	}
	if capturedMode != CompactModeSoft {
		t.Errorf("expected soft mode, got: %s", capturedMode)
	}
	if capturedKeepLast != 10 {
		t.Errorf("expected keepLast 10, got: %d", capturedKeepLast)
	}
	if !strings.Contains(result, "soft mode") {
		t.Errorf("expected 'soft mode' in result, got: %s", result)
	}
}

func TestCompactTool_HardMode(t *testing.T) {
	var capturedKeepLast int

	callback := func(sessionKey string, mode CompactMode, keepLast int) (string, error) {
		capturedKeepLast = keepLast
		return "Test summary", nil
	}

	tool := NewCompactTool(callback)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"__context_channel": "telegram",
		"__context_chat_id": "123",
		"mode":              "hard",
		"keep_last":         10,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedKeepLast != 0 {
		t.Errorf("hard mode should force keepLast to 0, got: %d", capturedKeepLast)
	}
}

func TestCompactTool_DefaultKeepLast(t *testing.T) {
	var capturedKeepLast int

	callback := func(sessionKey string, mode CompactMode, keepLast int) (string, error) {
		capturedKeepLast = keepLast
		return "Test summary", nil
	}

	tool := NewCompactTool(callback)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"__context_channel": "telegram",
		"__context_chat_id": "123",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedKeepLast != 4 {
		t.Errorf("expected default keepLast 4, got: %d", capturedKeepLast)
	}
}

func TestCompactTool_KeepLastBounds(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int
	}{
		{"negative", -5, 0},
		{"zero", 0, 0},
		{"normal", 10, 10},
		{"over max", 100, 50},
		{"float", 7.0, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedKeepLast int
			callback := func(sessionKey string, mode CompactMode, keepLast int) (string, error) {
				capturedKeepLast = keepLast
				return "Test summary", nil
			}

			tool := NewCompactTool(callback)
			_, err := tool.Execute(context.Background(), map[string]interface{}{
				"__context_channel": "telegram",
				"__context_chat_id": "123",
				"keep_last":         tt.input,
			})

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capturedKeepLast != tt.expected {
				t.Errorf("expected keepLast %d, got: %d", tt.expected, capturedKeepLast)
			}
		})
	}
}

func TestCompactTool_MissingContext(t *testing.T) {
	tool := NewCompactTool(func(sessionKey string, mode CompactMode, keepLast int) (string, error) {
		return "should not be called", nil
	})

	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing context")
	}
}

func TestCompactTool_SessionKeyFormat(t *testing.T) {
	var capturedKey string
	callback := func(sessionKey string, mode CompactMode, keepLast int) (string, error) {
		capturedKey = sessionKey
		return "Test summary", nil
	}

	tool := NewCompactTool(callback)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"__context_channel": "deltachat",
		"__context_chat_id": "42",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedKey != "deltachat:42" {
		t.Errorf("expected session key 'deltachat:42', got: %s", capturedKey)
	}
}
