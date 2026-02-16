package channels

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

type mockChannel struct {
	mu         sync.Mutex
	startCount int
	stopCount  int
	running    bool
	sendCount  int
	sendErr    error
	startErr   error
	stopErr    error
	name       string
	lastSend   []struct {
		msg bus.OutboundMessage
	}
	sentSignal chan bus.OutboundMessage

	allowFrom map[string]bool
}

func newMockChannel(name string) *mockChannel {
	return &mockChannel{
		name:       name,
		sentSignal: make(chan bus.OutboundMessage, 4),
		allowFrom: map[string]bool{
			"alice": true,
		},
	}
}

func (m *mockChannel) Name() string {
	return m.name
}

func (m *mockChannel) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCount++
	m.running = true
	return m.startErr
}

func (m *mockChannel) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCount++
	m.running = false
	return m.stopErr
}

func (m *mockChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	m.mu.Lock()
	m.sendCount++
	m.lastSend = append(m.lastSend, struct {
		msg bus.OutboundMessage
	}{msg})
	m.mu.Unlock()

	select {
	case m.sentSignal <- msg:
	default:
	}

	return m.sendErr
}

func (m *mockChannel) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *mockChannel) IsAllowed(senderID string) bool {
	if len(m.allowFrom) == 0 {
		return true
	}
	return m.allowFrom[senderID]
}

func (m *mockChannel) waitForSend(t *testing.T, timeout time.Duration) bus.OutboundMessage {
	t.Helper()
	select {
	case msg := <-m.sentSignal:
		return msg
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for outbound send")
	}

	return bus.OutboundMessage{}
}

func (m *mockChannel) startStats() (startCount, stopCount, sendCount int, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startCount, m.stopCount, m.sendCount, m.running
}

func TestManager_InitializeWithoutEnabledChannels(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll returned error: %v", err)
	}

	if len(manager.GetEnabledChannels()) != 0 {
		t.Fatalf("expected no enabled channels, got %d", len(manager.GetEnabledChannels()))
	}
}

func TestManager_RegisterAndUnregisterChannel(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	ch := newMockChannel("telegram")
	manager.RegisterChannel("telegram", ch)

	got, ok := manager.GetChannel("telegram")
	if !ok {
		t.Fatal("expected channel to be registered")
	}
	if got != ch {
		t.Fatal("expected to retrieve same channel instance")
	}

	manager.UnregisterChannel("telegram")
	if _, ok := manager.GetChannel("telegram"); ok {
		t.Fatal("expected channel to be unregistered")
	}
}

func TestManager_SendToChannel(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	channel := newMockChannel("telegram")
	manager.RegisterChannel("telegram", channel)

	if err := manager.SendToChannel(context.Background(), "unknown", "chat", "hello"); err == nil {
		t.Fatal("expected error sending to unknown channel")
	}

	if err := manager.SendToChannel(context.Background(), "telegram", "chat", "hello"); err != nil {
		t.Fatalf("expected send to succeed: %v", err)
	}

	count := channel.sendCount
	if count != 1 {
		t.Fatalf("expected sendCount=1, got %d", count)
	}

	if channel.lastSend[0].msg.Channel != "telegram" || channel.lastSend[0].msg.ChatID != "chat" || channel.lastSend[0].msg.Content != "hello" {
		t.Fatalf("unexpected outbound payload: %#v", channel.lastSend[0].msg)
	}
}

func TestManager_StartAndStopAll(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	channel := newMockChannel("telegram")
	manager.RegisterChannel("telegram", channel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}

	startCount, stopCount, _, running := channel.startStats()
	if startCount != 1 {
		t.Fatalf("expected startCount=1, got %d", startCount)
	}
	if stopCount != 0 {
		t.Fatalf("expected stopCount=0 before StopAll, got %d", stopCount)
	}
	if !running {
		t.Fatalf("expected channel running before StopAll")
	}

	manager.bus.PublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "chat-1", Content: "hello"})
	msg := channel.waitForSend(t, 2*time.Second)
	if msg.ChatID != "chat-1" || msg.Content != "hello" {
		t.Fatalf("unexpected dispatched message: %#v", msg)
	}

	if err := manager.StopAll(ctx); err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}

	_, stopCount, _, running = channel.startStats()
	if stopCount != 1 {
		t.Fatalf("expected stopCount=1 after StopAll, got %d", stopCount)
	}
	if running {
		t.Fatalf("expected channel not running after StopAll")
	}
}

func TestManager_GetStatus(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	chA := newMockChannel("a")
	chB := newMockChannel("b")

	manager.RegisterChannel("a", chA)
	manager.RegisterChannel("b", chB)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}

	status := manager.GetStatus()
	if len(status) != 2 {
		t.Fatalf("expected 2 status entries, got %d", len(status))
	}

	for _, name := range []string{"a", "b"} {
		channelStatusRaw, ok := status[name]
		if !ok {
			t.Fatalf("expected status entry for channel %q", name)
		}

		channelStatus, ok := channelStatusRaw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map[string]interface{} for channel %q", name)
		}

		if channelStatus["running"] != true {
			t.Fatalf("expected channel %q running=true, got %#v", name, channelStatus["running"])
		}
		if channelStatus["enabled"] != true {
			t.Fatalf("expected channel %q enabled=true, got %#v", name, channelStatus["enabled"])
		}
	}

	if err := manager.StopAll(ctx); err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}
}

func TestManager_GetEnabledChannels(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	for _, name := range []string{"a", "b", "c"} {
		manager.RegisterChannel(name, newMockChannel(name))
	}

	got := manager.GetEnabledChannels()

	if len(got) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(got))
	}

	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
	}

	for _, expected := range []string{"a", "b", "c"} {
		if !seen[expected] {
			// include a list to help debugging if map set mismatched
			t.Fatalf("expected enabled channel %q, got %v", expected, got)
		}
	}

	manager.UnregisterChannel("b")

	if got = manager.GetEnabledChannels(); len(got) != 2 {
		t.Fatalf("expected 2 channels after unregister, got %d", len(got))
	}

	if _, ok := manager.GetChannel("b"); ok {
		t.Fatalf("expected channel b to be unregistered")
	}
}

func TestManager_StartAll_IsIdempotent(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	channel := newMockChannel("telegram")
	manager.RegisterChannel("telegram", channel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("first StartAll failed: %v", err)
	}
	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("second StartAll failed: %v", err)
	}

	startCount, _, _, _ := channel.startStats()
	if startCount != 1 {
		t.Fatalf("expected StartAll to be idempotent (startCount=1), got %d", startCount)
	}

	if err := manager.StopAll(ctx); err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}
}

func TestManager_StopAllAfterDoubleStart_StopsAllDispatchers(t *testing.T) {
	manager := &Manager{
		channels: make(map[string]Channel),
		bus:      bus.NewMessageBus(),
	}

	channel := newMockChannel("telegram")
	manager.RegisterChannel("telegram", channel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("first StartAll failed: %v", err)
	}
	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("second StartAll failed: %v", err)
	}

	if err := manager.StopAll(ctx); err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}

	manager.bus.PublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "chat-1", Content: "should not dispatch"})

	select {
	case msg := <-channel.sentSignal:
		t.Fatalf("expected no dispatchers after StopAll, but message was sent: %#v", msg)
	case <-time.After(300 * time.Millisecond):
		// expected
	}
}
