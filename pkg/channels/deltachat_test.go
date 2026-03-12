package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	payload, err := readDeltaPayloadResult(conn, timeout)
	if err != nil {
		t.Fatalf("bridge read failed: %v", err)
	}
	return payload
}

func readDeltaPayloadResult(conn *websocket.Conn, timeout time.Duration) (map[string]interface{}, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeDeltaAckForPayload(conn *websocket.Conn, payload map[string]interface{}, ok bool, errorText string) error {
	requestID, _ := payload["request_id"].(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("missing request_id in outbound payload")
	}

	requireAck, _ := payload["require_ack"].(bool)
	if !requireAck {
		return fmt.Errorf("outbound payload %q missing require_ack=true", payload["type"])
	}

	ackPayload := map[string]interface{}{
		"type":       "ack",
		"request_id": requestID,
		"ok":         ok,
	}
	if errorText != "" {
		ackPayload["error"] = errorText
	}

	return conn.WriteJSON(ackPayload)
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
	payloadCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
			bridgeErrCh <- ackErr
			return
		}
		payloadCh <- payload
		bridgeErrCh <- nil
	}()

	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}
	got := <-payloadCh

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

func TestDeltaChatChannelSend_EscapesStandaloneTildes(t *testing.T) {
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
		ChatID:  "chat-tilde",
		Content: "PORTFOLIO: opened ~1h ago\nP/L (~$276)\nUse ~~bold strike~~ if needed",
	}
	payloadCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
			bridgeErrCh <- ackErr
			return
		}
		payloadCh <- payload
		bridgeErrCh <- nil
	}()

	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}
	got := <-payloadCh

	want := "PORTFOLIO: opened \\~1h ago\nP/L (\\~$276)\nUse ~~bold strike~~ if needed"
	if got["content"] != want {
		t.Fatalf("content = %v, want %q", got["content"], want)
	}
}

func TestSanitizeDeltaOutboundContent(t *testing.T) {
	in := "~1h (~$276) ~~strike~~ \\~already"
	got := sanitizeDeltaOutboundContent(in)
	want := "\\~1h (\\~$276) ~~strike~~ \\~already"
	if got != want {
		t.Fatalf("sanitizeDeltaOutboundContent() = %q, want %q", got, want)
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

func TestDeltaChatChannelIncomingMessageAddsTimingMetadata(t *testing.T) {
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

	now := time.Now()
	sentMillis := now.Add(-4 * time.Second).UnixMilli()
	rcvdMillis := now.Add(-2900 * time.Millisecond).UnixMilli()
	bridgeMillis := now.Add(-2700 * time.Millisecond).UnixMilli()

	incoming := map[string]interface{}{
		"type":                 "message",
		"id":                   "m-timing",
		"from":                 "dc-user-99",
		"chat":                 "chat-99",
		"content":              "timed message",
		"bridge_received_ms":   float64(bridgeMillis),
		"dc_timestamp_sent_ms": float64(sentMillis),
		"dc_timestamp_rcvd_ms": float64(rcvdMillis),
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

	if got := msg.Metadata["bridge_received_ms"]; got != strconv.FormatInt(bridgeMillis, 10) {
		t.Fatalf("metadata.bridge_received_ms = %q, want %q", got, strconv.FormatInt(bridgeMillis, 10))
	}
	if got := msg.Metadata["dc_timestamp_sent_ms"]; got != strconv.FormatInt(sentMillis, 10) {
		t.Fatalf("metadata.dc_timestamp_sent_ms = %q, want %q", got, strconv.FormatInt(sentMillis, 10))
	}
	if got := msg.Metadata["dc_timestamp_rcvd_ms"]; got != strconv.FormatInt(rcvdMillis, 10) {
		t.Fatalf("metadata.dc_timestamp_rcvd_ms = %q, want %q", got, strconv.FormatInt(rcvdMillis, 10))
	}

	if got := msg.Metadata["dc_transport_ms"]; got != "1100" {
		t.Fatalf("metadata.dc_transport_ms = %q, want 1100", got)
	}
	if got := msg.Metadata["dc_sent_to_bridge_ms"]; got != "1300" {
		t.Fatalf("metadata.dc_sent_to_bridge_ms = %q, want 1300", got)
	}

	bridgeToGateway, err := strconv.ParseInt(msg.Metadata["bridge_to_gateway_ms"], 10, 64)
	if err != nil {
		t.Fatalf("metadata.bridge_to_gateway_ms parse error: %v (value=%q)", err, msg.Metadata["bridge_to_gateway_ms"])
	}
	if bridgeToGateway <= 0 {
		t.Fatalf("metadata.bridge_to_gateway_ms = %d, want > 0", bridgeToGateway)
	}
}

func TestDeltaChatChannelIgnoresIncomingReactionsByDefault(t *testing.T) {
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
		"id":         "123",
		"message_id": "123",
		"from":       "dc-user-42",
		"from_name":  "Alice",
		"chat":       "chat-42",
		"reaction":   "🤗",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer consumeCancel()

	if _, ok := mb.ConsumeInbound(consumeCtx); ok {
		t.Fatal("expected reaction event to be ignored")
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

func TestDeltaLookupMediaFallbackResolvesBlobFromDB(t *testing.T) {
	baseDir := t.TempDir()
	accountsDir := filepath.Join(baseDir, "accounts")
	accountDir := filepath.Join(accountsDir, "1")
	blobsDir := filepath.Join(accountDir, "dc.db-blobs")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		t.Fatalf("mkdir blobs dir: %v", err)
	}

	dbPath := filepath.Join(accountDir, "dc.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE msgs (id INTEGER PRIMARY KEY, chat_id INTEGER, param TEXT)`); err != nil {
		t.Fatalf("create msgs table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO msgs (id, chat_id, param) VALUES (123, 77, ?)`, "f=$BLOBDIR/image.png"); err != nil {
		t.Fatalf("insert row: %v", err)
	}

	blobPath := filepath.Join(blobsDir, "image.png")
	if err := os.WriteFile(blobPath, []byte("img"), 0o644); err != nil {
		t.Fatalf("write blob file: %v", err)
	}

	t.Setenv("DELTACHAT_ACCOUNTS_DIR", accountsDir)
	t.Setenv("PICOCLAW_HOME", "")
	t.Setenv("HOME", filepath.Join(baseDir, "home"))

	fallback := deltaLookupMediaFallback("123", "77")
	if len(fallback.Paths) != 1 || fallback.Paths[0] != blobPath {
		t.Fatalf("fallback paths = %v, want [%s]", fallback.Paths, blobPath)
	}

	wrongChat := deltaLookupMediaFallback("123", "78")
	if len(wrongChat.Paths) != 0 {
		t.Fatalf("fallback for wrong chat = %v, want []", wrongChat.Paths)
	}
}

func TestDeltaLookupMediaFallbackWaitsForDBAndBlob(t *testing.T) {
	baseDir := t.TempDir()
	accountsDir := filepath.Join(baseDir, "accounts")
	accountDir := filepath.Join(accountsDir, "1")
	blobsDir := filepath.Join(accountDir, "dc.db-blobs")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		t.Fatalf("mkdir blobs dir: %v", err)
	}

	dbPath := filepath.Join(accountDir, "dc.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE msgs (id INTEGER PRIMARY KEY, chat_id INTEGER, param TEXT)`); err != nil {
		t.Fatalf("create msgs table: %v", err)
	}

	blobPath := filepath.Join(blobsDir, "image.png")
	go func() {
		time.Sleep(120 * time.Millisecond)
		_, _ = db.Exec(`INSERT INTO msgs (id, chat_id, param) VALUES (123, 77, ?)`, "f=$BLOBDIR/image.png")
		time.Sleep(120 * time.Millisecond)
		_ = os.WriteFile(blobPath, []byte("img"), 0o644)
	}()

	t.Setenv("DELTACHAT_ACCOUNTS_DIR", accountsDir)
	t.Setenv("PICOCLAW_HOME", "")
	t.Setenv("HOME", filepath.Join(baseDir, "home"))

	fallback := deltaLookupMediaFallback("123", "77")
	if len(fallback.Paths) != 1 || fallback.Paths[0] != blobPath {
		t.Fatalf("fallback paths = %v, want [%s]", fallback.Paths, blobPath)
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
	payloadCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(secondConn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		if ackErr := writeDeltaAckForPayload(secondConn, payload, true, ""); ackErr != nil {
			bridgeErrCh <- ackErr
			return
		}
		payloadCh <- payload
		bridgeErrCh <- nil
	}()

	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed after reconnect: %v", err)
	}

	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed after reconnect: %v", bridgeErr)
	}
	got := <-payloadCh

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
	typingStopCh := make(chan map[string]interface{}, 1)
	messageCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		var typingStop map[string]interface{}
		var messagePayload map[string]interface{}

		for time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			payload, readErr := readDeltaPayloadResult(conn, remaining)
			if readErr != nil {
				bridgeErrCh <- readErr
				return
			}

			typeName, _ := payload["type"].(string)
			switch typeName {
			case "typing":
				typingValue, _ := payload["typing"].(bool)
				if !typingValue && typingStop == nil {
					typingStop = payload
				}
			case "message":
				if messagePayload == nil {
					if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
						bridgeErrCh <- ackErr
						return
					}
					messagePayload = payload
				}
			}

			if typingStop != nil && messagePayload != nil {
				typingStopCh <- typingStop
				messageCh <- messagePayload
				bridgeErrCh <- nil
				return
			}
		}

		bridgeErrCh <- fmt.Errorf("timed out waiting for typing stop + outbound message")
	}()

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-typing", Content: "response"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}

	typingStop := <-typingStopCh
	if typingStop["typing"] != false {
		t.Fatalf("typing stop payload has typing=%v, want false", typingStop["typing"])
	}

	messagePayload := <-messageCh
	if messagePayload["content"] != "response" {
		t.Fatalf("message content = %v, want response", messagePayload["content"])
	}
}

func TestDeltaChatChannelSendsThinkingMarkerImmediately(t *testing.T) {
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

	thinkingPayload := waitDeltaPayloadType(t, conn, "thinking_start", 2*time.Second)
	if thinkingPayload["content"] != "thinking..." {
		t.Fatalf("thinking start content = %v, want thinking...", thinkingPayload["content"])
	}
	if thinkingPayload["to"] != "chat-thinking" {
		t.Fatalf("thinking start to = %v, want chat-thinking", thinkingPayload["to"])
	}
}

func TestDeltaChatChannelClearsThinkingMarkerBeforeReply(t *testing.T) {
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

	thinkingStart := waitDeltaPayloadType(t, conn, "thinking_start", 2*time.Second)
	if thinkingStart["to"] != "chat-fast" {
		t.Fatalf("thinking start to = %v, want chat-fast", thinkingStart["to"])
	}

	typingStopCh := make(chan map[string]interface{}, 1)
	thinkingClearCh := make(chan map[string]interface{}, 1)
	messageCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		var typingStop map[string]interface{}
		var thinkingClear map[string]interface{}
		var messagePayload map[string]interface{}

		for time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			payload, readErr := readDeltaPayloadResult(conn, remaining)
			if readErr != nil {
				bridgeErrCh <- readErr
				return
			}

			typeName, _ := payload["type"].(string)
			switch typeName {
			case "typing":
				typingValue, _ := payload["typing"].(bool)
				if !typingValue && typingStop == nil {
					typingStop = payload
				}
			case "thinking_clear":
				if thinkingClear == nil {
					thinkingClear = payload
				}
			case "message":
				if messagePayload == nil {
					if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
						bridgeErrCh <- ackErr
						return
					}
					messagePayload = payload
				}
			}

			if typingStop != nil && thinkingClear != nil && messagePayload != nil {
				typingStopCh <- typingStop
				thinkingClearCh <- thinkingClear
				messageCh <- messagePayload
				bridgeErrCh <- nil
				return
			}
		}

		bridgeErrCh <- fmt.Errorf("timed out waiting for typing stop + thinking clear + outbound message")
	}()

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-fast", Content: "response"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}

	typingStop := <-typingStopCh
	if typingStop["typing"] != false {
		t.Fatalf("typing stop payload has typing=%v, want false", typingStop["typing"])
	}

	thinkingClear := <-thinkingClearCh
	if thinkingClear["to"] != "chat-fast" {
		t.Fatalf("thinking clear to = %v, want chat-fast", thinkingClear["to"])
	}

	messagePayload := <-messageCh
	if messagePayload["content"] != "response" {
		t.Fatalf("message content = %v, want response", messagePayload["content"])
	}
}

func TestDeltaChatChannelSendsAckReactionOnIncomingMessage(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL, AckReaction: "\U0001F440"}, mb)
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
		"id":      "42",
		"from":    "dc-user-ack",
		"chat":    "chat-ack",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	reactionPayload := waitDeltaPayloadType(t, conn, "reaction", 2*time.Second)
	if reactionPayload["to"] != "chat-ack" {
		t.Fatalf("reaction to = %v, want chat-ack", reactionPayload["to"])
	}
	if reactionPayload["message_id"] != "42" {
		t.Fatalf("reaction message_id = %v, want 42", reactionPayload["message_id"])
	}
	if reactionPayload["reaction"] != "\U0001F440" {
		t.Fatalf("reaction = %v, want 👀", reactionPayload["reaction"])
	}
}

func TestDeltaChatChannelSendsDoneReactionAfterReply(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL, DoneReaction: "\u2705"}, mb)
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
		"id":      "77",
		"from":    "dc-user-done",
		"chat":    "chat-done",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	doneReactionCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			payload, readErr := readDeltaPayloadResult(conn, remaining)
			if readErr != nil {
				bridgeErrCh <- readErr
				return
			}

			typeName, _ := payload["type"].(string)
			switch typeName {
			case "message":
				if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
					bridgeErrCh <- ackErr
					return
				}
			case "reaction":
				if payload["reaction"] == "\u2705" {
					doneReactionCh <- payload
					bridgeErrCh <- nil
					return
				}
			}
		}

		bridgeErrCh <- fmt.Errorf("timed out waiting for done reaction")
	}()

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-done", Content: "response"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge handling failed: %v", bridgeErr)
	}

	donePayload := <-doneReactionCh
	if donePayload["to"] != "chat-done" {
		t.Fatalf("done reaction to = %v, want chat-done", donePayload["to"])
	}
	if donePayload["message_id"] != "77" {
		t.Fatalf("done reaction message_id = %v, want 77", donePayload["message_id"])
	}
}

func TestDeltaChatChannelAgentProgressDoesNotTriggerDoneReactionOrStopTyping(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL, DoneReaction: "\u2705"}, mb)
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
		"id":      "77",
		"from":    "dc-user-progress",
		"chat":    "chat-progress",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	// Ensure typing + thinking are active before we send any outbound messages.
	_ = waitDeltaPayloadType(t, conn, "typing", 2*time.Second)
	_ = waitDeltaPayloadType(t, conn, "thinking_start", 2*time.Second)

	progressContent := "Agent progress (v1, run=R1): running exec\nCalls:\n1. run exec - run command\n"

	progressErrCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			payload, readErr := readDeltaPayloadResult(conn, remaining)
			if readErr != nil {
				progressErrCh <- readErr
				return
			}

			typeName, _ := payload["type"].(string)
			switch typeName {
			case "typing":
				typingValue, _ := payload["typing"].(bool)
				if !typingValue {
					progressErrCh <- fmt.Errorf("unexpected typing stop during agent progress: %+v", payload)
					return
				}
			case "thinking_clear":
				progressErrCh <- fmt.Errorf("unexpected thinking_clear during agent progress: %+v", payload)
				return
			case "reaction":
				if payload["reaction"] == "\u2705" {
					progressErrCh <- fmt.Errorf("unexpected done reaction during agent progress: %+v", payload)
					return
				}
			case "message":
				content, _ := payload["content"].(string)
				if strings.HasPrefix(content, "Agent progress (v1") {
					if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
						progressErrCh <- ackErr
						return
					}
					progressErrCh <- nil
					return
				}
				// Best-effort: ack any other message payload.
				_ = writeDeltaAckForPayload(conn, payload, true, "")
			}
		}
		progressErrCh <- fmt.Errorf("timed out waiting for agent progress message")
	}()

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-progress", Content: progressContent}); err != nil {
		t.Fatalf("Send progress failed: %v", err)
	}
	if progressErr := <-progressErrCh; progressErr != nil {
		t.Fatalf("bridge handling failed during progress send: %v", progressErr)
	}

	// Now send a normal reply and ensure done reaction arrives.
	doneReactionCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			payload, readErr := readDeltaPayloadResult(conn, remaining)
			if readErr != nil {
				bridgeErrCh <- readErr
				return
			}

			typeName, _ := payload["type"].(string)
			switch typeName {
			case "message":
				if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
					bridgeErrCh <- ackErr
					return
				}
			case "reaction":
				if payload["reaction"] == "\u2705" {
					doneReactionCh <- payload
					bridgeErrCh <- nil
					return
				}
			}
		}
		bridgeErrCh <- fmt.Errorf("timed out waiting for done reaction")
	}()

	if err := ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-progress", Content: "response"}); err != nil {
		t.Fatalf("Send reply failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge handling failed during reply send: %v", bridgeErr)
	}

	donePayload := <-doneReactionCh
	if donePayload["to"] != "chat-progress" {
		t.Fatalf("done reaction to = %v, want chat-progress", donePayload["to"])
	}
	if donePayload["message_id"] != "77" {
		t.Fatalf("done reaction message_id = %v, want 77", donePayload["message_id"])
	}
}

func TestDeltaChatChannelSendsErrorReactionWhenReplyFails(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL, ErrorReaction: "\u26a0\ufe0f"}, mb)
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
		"id":      "88",
		"from":    "dc-user-error",
		"chat":    "chat-error",
		"content": "hello",
	}
	if err := conn.WriteJSON(incoming); err != nil {
		t.Fatalf("bridge write failed: %v", err)
	}

	errorReactionCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			remaining := time.Until(deadline)
			payload, readErr := readDeltaPayloadResult(conn, remaining)
			if readErr != nil {
				bridgeErrCh <- readErr
				return
			}

			typeName, _ := payload["type"].(string)
			switch typeName {
			case "message":
				if ackErr := writeDeltaAckForPayload(conn, payload, false, "nope"); ackErr != nil {
					bridgeErrCh <- ackErr
					return
				}
			case "reaction":
				if payload["reaction"] == "\u26a0\ufe0f" {
					errorReactionCh <- payload
					bridgeErrCh <- nil
					return
				}
			}
		}

		bridgeErrCh <- fmt.Errorf("timed out waiting for error reaction")
	}()

	err = ch.Send(ctx, bus.OutboundMessage{Channel: "deltachat", ChatID: "chat-error", Content: "response"})
	if err == nil {
		t.Fatal("expected Send to fail when bridge rejects the reply")
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge handling failed: %v", bridgeErr)
	}

	errorPayload := <-errorReactionCh
	if errorPayload["to"] != "chat-error" {
		t.Fatalf("error reaction to = %v, want chat-error", errorPayload["to"])
	}
	if errorPayload["message_id"] != "88" {
		t.Fatalf("error reaction message_id = %v, want 88", errorPayload["message_id"])
	}
}

func TestDeltaChatChannelSendProfilePictureCommandWithPath(t *testing.T) {
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
		ChatID:  "chat-profile",
		Content: "/set_profile_picture /root/.picoclaw/workspace/avatar.png",
	}

	payloadCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
			bridgeErrCh <- ackErr
			return
		}
		payloadCh <- payload
		bridgeErrCh <- nil
	}()

	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}

	payload := <-payloadCh
	if payload["type"] != "profile_image" {
		t.Fatalf("type = %v, want profile_image", payload["type"])
	}
	if payload["path"] != "/root/.picoclaw/workspace/avatar.png" {
		t.Fatalf("path = %v, want /root/.picoclaw/workspace/avatar.png", payload["path"])
	}
}

func TestDeltaChatChannelSendProfilePictureCommandUsesMediaFallback(t *testing.T) {
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
		ChatID:  "chat-profile",
		Content: "/set_profile_picture",
		Media:   []string{"/root/.picoclaw/workspace/avatar-from-media.png"},
	}

	payloadCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
			bridgeErrCh <- ackErr
			return
		}
		payloadCh <- payload
		bridgeErrCh <- nil
	}()

	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}

	payload := <-payloadCh
	if payload["type"] != "profile_image" {
		t.Fatalf("type = %v, want profile_image", payload["type"])
	}
	if payload["path"] != "/root/.picoclaw/workspace/avatar-from-media.png" {
		t.Fatalf("path = %v, want /root/.picoclaw/workspace/avatar-from-media.png", payload["path"])
	}
}

func TestDeltaChatChannelSendProfilePictureCommandRequiresPathOrMedia(t *testing.T) {
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

	err = ch.Send(ctx, bus.OutboundMessage{
		Channel: "deltachat",
		ChatID:  "chat-profile",
		Content: "/set_profile_picture",
	})
	if err == nil {
		t.Fatal("expected Send to fail when profile picture path is missing")
	}

	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, readErr := conn.ReadMessage()
	if readErr != nil {
		if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
			return
		}
		t.Fatalf("unexpected websocket read error: %v", readErr)
	}

	t.Fatal("expected no payload to be sent for invalid profile picture command")
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
	payloadCh := make(chan map[string]interface{}, 1)
	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		if ackErr := writeDeltaAckForPayload(conn, payload, true, ""); ackErr != nil {
			bridgeErrCh <- ackErr
			return
		}
		payloadCh <- payload
		bridgeErrCh <- nil
	}()

	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack failed: %v", bridgeErr)
	}

	payload := <-payloadCh
	if payload["message_id"] != "42" {
		t.Fatalf("message_id = %v, want 42", payload["message_id"])
	}
	if payload["reaction"] != "👍" {
		t.Fatalf("reaction = %v, want 👍", payload["reaction"])
	}
}

func TestDeltaChatChannelSendReturnsErrorWhenBridgeRejectsAttachment(t *testing.T) {
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

	bridgeErrCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeErrCh <- readErr
			return
		}
		bridgeErrCh <- writeDeltaAckForPayload(conn, payload, false, "failed to attach file")
	}()

	err = ch.Send(ctx, bus.OutboundMessage{
		Channel: "deltachat",
		ChatID:  "chat-ack-error",
		Content: "with file",
		Media:   []string{"/tmp/not-found.png"},
	})

	if bridgeErr := <-bridgeErrCh; bridgeErr != nil {
		t.Fatalf("bridge ack write failed: %v", bridgeErr)
	}

	if err == nil {
		t.Fatal("expected Send to fail when bridge rejects attachment")
	}
	if !strings.Contains(err.Error(), "failed to attach file") {
		t.Fatalf("error = %v, want bridge rejection reason", err)
	}
}

func TestDeltaChatChannelSendReturnsErrorWhenAckMissing(t *testing.T) {
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

	sendCtx, sendCancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer sendCancel()

	bridgeReadCh := make(chan error, 1)
	go func() {
		payload, readErr := readDeltaPayloadResult(conn, 2*time.Second)
		if readErr != nil {
			bridgeReadCh <- readErr
			return
		}
		if _, ok := payload["request_id"].(string); !ok {
			bridgeReadCh <- fmt.Errorf("missing request_id in outbound payload")
			return
		}
		bridgeReadCh <- nil
	}()

	err = ch.Send(sendCtx, bus.OutboundMessage{
		Channel: "deltachat",
		ChatID:  "chat-ack-timeout",
		Content: "with file",
		Media:   []string{"/tmp/a.png"},
	})
	if bridgeReadErr := <-bridgeReadCh; bridgeReadErr != nil {
		t.Fatalf("bridge read failed: %v", bridgeReadErr)
	}
	if err == nil {
		t.Fatal("expected Send to fail when bridge ack is missing")
	}
}

func TestDeltaChatChannelIncomingReaction(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	wsURL, connCh, cleanup := startDeltaBridge(t)
	defer cleanup()

	ch, err := NewDeltaChatChannel(config.DeltaChatConfig{Enabled: true, BridgeURL: wsURL, ForwardReactions: true}, mb)
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
	if msg.Content != "[reaction to 77] 🔥" {
		t.Fatalf("content = %q, want [reaction to 77] 🔥", msg.Content)
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
