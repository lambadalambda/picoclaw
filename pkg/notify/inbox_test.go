package notify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/routing"
)

func TestEnqueue_WritesInboxFile(t *testing.T) {
	workspace := t.TempDir()

	id, err := Enqueue(workspace, QueueMessage{
		Source:  "opencode",
		Content: "Build failed",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty message ID")
	}

	entries, err := os.ReadDir(InboxDir(workspace))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 inbox file, got %d", len(entries))
	}
}

func TestInboxService_ProcessPending_UsesLastTarget(t *testing.T) {
	workspace := t.TempDir()
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	if err := cron.SaveLastTarget(cron.LastTargetPath(workspace), cron.LastTarget{Channel: "telegram", ChatID: "chat-1"}); err != nil {
		t.Fatalf("SaveLastTarget error = %v", err)
	}

	id, err := Enqueue(workspace, QueueMessage{Source: "opencode", Content: "Build failed"})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	svc := NewInboxService(workspace, msgBus, ServiceOptions{MinIntervalPerSource: time.Minute})
	svc.processPending()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected inbound message")
	}
	if msg.Channel != "telegram" {
		t.Fatalf("channel = %q, want %q", msg.Channel, "telegram")
	}
	if msg.ChatID != "chat-1" {
		t.Fatalf("chat_id = %q, want %q", msg.ChatID, "chat-1")
	}
	if msg.SenderID != "local:opencode" {
		t.Fatalf("sender_id = %q, want %q", msg.SenderID, "local:opencode")
	}
	if msg.SessionKey != routing.EncodeSystemRoute("telegram", "chat-1") {
		t.Fatalf("session_key = %q, want %q", msg.SessionKey, routing.EncodeSystemRoute("telegram", "chat-1"))
	}
	if msg.Content != "[local:opencode] Build failed" {
		t.Fatalf("content = %q, want %q", msg.Content, "[local:opencode] Build failed")
	}
	if msg.Metadata == nil || msg.Metadata["local_message_id"] != id {
		t.Fatalf("expected local_message_id metadata = %q", id)
	}

	entries, err := os.ReadDir(InboxDir(workspace))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected inbox to be empty after delivery, got %d files", len(entries))
	}
}

func TestInboxService_ProcessPending_NoTargetKeepsMessage(t *testing.T) {
	workspace := t.TempDir()
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	if _, err := Enqueue(workspace, QueueMessage{Source: "opencode", Content: "Need help"}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	svc := NewInboxService(workspace, msgBus, ServiceOptions{})
	svc.processPending()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, ok := msgBus.ConsumeInbound(ctx); ok {
		t.Fatal("expected no inbound message when no target is available")
	}

	entries, err := os.ReadDir(InboxDir(workspace))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected inbox file to remain queued, got %d files", len(entries))
	}
}

func TestInboxService_ProcessPending_RateLimitPerSource(t *testing.T) {
	workspace := t.TempDir()
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	if err := cron.SaveLastTarget(cron.LastTargetPath(workspace), cron.LastTarget{Channel: "telegram", ChatID: "chat-1"}); err != nil {
		t.Fatalf("SaveLastTarget error = %v", err)
	}

	if _, err := Enqueue(workspace, QueueMessage{Source: "opencode", Content: "first"}); err != nil {
		t.Fatalf("Enqueue first error = %v", err)
	}
	if _, err := Enqueue(workspace, QueueMessage{Source: "opencode", Content: "second"}); err != nil {
		t.Fatalf("Enqueue second error = %v", err)
	}

	svc := NewInboxService(workspace, msgBus, ServiceOptions{MinIntervalPerSource: time.Minute})
	svc.processPending()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, ok := msgBus.ConsumeInbound(ctx); !ok {
		t.Fatal("expected first inbound message")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if _, ok := msgBus.ConsumeInbound(ctx2); ok {
		t.Fatal("expected second message to be rate-limited")
	}

	entries, err := os.ReadDir(InboxDir(workspace))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one queued message remaining, got %d", len(entries))
	}
}

func TestInboxService_ProcessPending_ExplicitTargetOverridesLastTarget(t *testing.T) {
	workspace := t.TempDir()
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	if err := cron.SaveLastTarget(cron.LastTargetPath(workspace), cron.LastTarget{Channel: "telegram", ChatID: "chat-1"}); err != nil {
		t.Fatalf("SaveLastTarget error = %v", err)
	}

	if _, err := Enqueue(workspace, QueueMessage{
		Source:  "opencode",
		Content: "to explicit target",
		Channel: "deltachat",
		ChatID:  "42",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	svc := NewInboxService(workspace, msgBus, ServiceOptions{})
	svc.processPending()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msg, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected inbound message")
	}
	if msg.Channel != "deltachat" || msg.ChatID != "42" {
		t.Fatalf("target = %s:%s, want %s:%s", msg.Channel, msg.ChatID, "deltachat", "42")
	}
}

func TestEnqueue_RejectsPartialExplicitTarget(t *testing.T) {
	workspace := t.TempDir()

	_, err := Enqueue(workspace, QueueMessage{Source: "opencode", Content: "x", Channel: "telegram"})
	if err == nil {
		t.Fatal("expected error for partial explicit target")
	}

	entries, err := os.ReadDir(filepath.Join(workspace, "inbox"))
	if err == nil && len(entries) > 0 {
		t.Fatalf("expected no queued files, found %d", len(entries))
	}
}
