package bus

import (
	"context"
	"log"
	"sync"
)

type MessageBus struct {
	inbound   chan InboundMessage
	outbound  chan OutboundMessage
	handlers  map[string]MessageHandler
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
	mu        sync.RWMutex
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		inbound:  make(chan InboundMessage, 100),
		outbound: make(chan OutboundMessage, 100),
		handlers: make(map[string]MessageHandler),
		done:     make(chan struct{}),
	}
}

func (mb *MessageBus) PublishInbound(msg InboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}

	select {
	case mb.inbound <- msg:
	default:
		log.Printf("[WARN] bus: inbound channel full, dropping message from %s:%s", msg.Channel, msg.ChatID)
	}
}

func (mb *MessageBus) ConsumeInbound(ctx context.Context) (InboundMessage, bool) {
	mb.mu.RLock()
	closed := mb.closed
	mb.mu.RUnlock()
	if closed {
		return InboundMessage{}, false
	}

	select {
	case msg := <-mb.inbound:
		return msg, true
	case <-mb.done:
		return InboundMessage{}, false
	case <-ctx.Done():
		return InboundMessage{}, false
	}
}

func (mb *MessageBus) PublishOutbound(msg OutboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}

	select {
	case mb.outbound <- msg:
	default:
		log.Printf("[WARN] bus: outbound channel full, dropping message for %s:%s", msg.Channel, msg.ChatID)
	}
}

func (mb *MessageBus) SubscribeOutbound(ctx context.Context) (OutboundMessage, bool) {
	mb.mu.RLock()
	closed := mb.closed
	mb.mu.RUnlock()
	if closed {
		return OutboundMessage{}, false
	}

	select {
	case msg := <-mb.outbound:
		return msg, true
	case <-mb.done:
		return OutboundMessage{}, false
	case <-ctx.Done():
		return OutboundMessage{}, false
	}
}

func (mb *MessageBus) RegisterHandler(channel string, handler MessageHandler) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.handlers[channel] = handler
}

func (mb *MessageBus) GetHandler(channel string) (MessageHandler, bool) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	handler, ok := mb.handlers[channel]
	return handler, ok
}

func (mb *MessageBus) Close() {
	mb.closeOnce.Do(func() {
		mb.mu.Lock()
		mb.closed = true
		close(mb.done)
		mb.mu.Unlock()
	})
}
