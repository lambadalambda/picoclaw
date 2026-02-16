package bus

import (
	"context"
	"testing"
	"time"
)

func TestMessageBus_PublishInboundAfterClose_DoesNotPanic(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	didPanic := false
	func() {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		mb.PublishInbound(InboundMessage{Channel: "test", ChatID: "chat", Content: "hello"})
	}()

	if didPanic {
		t.Fatal("PublishInbound should not panic after Close")
	}
}

func TestMessageBus_PublishOutboundAfterClose_DoesNotPanic(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	didPanic := false
	func() {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		mb.PublishOutbound(OutboundMessage{Channel: "test", ChatID: "chat", Content: "hello"})
	}()

	if didPanic {
		t.Fatal("PublishOutbound should not panic after Close")
	}
}

func TestMessageBus_ConsumeInboundAfterClose_ReturnsFalse(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, ok := mb.ConsumeInbound(ctx)
	if ok {
		t.Fatal("ConsumeInbound should return ok=false after Close")
	}
}

func TestMessageBus_SubscribeOutboundAfterClose_ReturnsFalse(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, ok := mb.SubscribeOutbound(ctx)
	if ok {
		t.Fatal("SubscribeOutbound should return ok=false after Close")
	}
}
