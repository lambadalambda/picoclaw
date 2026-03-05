package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestRegisterMessageTool_PublishesOutbound(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()
	registry := NewToolRegistry()
	RegisterMessageTool(registry, msgBus, workspace, MessageToolOptions{})

	_, err := registry.ExecuteWithContext(context.Background(), "message", map[string]interface{}{
		"content": "hi",
	}, "telegram", "chat1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, ok := msgBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message")
	}
	if out.Channel != "telegram" {
		t.Fatalf("channel=%q, want %q", out.Channel, "telegram")
	}
	if out.ChatID != "chat1" {
		t.Fatalf("chat_id=%q, want %q", out.ChatID, "chat1")
	}
	if out.Content != "hi" {
		t.Fatalf("content=%q, want %q", out.Content, "hi")
	}
}

func TestRegisterMessageTool_ForceContextTarget_IgnoresOverride(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()
	registry := NewToolRegistry()
	RegisterMessageTool(registry, msgBus, workspace, MessageToolOptions{ForceContextTarget: true})

	_, err := registry.ExecuteWithContext(context.Background(), "message", map[string]interface{}{
		"content": "hi",
		"channel": "telegram",
		"chat_id": "override",
	}, "deltachat", "chat1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, ok := msgBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message")
	}
	if out.Channel != "deltachat" {
		t.Fatalf("channel=%q, want %q", out.Channel, "deltachat")
	}
	if out.ChatID != "chat1" {
		t.Fatalf("chat_id=%q, want %q", out.ChatID, "chat1")
	}
}

func TestRegisterMessageTool_RestrictMediaToWorkspace_BlocksOutsideAbsolutePath(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()
	registry := NewToolRegistry()
	RegisterMessageTool(registry, msgBus, workspace, MessageToolOptions{RestrictMediaToWorkspace: true})

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("nope"), 0644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	result, err := registry.ExecuteWithContext(context.Background(), "message", map[string]interface{}{
		"content": "attempt",
		"media":   []interface{}{outsideFile},
	}, "telegram", "chat1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) < 6 || result[:6] != "Error:" {
		t.Fatalf("expected error result, got %q", result)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := msgBus.SubscribeOutbound(ctx); ok {
		t.Fatal("expected no outbound message")
	}
}
