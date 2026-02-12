package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/sipeed/picoclaw/pkg/bus"
)

// mockTelegramBot implements telegramBot for testing.
type mockTelegramBot struct {
	mu sync.Mutex

	sendMessageCalls    []*telego.SendMessageParams
	sendChatActionCalls []*telego.SendChatActionParams
	editMessageCalls    []*telego.EditMessageTextParams
	deleteMessageCalls  []*telego.DeleteMessageParams
	sendPhotoCalls      []*telego.SendPhotoParams
	sendDocumentCalls   []*telego.SendDocumentParams

	// configurable return for SendMessage
	sendMessageID int
}

func newMockBot() *mockTelegramBot {
	return &mockTelegramBot{sendMessageID: 42}
}

func (m *mockTelegramBot) Username() string { return "testbot" }
func (m *mockTelegramBot) FileDownloadURL(filepath string) string {
	return "https://example.com/" + filepath
}
func (m *mockTelegramBot) UpdatesViaLongPolling(ctx context.Context, params *telego.GetUpdatesParams, options ...telego.LongPollingOption) (<-chan telego.Update, error) {
	return nil, nil
}
func (m *mockTelegramBot) SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendMessageCalls = append(m.sendMessageCalls, params)
	return &telego.Message{MessageID: m.sendMessageID}, nil
}
func (m *mockTelegramBot) SendChatAction(ctx context.Context, params *telego.SendChatActionParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendChatActionCalls = append(m.sendChatActionCalls, params)
	return nil
}
func (m *mockTelegramBot) SendPhoto(ctx context.Context, params *telego.SendPhotoParams) (*telego.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendPhotoCalls = append(m.sendPhotoCalls, params)
	return &telego.Message{MessageID: m.sendMessageID}, nil
}
func (m *mockTelegramBot) SendDocument(ctx context.Context, params *telego.SendDocumentParams) (*telego.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendDocumentCalls = append(m.sendDocumentCalls, params)
	return &telego.Message{MessageID: m.sendMessageID}, nil
}
func (m *mockTelegramBot) EditMessageText(ctx context.Context, params *telego.EditMessageTextParams) (*telego.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.editMessageCalls = append(m.editMessageCalls, params)
	return &telego.Message{MessageID: params.MessageID}, nil
}
func (m *mockTelegramBot) DeleteMessage(ctx context.Context, params *telego.DeleteMessageParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteMessageCalls = append(m.deleteMessageCalls, params)
	return nil
}
func (m *mockTelegramBot) GetFile(ctx context.Context, params *telego.GetFileParams) (*telego.File, error) {
	return &telego.File{FileID: params.FileID, FilePath: "photos/test.jpg"}, nil
}

func (m *mockTelegramBot) getSendMessageCalls() []*telego.SendMessageParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*telego.SendMessageParams, len(m.sendMessageCalls))
	copy(cp, m.sendMessageCalls)
	return cp
}

func (m *mockTelegramBot) getSendChatActionCalls() []*telego.SendChatActionParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*telego.SendChatActionParams, len(m.sendChatActionCalls))
	copy(cp, m.sendChatActionCalls)
	return cp
}

func (m *mockTelegramBot) getEditMessageCalls() []*telego.EditMessageTextParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*telego.EditMessageTextParams, len(m.editMessageCalls))
	copy(cp, m.editMessageCalls)
	return cp
}

func (m *mockTelegramBot) getDeleteMessageCalls() []*telego.DeleteMessageParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*telego.DeleteMessageParams, len(m.deleteMessageCalls))
	copy(cp, m.deleteMessageCalls)
	return cp
}

func newTestTelegramChannel(bot telegramBot) *TelegramChannel {
	msgBus := bus.NewMessageBus()
	base := NewBaseChannel("telegram", nil, msgBus, nil)
	base.running.Store(true)
	ch := &TelegramChannel{
		BaseChannel:    base,
		bot:            bot,
		chatIDs:        make(map[string]int64),
		stopThinking:   sync.Map{},
		typingInterval: 100 * time.Millisecond, // fast ticks for tests
	}
	return ch
}

// --- Pure function tests (no mock needed) ---

func TestIsImageFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Images
		{"/tmp/photo.jpg", true},
		{"/tmp/photo.jpeg", true},
		{"/tmp/photo.JPG", true},
		{"/tmp/image.png", true},
		{"/tmp/image.PNG", true},
		{"/tmp/animation.gif", true},
		{"/tmp/sticker.webp", true},
		{"photo.JPEG", true},

		// Non-images
		{"/tmp/report.pdf", false},
		{"/tmp/data.txt", false},
		{"/tmp/archive.zip", false},
		{"/tmp/video.mp4", false},
		{"/tmp/audio.mp3", false},
		{"/tmp/binary.exe", false},
		{"", false},
		{"noextension", false},
		{"/tmp/.jpg", true}, // hidden file with image extension — still an image
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isImageFile(tt.path)
			if got != tt.want {
				t.Errorf("isImageFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractCodeBlocks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCodes int
		// Each placeholder should have a unique, sequential index
		wantDistinctPlaceholders int
	}{
		{
			name:                     "no code blocks",
			input:                    "hello world",
			wantCodes:                0,
			wantDistinctPlaceholders: 0,
		},
		{
			name:                     "single code block",
			input:                    "before\n```go\nfmt.Println(\"hi\")\n```\nafter",
			wantCodes:                1,
			wantDistinctPlaceholders: 1,
		},
		{
			name:                     "two code blocks",
			input:                    "```\nfirst\n```\nmiddle\n```\nsecond\n```",
			wantCodes:                2,
			wantDistinctPlaceholders: 2,
		},
		{
			name:                     "three code blocks",
			input:                    "```\nA\n```\n```\nB\n```\n```\nC\n```",
			wantCodes:                3,
			wantDistinctPlaceholders: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractCodeBlocks(tt.input)
			if len(result.codes) != tt.wantCodes {
				t.Errorf("got %d codes, want %d", len(result.codes), tt.wantCodes)
			}

			// Check that each placeholder has a unique index
			seen := make(map[string]bool)
			for i := 0; i < len(result.codes); i++ {
				placeholder := fmt.Sprintf("\x00CB%d\x00", i)
				if !strings.Contains(result.text, placeholder) {
					t.Errorf("missing placeholder %q in result text %q", placeholder, result.text)
				}
				seen[placeholder] = true
			}
			if len(seen) != tt.wantDistinctPlaceholders {
				t.Errorf("got %d distinct placeholders, want %d", len(seen), tt.wantDistinctPlaceholders)
			}
		})
	}
}

func TestMarkdownToTelegramHTML(t *testing.T) {
	// Verify existing functionality still works
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "plain text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "bold text",
			input: "**bold**",
			want:  "<b>bold</b>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdownToTelegramHTML(tt.input)
			if got != tt.want {
				t.Errorf("markdownToTelegramHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Send() tests ---

func TestSend_TextMessage(t *testing.T) {
	mock := newMockBot()
	ch := newTestTelegramChannel(mock)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Hello world",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	calls := mock.getSendMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(calls))
	}
	if calls[0].ParseMode != telego.ModeHTML {
		t.Errorf("expected HTML parse mode, got %q", calls[0].ParseMode)
	}
}

func TestSend_NeverEditsOrDeletesMessages(t *testing.T) {
	mock := newMockBot()
	ch := newTestTelegramChannel(mock)

	// Send should always create a new message — never edit or delete.
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Response text",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	edits := mock.getEditMessageCalls()
	if len(edits) != 0 {
		t.Errorf("expected 0 EditMessageText calls, got %d", len(edits))
	}

	deletes := mock.getDeleteMessageCalls()
	if len(deletes) != 0 {
		t.Errorf("expected 0 DeleteMessage calls, got %d", len(deletes))
	}

	sends := mock.getSendMessageCalls()
	if len(sends) != 1 {
		t.Errorf("expected 1 SendMessage call, got %d", len(sends))
	}
}

func TestSend_StopsTypingIndicator(t *testing.T) {
	mock := newMockBot()
	ch := newTestTelegramChannel(mock)

	// Simulate a running typing indicator
	var cancelled atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	ch.stopThinking.Store("12345", &thinkingCancel{fn: func() {
		cancelled.Store(true)
		cancel()
	}})

	err := ch.Send(ctx, bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Done thinking",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if !cancelled.Load() {
		t.Error("expected typing indicator to be cancelled when Send is called")
	}

	// The stopThinking entry should be cleaned up
	if _, ok := ch.stopThinking.Load("12345"); ok {
		t.Error("expected stopThinking entry to be deleted after Send")
	}
}

// --- Typing indicator tests ---

func TestStartTypingIndicator_SendsChatAction(t *testing.T) {
	mock := newMockBot()
	ch := newTestTelegramChannel(mock)

	ctx, cancel := context.WithCancel(context.Background())
	chatIDStr := "12345"

	ch.startTypingIndicator(ctx, cancel, 12345, chatIDStr)

	// Wait for at least one tick
	time.Sleep(150 * time.Millisecond)

	actions := mock.getSendChatActionCalls()
	if len(actions) == 0 {
		t.Error("expected at least one SendChatAction call")
	}
	for _, a := range actions {
		if a.Action != telego.ChatActionTyping {
			t.Errorf("expected action %q, got %q", telego.ChatActionTyping, a.Action)
		}
	}

	// Should NOT send any "Thinking..." message
	msgs := mock.getSendMessageCalls()
	if len(msgs) != 0 {
		t.Errorf("expected 0 SendMessage calls (no thinking message), got %d", len(msgs))
	}

	// Should NOT edit any message
	edits := mock.getEditMessageCalls()
	if len(edits) != 0 {
		t.Errorf("expected 0 EditMessageText calls, got %d", len(edits))
	}

	// Cleanup
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestStartTypingIndicator_StopsOnCancel(t *testing.T) {
	mock := newMockBot()
	ch := newTestTelegramChannel(mock)

	ctx, cancel := context.WithCancel(context.Background())

	ch.startTypingIndicator(ctx, cancel, 12345, "12345")

	// Let a couple ticks fire
	time.Sleep(250 * time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond)

	// Record count after cancel
	countAfterCancel := len(mock.getSendChatActionCalls())

	// Wait more — count should not increase
	time.Sleep(200 * time.Millisecond)
	countLater := len(mock.getSendChatActionCalls())

	if countLater > countAfterCancel {
		t.Errorf("typing indicator continued after cancel: %d > %d", countLater, countAfterCancel)
	}
}

func TestStartTypingIndicator_RepeatsAction(t *testing.T) {
	mock := newMockBot()
	ch := newTestTelegramChannel(mock)

	ctx, cancel := context.WithCancel(context.Background())

	ch.startTypingIndicator(ctx, cancel, 12345, "12345")

	// Wait long enough for multiple ticks
	time.Sleep(350 * time.Millisecond)

	cancel()

	actions := mock.getSendChatActionCalls()
	if len(actions) < 2 {
		t.Errorf("expected at least 2 SendChatAction calls for repeated typing, got %d", len(actions))
	}
}
