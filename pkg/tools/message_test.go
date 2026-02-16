package tools

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMessageTool_Name(t *testing.T) {
	tool := NewMessageTool()
	if tool.Name() != "message" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "message")
	}
}

func TestMessageTool_Parameters_IncludesMedia(t *testing.T) {
	tool := NewMessageTool()
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("parameters should have 'properties'")
	}

	mediaProp, ok := props["media"]
	if !ok {
		t.Fatal("parameters should include 'media' property")
	}

	mediaMap, ok := mediaProp.(map[string]interface{})
	if !ok {
		t.Fatal("media property should be a map")
	}

	if mediaMap["type"] != "array" {
		t.Errorf("media type = %q, want %q", mediaMap["type"], "array")
	}

	items, ok := mediaMap["items"].(map[string]interface{})
	if !ok {
		t.Fatal("media should have 'items' field")
	}
	if items["type"] != "string" {
		t.Errorf("media items type = %q, want %q", items["type"], "string")
	}
}

func TestMessageTool_Execute_UsesExplicitChannelChat(t *testing.T) {
	tool := NewMessageTool()

	var gotChannel, gotChatID, gotContent string
	var gotMedia []string

	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		gotChannel = channel
		gotChatID = chatID
		gotContent = content
		gotMedia = media
		return nil
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello world",
		"channel": "telegram",
		"chat_id": "123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotChannel != "telegram" {
		t.Errorf("channel = %q, want %q", gotChannel, "telegram")
	}
	if gotChatID != "123" {
		t.Errorf("chatID = %q, want %q", gotChatID, "123")
	}
	if gotContent != "hello world" {
		t.Errorf("content = %q, want %q", gotContent, "hello world")
	}
	if len(gotMedia) != 0 {
		t.Errorf("media = %v, want empty slice", gotMedia)
	}
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestMessageTool_Execute_WithMedia(t *testing.T) {
	tool := NewMessageTool()

	var gotMedia []string

	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		gotMedia = media
		return nil
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "here are the files",
		"channel": "telegram",
		"chat_id": "456",
		"media":   []interface{}{"/tmp/photo.jpg", "/tmp/report.pdf"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gotMedia) != 2 {
		t.Fatalf("media length = %d, want 2", len(gotMedia))
	}
	if gotMedia[0] != "/tmp/photo.jpg" {
		t.Errorf("media[0] = %q, want %q", gotMedia[0], "/tmp/photo.jpg")
	}
	if gotMedia[1] != "/tmp/report.pdf" {
		t.Errorf("media[1] = %q, want %q", gotMedia[1], "/tmp/report.pdf")
	}
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestMessageTool_Execute_NoContent(t *testing.T) {
	tool := NewMessageTool()
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return nil
	})

	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing content, got nil")
	}
}

func TestMessageTool_Execute_NoCallback(t *testing.T) {
	tool := NewMessageTool()

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
		"channel": "telegram",
		"chat_id": "123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error: Message sending not configured" {
		t.Errorf("result = %q, want error about sending not configured", result)
	}
}

func TestMessageTool_Execute_NoChannel(t *testing.T) {
	tool := NewMessageTool()
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return nil
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error: No target channel/chat specified" {
		t.Errorf("result = %q, want error about no target", result)
	}
}

func TestMessageTool_Execute_CallbackError(t *testing.T) {
	tool := NewMessageTool()
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return fmt.Errorf("network error")
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
		"channel": "telegram",
		"chat_id": "123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error sending message: network error" {
		t.Errorf("result = %q, want callback error message", result)
	}
}

func TestMessageTool_ExecuteWithRegistryContext(t *testing.T) {
	tool := NewMessageTool()
	registry := NewToolRegistry()
	registry.Register(tool)

	var gotChannel, gotChatID string
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		gotChannel = channel
		gotChatID = chatID
		return nil
	})

	_, err := registry.ExecuteWithContext(context.Background(), "message", map[string]interface{}{
		"content": "hello",
	}, "telegram", "ctx-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotChannel != "telegram" || gotChatID != "ctx-1" {
		t.Fatalf("expected injected context telegram:ctx-1, got %s:%s", gotChannel, gotChatID)
	}
}

func TestMessageTool_ConcurrentExecuteWithDifferentContext_NoCrossTalk(t *testing.T) {
	tool := NewMessageTool()
	registry := NewToolRegistry()
	registry.Register(tool)

	var mismatches atomic.Int32
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		if content != chatID {
			mismatches.Add(1)
		}
		return nil
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chatID := fmt.Sprintf("chat-%d", i)
			_, _ = registry.ExecuteWithContext(ctx, "message", map[string]interface{}{
				"content": chatID,
			}, "telegram", chatID)
		}(i)
	}

	wg.Wait()

	if got := mismatches.Load(); got != 0 {
		t.Fatalf("detected %d context/content mismatches", got)
	}
}
