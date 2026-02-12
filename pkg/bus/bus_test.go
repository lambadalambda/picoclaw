package bus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPublishConsumeInbound(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	msg := InboundMessage{Channel: "test", Content: "hello"}
	mb.PublishInbound(msg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected to consume a message")
	}
	if got.Content != "hello" {
		t.Fatalf("expected content 'hello', got %q", got.Content)
	}
}

func TestPublishSubscribeOutbound(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	msg := OutboundMessage{Channel: "test", Content: "world"}
	mb.PublishOutbound(msg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, ok := mb.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected to receive a message")
	}
	if got.Content != "world" {
		t.Fatalf("expected content 'world', got %q", got.Content)
	}
}

func TestConsumeInboundCancelled(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, ok := mb.ConsumeInbound(ctx)
	if ok {
		t.Fatal("expected false from cancelled context")
	}
}

func TestSubscribeOutboundCancelled(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, ok := mb.SubscribeOutbound(ctx)
	if ok {
		t.Fatal("expected false from cancelled context")
	}
}

// TestPublishInboundFullBufferDoesNotBlock verifies that publishing to a full
// inbound channel does not block the caller (it should drop the message).
func TestPublishInboundFullBufferDoesNotBlock(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	// Fill the buffer
	for i := 0; i < 100; i++ {
		mb.PublishInbound(InboundMessage{Content: "fill"})
	}

	// The 101st publish must not block
	done := make(chan struct{})
	go func() {
		mb.PublishInbound(InboundMessage{Content: "overflow"})
		close(done)
	}()

	select {
	case <-done:
		// good — did not block
	case <-time.After(time.Second):
		t.Fatal("PublishInbound blocked on full buffer")
	}
}

// TestPublishOutboundFullBufferDoesNotBlock verifies the same for outbound.
func TestPublishOutboundFullBufferDoesNotBlock(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	for i := 0; i < 100; i++ {
		mb.PublishOutbound(OutboundMessage{Content: "fill"})
	}

	done := make(chan struct{})
	go func() {
		mb.PublishOutbound(OutboundMessage{Content: "overflow"})
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(time.Second):
		t.Fatal("PublishOutbound blocked on full buffer")
	}
}

func TestRegisterAndGetHandler(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	called := false
	mb.RegisterHandler("test", func(msg InboundMessage) error {
		called = true
		return nil
	})

	handler, ok := mb.GetHandler("test")
	if !ok {
		t.Fatal("expected handler to be registered")
	}
	handler(InboundMessage{})
	if !called {
		t.Fatal("expected handler to be called")
	}

	_, ok = mb.GetHandler("nonexistent")
	if ok {
		t.Fatal("expected no handler for nonexistent channel")
	}
}

// TestConcurrentPublishConsume verifies bus works under concurrent access.
func TestConcurrentPublishConsume(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	const n = 50
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// Publish n messages concurrently
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mb.PublishInbound(InboundMessage{Content: "concurrent"})
		}()
	}

	// Consume n messages concurrently
	received := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := mb.ConsumeInbound(ctx); ok {
				received <- struct{}{}
			}
		}()
	}

	wg.Wait()
	if len(received) != n {
		t.Fatalf("expected %d messages, got %d", n, len(received))
	}
}
