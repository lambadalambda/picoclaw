package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

// collectOutbound drains outbound messages from the bus for the given duration.
func collectOutbound(b *bus.MessageBus, dur time.Duration) []bus.OutboundMessage {
	var msgs []bus.OutboundMessage
	deadline := time.After(dur)
	for {
		select {
		case <-deadline:
			return msgs
		default:
			// Try non-blocking read via a short-lived context
			ctx, cancel := newShortCtx()
			msg, ok := b.SubscribeOutbound(ctx)
			cancel()
			if ok {
				msgs = append(msgs, msg)
			}
		}
	}
}

func newShortCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Millisecond)
}

func TestStatusNotifier_SendsAfterDelay(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	n := newStatusNotifier(msgBus, "telegram", "chat123", 50*time.Millisecond)
	n.start("exec")

	// Wait long enough for the notifier to fire
	time.Sleep(120 * time.Millisecond)
	n.stop()

	msgs := collectOutbound(msgBus, 50*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("expected at least one status message, got none")
	}

	got := msgs[0]
	if got.Channel != "telegram" {
		t.Errorf("channel = %q, want %q", got.Channel, "telegram")
	}
	if got.ChatID != "chat123" {
		t.Errorf("chatID = %q, want %q", got.ChatID, "chat123")
	}
	if got.Content == "" {
		t.Error("expected non-empty status content")
	}
	// Should NOT contain tool name — keep user-facing messages generic
	if contains(got.Content, "exec") {
		t.Errorf("status content %q should not expose tool name to user", got.Content)
	}
}

func TestStatusNotifier_NoMessageBeforeDelay(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	n := newStatusNotifier(msgBus, "telegram", "chat123", 200*time.Millisecond)
	n.start("exec")

	// Stop well before the delay fires
	time.Sleep(30 * time.Millisecond)
	n.stop()

	msgs := collectOutbound(msgBus, 50*time.Millisecond)
	if len(msgs) != 0 {
		t.Errorf("expected no status messages before delay, got %d", len(msgs))
	}
}

func TestStatusNotifier_ResetExtendsDelay(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	n := newStatusNotifier(msgBus, "telegram", "chat123", 100*time.Millisecond)
	n.start("exec")

	// Reset at 60ms — this should push the next fire to 160ms from start
	time.Sleep(60 * time.Millisecond)
	n.reset("web_search")

	// At 120ms from start (60ms after reset), should NOT have fired yet
	time.Sleep(60 * time.Millisecond)
	msgs := collectOutbound(msgBus, 10*time.Millisecond)
	if len(msgs) != 0 {
		t.Errorf("expected no status message before reset delay expires, got %d", len(msgs))
	}

	// Wait for the reset delay to expire
	time.Sleep(60 * time.Millisecond)
	n.stop()

	msgs = collectOutbound(msgBus, 50*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatal("expected status message after reset delay expired, got none")
	}

	// Should NOT contain tool name — keep user-facing messages generic
	if contains(msgs[0].Content, "web_search") {
		t.Errorf("status content %q should not expose tool name to user", msgs[0].Content)
	}
}

func TestStatusNotifier_StopIsIdempotent(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	n := newStatusNotifier(msgBus, "telegram", "chat123", 50*time.Millisecond)
	n.start("exec")

	// Multiple stops should not panic
	n.stop()
	n.stop()
	n.stop()
}

func TestStatusNotifier_RepeatsIfNotStopped(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	n := newStatusNotifier(msgBus, "telegram", "chat123", 40*time.Millisecond)
	n.start("exec")

	// Wait long enough for at least 2 firings
	time.Sleep(130 * time.Millisecond)
	n.stop()

	msgs := collectOutbound(msgBus, 50*time.Millisecond)
	if len(msgs) < 2 {
		t.Errorf("expected at least 2 status messages (repeating), got %d", len(msgs))
	}
}

func TestStatusNotifier_ConcurrentSafety(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	n := newStatusNotifier(msgBus, "telegram", "chat123", 20*time.Millisecond)
	n.start("exec")

	var wg sync.WaitGroup
	// Hammer reset and stop concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.reset("tool_" + time.Now().Format("05.000"))
		}()
	}
	wg.Wait()
	n.stop()
	// Test passes if no race/panic
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
