package channels

import (
	"context"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestBaseChannel_NameAndPermissions(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	bc := NewBaseChannel("telegram", nil, mb, []string{"allowed-user"})

	if bc.Name() != "telegram" {
		t.Fatalf("expected channel name %q, got %q", "telegram", bc.Name())
	}

	if !bc.IsAllowed("allowed-user") {
		t.Error("expected allowed-user to be permitted")
	}

	if bc.IsAllowed("blocked-user") {
		t.Error("expected blocked-user to be denied")
	}

	open := NewBaseChannel("telegram", nil, mb, nil)
	if !open.IsAllowed("anyone") {
		t.Error("expected allow list empty to permit all")
	}
}

func TestBaseChannel_HandleMessage(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	bc := NewBaseChannel("telegram", nil, mb, []string{"allowed-user"})
	bc.HandleMessage("allowed-user", "chat-1", "hello", []string{"m1"}, map[string]string{"kind": "test"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected inbound message to be published")
	}
	if msg.Channel != "telegram" {
		t.Errorf("channel = %q, want %q", msg.Channel, "telegram")
	}
	if msg.SenderID != "allowed-user" {
		t.Errorf("sender = %q, want %q", msg.SenderID, "allowed-user")
	}
	if msg.ChatID != "chat-1" {
		t.Errorf("chat = %q, want %q", msg.ChatID, "chat-1")
	}
	if msg.SessionKey != "telegram:chat-1" {
		t.Errorf("session key = %q, want %q", msg.SessionKey, "telegram:chat-1")
	}
	if msg.Content != "hello" {
		t.Errorf("content = %q, want %q", msg.Content, "hello")
	}
	if len(msg.Media) != 1 || msg.Media[0] != "m1" {
		t.Errorf("media = %v, want [m1]", msg.Media)
	}
	if msg.Metadata["kind"] != "test" {
		t.Errorf("metadata kind = %q, want %q", msg.Metadata["kind"], "test")
	}
}

func TestBaseChannel_HandleMessageBlockedSenderDoesNotPublish(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	bc := NewBaseChannel("telegram", nil, mb, []string{"allowed-user"})
	bc.HandleMessage("blocked-user", "chat-2", "hello", nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatal("expected inbound message to be blocked")
	}
}
