package channels

import (
	"context"
	"encoding/json"
	"net"
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

func readDeltaPayload(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]interface{} {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("bridge read failed: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("failed to unmarshal bridge payload: %v", err)
	}
	return payload
}

func waitDeltaPayloadType(t *testing.T, conn *websocket.Conn, expectedType string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		payload := readDeltaPayload(t, conn, remaining)
		if payload["type"] == expectedType {
			return payload
		}
	}

	t.Fatalf("timed out waiting for payload type %q", expectedType)
	return nil
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

func TestDeltaChatChannelIncomingMediaOnlyMessage(t *testing.T) {
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
		"type": "message",
		"id":   "m-media",
		"from": "dc-user-43",
		"chat": "chat-43",
		"media": []interface{}{
			"/accounts/1/blob/image.jpg",
		},
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer consumeCancel()

	msg, ok := mb.ConsumeInbound(consumeCtx)
	if !ok {
		t.Fatal("expected inbound media-only message from DeltaChat")
	}

	if msg.Content != "[file]" {
		t.Fatalf("content = %q, want [file]", msg.Content)
	}
	if len(msg.Media) != 1 || msg.Media[0] != "/accounts/1/blob/image.jpg" {
		t.Fatalf("media = %v, want [/accounts/1/blob/image.jpg]", msg.Media)
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

func TestDeltaChatChannelTypingIndicatorLifecycle(t *testing.T) {
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
		"type":    "message",
		"id":      "m-typing",
		"from":    "dc-user-typing",
		"chat":    "chat-typing",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	typingStart := waitDeltaPayloadType(t, conn, "typing", 2*time.Second)
	if typingStart["typing"] != true {
		t.Fatalf("typing start payload has typing=%v, want true", typingStart["typing"])
	}

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-typing", Content: "response"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	typingStop := waitDeltaPayloadType(t, conn, "typing", 2*time.Second)
	if typingStop["typing"] != false {
		t.Fatalf("typing stop payload has typing=%v, want false", typingStop["typing"])
	}

	messagePayload := waitDeltaPayloadType(t, conn, "message", 2*time.Second)
	if messagePayload["content"] != "response" {
		t.Fatalf("message content = %v, want response", messagePayload["content"])
	}
}

func TestDeltaChatChannelSendsThinkingFallbackForSlowResponses(t *testing.T) {
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
		"type":    "message",
		"id":      "m-thinking",
		"from":    "dc-user-thinking",
		"chat":    "chat-thinking",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	typingStart := waitDeltaPayloadType(t, conn, "typing", 2*time.Second)
	if typingStart["typing"] != true {
		t.Fatalf("typing start payload has typing=%v, want true", typingStart["typing"])
	}

	thinkingPayload := waitDeltaPayloadType(t, conn, "message", 5*time.Second)
	if thinkingPayload["content"] != "thinking..." {
		t.Fatalf("thinking message content = %v, want thinking...", thinkingPayload["content"])
	}
	if thinkingPayload["to"] != "chat-thinking" {
		t.Fatalf("thinking message to = %v, want chat-thinking", thinkingPayload["to"])
	}
}

func TestDeltaChatChannelSkipsThinkingFallbackWhenReplyIsImmediate(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL}, mb)
	if err != nil {
		t.Fatalf("NewDeltaChatChannel failed: %v", err)
	}
	ch.thinkingDelay = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer ch.Stop(context.Background())

	conn := waitDeltaConn(t, connCh)
	defer conn.Close()

	incoming := map[string]interface{}{
		"type":    "message",
		"id":      "m-fast",
		"from":    "dc-user-fast",
		"chat":    "chat-fast",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	typingStart := waitDeltaPayloadType(t, conn, "typing", 2*time.Second)
	if typingStart["typing"] != true {
		t.Fatalf("typing start payload has typing=%v, want true", typingStart["typing"])
	}

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-fast", Content: "response"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	typingStop := waitDeltaPayloadType(t, conn, "typing", 2*time.Second)
	if typingStop["typing"] != false {
		t.Fatalf("typing stop payload has typing=%v, want false", typingStop["typing"])
	}

	messagePayload := waitDeltaPayloadType(t, conn, "message", 2*time.Second)
	if messagePayload["content"] != "response" {
		t.Fatalf("message content = %v, want response", messagePayload["content"])
	}

	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return
		}
		t.Fatalf("bridge read failed: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("failed to unmarshal extra bridge payload: %v", err)
	}

	if payload["type"] == "message" && payload["content"] == "thinking..." {
		t.Fatal("unexpected thinking fallback message for immediate response")
	}
}

func TestDeltaChatChannelSendReactionCommand(t *testing.T) {
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
		ChatID:  "chat-7",
		Content: "/react 42 👍",
	}
	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	payload := waitDeltaPayloadType(t, conn, "reaction", 2*time.Second)
	if payload["message_id"] != "42" {
		t.Fatalf("message_id = %v, want 42", payload["message_id"])
	}
	if payload["reaction"] != "👍" {
		t.Fatalf("reaction = %v, want 👍", payload["reaction"])
	}
}

func TestDeltaChatChannelIncomingReaction(t *testing.T) {
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
		"type":       "reaction",
		"from":       "dc-user-99",
		"from_name":  "Reaction User",
		"chat":       "chat-r",
		"message_id": "77",
		"reaction":   "🔥",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer consumeCancel()

	msg, ok := mb.ConsumeInbound(consumeCtx)
	if !ok {
		t.Fatal("expected inbound reaction from DeltaChat")
	}

	if msg.Channel != "deltachat" {
		t.Fatalf("channel = %q, want deltachat", msg.Channel)
	}
	if msg.SenderID != "dc-user-99" {
		t.Fatalf("sender = %q, want dc-user-99", msg.SenderID)
	}
	if msg.ChatID != "chat-r" {
		t.Fatalf("chat = %q, want chat-r", msg.ChatID)
	}
	if msg.Metadata["event"] != "reaction" {
		t.Fatalf("metadata.event = %q, want reaction", msg.Metadata["event"])
	}
	if msg.Metadata["reacted_message_id"] != "77" {
		t.Fatalf("metadata.reacted_message_id = %q, want 77", msg.Metadata["reacted_message_id"])
	}
	if msg.Metadata["reaction"] != "🔥" {
		t.Fatalf("metadata.reaction = %q, want 🔥", msg.Metadata["reaction"])
	}
}
