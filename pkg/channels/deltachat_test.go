package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func startDeltaBridge(t *testing.T) (string, <-chan *websocket.Conn, func()) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	connCh := make(chan *websocket.Conn, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connCh <- conn
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	cleanup := func() {
		srv.Close()
	}

	return wsURL, connCh, cleanup
}

func waitDeltaConn(t *testing.T, connCh <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-connCh:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket client connection")
		return nil
	}
}

func TestDeltaChatChannelSend(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL}, mb)
	if err != nil {
		t.Fatalf("NewDeltaChatChannel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer ch.Stop(context.Background())

	conn := waitDeltaConn(t, connCh)
	defer conn.Close()

	out := bus.OutboundMessage{
		Channel: "deltachat",
		ChatID:  "chat-123",
		Content: "hello from picoclaw",
		Media:   []string{"/tmp/a.png"},
	}
	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("bridge read failed: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal bridge payload: %v", err)
	}

	if got["type"] != "message" {
		t.Fatalf("type = %v, want message", got["type"])
	}
	if got["to"] != "chat-123" {
		t.Fatalf("to = %v, want chat-123", got["to"])
	}
	if got["content"] != "hello from picoclaw" {
		t.Fatalf("content = %v, want hello from picoclaw", got["content"])
	}
	media, ok := got["media"].([]interface{})
	if !ok || len(media) != 1 || media[0] != "/tmp/a.png" {
		t.Fatalf("media = %v, want [/tmp/a.png]", got["media"])
	}
}

func TestDeltaChatChannelIncomingMessage(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL}, mb)
	if err != nil {
		t.Fatalf("NewDeltaChatChannel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer ch.Stop(context.Background())

	conn := waitDeltaConn(t, connCh)
	defer conn.Close()

	incoming := map[string]interface{}{
		"type":      "message",
		"id":        "m-1",
		"from":      "dc-user-42",
		"from_name": "Alice",
		"chat":      "chat-42",
		"content":   "hi from delta",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer consumeCancel()

	msg, ok := mb.ConsumeInbound(consumeCtx)
	if !ok {
		t.Fatal("expected inbound message from DeltaChat")
	}

	if msg.Channel != "deltachat" {
		t.Fatalf("channel = %q, want deltachat", msg.Channel)
	}
	if msg.SenderID != "dc-user-42" {
		t.Fatalf("sender = %q, want dc-user-42", msg.SenderID)
	}
	if msg.ChatID != "chat-42" {
		t.Fatalf("chat = %q, want chat-42", msg.ChatID)
	}
	if msg.SessionKey != "deltachat:chat-42" {
		t.Fatalf("session = %q, want deltachat:chat-42", msg.SessionKey)
	}
	if msg.Content != "hi from delta" {
		t.Fatalf("content = %q, want hi from delta", msg.Content)
	}
	if msg.Metadata["message_id"] != "m-1" {
		t.Fatalf("metadata.message_id = %q, want m-1", msg.Metadata["message_id"])
	}
	if msg.Metadata["user_name"] != "Alice" {
		t.Fatalf("metadata.user_name = %q, want Alice", msg.Metadata["user_name"])
	}
}

func TestDeltaChatChannelAllowlist(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{
		Enabled:   true,
		BridgeURL: wsURL,
		AllowFrom: []string{"allowed-user"},
	}, mb)
	if err != nil {
		t.Fatalf("NewDeltaChatChannel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer ch.Stop(context.Background())

	conn := waitDeltaConn(t, connCh)
	defer conn.Close()

	blocked := map[string]interface{}{
		"type":    "message",
		"from":    "blocked-user",
		"chat":    "chat-x",
		"content": "this should be filtered",
	}
	if err := conn.WriteJSON(blocked); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer consumeCancel()

	if _, ok := mb.ConsumeInbound(consumeCtx); ok {
		t.Fatal("expected blocked sender message to be dropped")
	}
}

func TestDeltaChatChannelReconnectAfterDisconnect(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL}, mb)
	if err != nil {
		t.Fatalf("NewDeltaChatChannel failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer ch.Stop(context.Background())

	firstConn := waitDeltaConn(t, connCh)
	if err := firstConn.Close(); err != nil {
		t.Fatalf("failed to close first bridge connection: %v", err)
	}

	var secondConn *websocket.Conn
	select {
	case secondConn = <-connCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for websocket client reconnection")
	}
	defer secondConn.Close()

	out := bus.OutboundMessage{
		Channel: "deltachat",
		ChatID:  "chat-reconnect",
		Content: "hello after reconnect",
	}
	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed after reconnect: %v", err)
	}

	_ = secondConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := secondConn.ReadMessage()
	if err != nil {
		t.Fatalf("bridge read failed after reconnect: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal bridge payload after reconnect: %v", err)
	}

	if got["content"] != "hello after reconnect" {
		t.Fatalf("content = %v, want hello after reconnect", got["content"])
	}
}
