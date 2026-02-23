package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coercionCaptureTool struct {
	lastArgs map[string]interface{}
}

func (t *coercionCaptureTool) Name() string {
	return "coerce_probe"
}

func (t *coercionCaptureTool) Description() string {
	return "test tool for argument coercion"
}

func (t *coercionCaptureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"count": map[string]interface{}{
				"type": "integer",
			},
			"deliver": map[string]interface{}{
				"type": "boolean",
			},
		},
		"required": []string{"count", "deliver"},
	}
}

func (t *coercionCaptureTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	t.lastArgs = args
	return "ok", nil
}

func TestToolRegistry_ValidationMissingRequiredReturnsGuidance(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&WriteFileTool{})

	_, err := registry.ExecuteWithContext(context.Background(), "write_file", map[string]interface{}{
		"path": "/tmp/out.txt",
	}, "", "")
	if err == nil {
		t.Fatal("expected missing required parameter error")
	}
	if !strings.Contains(err.Error(), "Missing required parameter: content") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "Supply correct parameters before retrying.") {
		t.Fatalf("expected retry guidance, got: %v", err)
	}
}

func TestToolRegistry_NormalizesMessageAliases(t *testing.T) {
	registry := NewToolRegistry()
	messageTool := NewMessageTool()
	registry.Register(messageTool)

	gotChannel := ""
	gotChatID := ""
	gotContent := ""
	messageTool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		gotChannel = channel
		gotChatID = chatID
		gotContent = content
		return nil
	})

	_, err := registry.ExecuteWithContext(context.Background(), "message", map[string]interface{}{
		"message": "hello there",
		"chatId":  "42",
	}, "deltachat", "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotChannel != "deltachat" {
		t.Fatalf("channel = %q, want deltachat", gotChannel)
	}
	if gotChatID != "42" {
		t.Fatalf("chat_id = %q, want 42", gotChatID)
	}
	if gotContent != "hello there" {
		t.Fatalf("content = %q, want hello there", gotContent)
	}
}

func TestToolRegistry_CoercesNumericAndBooleanArgs(t *testing.T) {
	registry := NewToolRegistry()
	probe := &coercionCaptureTool{}
	registry.Register(probe)

	result, err := registry.ExecuteWithContext(context.Background(), "coerce_probe", map[string]interface{}{
		"count":   "5",
		"deliver": "true",
	}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q, want ok", result)
	}

	count, ok := probe.lastArgs["count"].(float64)
	if !ok || count != 5 {
		t.Fatalf("count = %#v, want float64(5)", probe.lastArgs["count"])
	}
	deliver, ok := probe.lastArgs["deliver"].(bool)
	if !ok || !deliver {
		t.Fatalf("deliver = %#v, want bool(true)", probe.lastArgs["deliver"])
	}
}

func TestToolRegistry_InvalidTypeReturnsGuidance(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&coercionCaptureTool{})

	_, err := registry.ExecuteWithContext(context.Background(), "coerce_probe", map[string]interface{}{
		"count":   "five",
		"deliver": true,
	}, "", "")
	if err == nil {
		t.Fatal("expected invalid parameter error")
	}
	if !strings.Contains(err.Error(), "Invalid parameter 'count': expected integer") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "Supply correct parameters before retrying.") {
		t.Fatalf("expected retry guidance, got: %v", err)
	}
}

func TestToolRegistry_NormalizesReadFileAliases(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&ReadFileTool{})

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sample.txt")
	content := "line 1\nline 2\nline 3\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	result, err := registry.ExecuteWithContext(context.Background(), "read_file", map[string]interface{}{
		"filePath":  path,
		"startLine": "2",
		"maxLines":  "1",
	}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "line 2\n" {
		t.Fatalf("result = %q, want line 2", result)
	}
}
