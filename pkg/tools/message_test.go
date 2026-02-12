package tools

import (
	"context"
	"fmt"
	"sync"
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

func TestMessageTool_Execute_TextOnly(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("telegram", "123")

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
	tool.SetContext("telegram", "456")

	var gotMedia []string

	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		gotMedia = media
		return nil
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "here are the files",
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
	tool.SetContext("telegram", "123")
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
	tool.SetContext("telegram", "123")
	// Don't set callback

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
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
	// Don't set context — no default channel/chatID
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
	tool.SetContext("telegram", "123")
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return fmt.Errorf("network error")
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Error sending message: network error" {
		t.Errorf("result = %q, want callback error message", result)
	}
}

func TestMessageTool_SetContextConcurrentWithExecute_NoRace(t *testing.T) {
	tool := NewMessageTool()
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return nil
	})
	tool.SetContext("telegram", "init")

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			tool.SetContext("telegram", fmt.Sprintf("%d", i))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, _ = tool.Execute(ctx, map[string]interface{}{
				"content": "hello",
			})
		}
	}()

	wg.Wait()
}
