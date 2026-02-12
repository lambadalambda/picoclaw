package session

import (
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestNewSessionManager_NoStorage(t *testing.T) {
	sm := NewSessionManager("")
	if sm == nil {
		t.Fatal("expected non-nil SessionManager")
	}
}

func TestNewSessionManager_WithStorage(t *testing.T) {
	sm := NewSessionManager(t.TempDir())
	if sm == nil {
		t.Fatal("expected non-nil SessionManager")
	}
}

func TestGetOrCreate_NewSession(t *testing.T) {
	sm := NewSessionManager("")
	session := sm.GetOrCreate("test-key")

	if session == nil {
		t.Fatal("expected non-nil session")
	}
	if session.Key != "test-key" {
		t.Errorf("expected key 'test-key', got %q", session.Key)
	}
	if len(session.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(session.Messages))
	}
}

func TestGetOrCreate_ExistingSession(t *testing.T) {
	sm := NewSessionManager("")
	s1 := sm.GetOrCreate("key")
	s2 := sm.GetOrCreate("key")

	if s1 != s2 {
		t.Error("expected same session pointer for same key")
	}
}

func TestAddMessage(t *testing.T) {
	sm := NewSessionManager("")
	sm.GetOrCreate("key")
	sm.AddMessage("key", "user", "hello")
	sm.AddMessage("key", "assistant", "hi there")

	history := sm.GetHistory("key")
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "hi there" {
		t.Errorf("unexpected second message: %+v", history[1])
	}
}

func TestAddMessage_AutoCreatesSession(t *testing.T) {
	sm := NewSessionManager("")
	// Don't call GetOrCreate first — AddMessage should create the session
	sm.AddMessage("new-key", "user", "hello")

	history := sm.GetHistory("new-key")
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
}

func TestAddFullMessage(t *testing.T) {
	sm := NewSessionManager("")
	sm.GetOrCreate("key")

	msg := providers.Message{
		Role:    "assistant",
		Content: "Let me check that.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "exec", Arguments: map[string]interface{}{"command": "ls"}},
		},
	}
	sm.AddFullMessage("key", msg)

	history := sm.GetHistory("key")
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if len(history[0].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(history[0].ToolCalls))
	}
}

func TestGetHistory_ReturnsDeepCopy(t *testing.T) {
	sm := NewSessionManager("")
	sm.AddMessage("key", "user", "hello")

	history := sm.GetHistory("key")
	history[0].Content = "modified"

	// Original should be unchanged
	original := sm.GetHistory("key")
	if original[0].Content != "hello" {
		t.Errorf("GetHistory should return a copy, but original was modified")
	}
}

func TestGetHistory_NonexistentKey(t *testing.T) {
	sm := NewSessionManager("")
	history := sm.GetHistory("nonexistent")
	if history == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(history) != 0 {
		t.Errorf("expected 0 messages, got %d", len(history))
	}
}

func TestSummary(t *testing.T) {
	sm := NewSessionManager("")
	sm.GetOrCreate("key")

	if got := sm.GetSummary("key"); got != "" {
		t.Errorf("expected empty summary, got %q", got)
	}

	sm.SetSummary("key", "User asked about Go testing")
	if got := sm.GetSummary("key"); got != "User asked about Go testing" {
		t.Errorf("expected summary, got %q", got)
	}
}

func TestGetSummary_NonexistentKey(t *testing.T) {
	sm := NewSessionManager("")
	if got := sm.GetSummary("nonexistent"); got != "" {
		t.Errorf("expected empty summary for nonexistent key, got %q", got)
	}
}

func TestSetSummary_NonexistentKey(t *testing.T) {
	sm := NewSessionManager("")
	// Should not panic
	sm.SetSummary("nonexistent", "some summary")
}

func TestTruncateHistory(t *testing.T) {
	sm := NewSessionManager("")
	for i := 0; i < 10; i++ {
		sm.AddMessage("key", "user", "message")
	}

	sm.TruncateHistory("key", 3)
	history := sm.GetHistory("key")
	if len(history) != 3 {
		t.Errorf("expected 3 messages after truncation, got %d", len(history))
	}
}

func TestTruncateHistory_KeepMoreThanExists(t *testing.T) {
	sm := NewSessionManager("")
	sm.AddMessage("key", "user", "only one")

	sm.TruncateHistory("key", 10)
	history := sm.GetHistory("key")
	if len(history) != 1 {
		t.Errorf("expected 1 message (no truncation needed), got %d", len(history))
	}
}

func TestTruncateHistory_NonexistentKey(t *testing.T) {
	sm := NewSessionManager("")
	// Should not panic
	sm.TruncateHistory("nonexistent", 5)
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()

	// Create manager, add data, save
	sm1 := NewSessionManager(dir)
	sm1.AddMessage("chat-1", "user", "hello")
	sm1.AddMessage("chat-1", "assistant", "hi!")
	sm1.SetSummary("chat-1", "Greeting exchange")

	session := sm1.GetOrCreate("chat-1")
	if err := sm1.Save(session); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Create new manager from same dir — should load the session
	sm2 := NewSessionManager(dir)
	history := sm2.GetHistory("chat-1")
	if len(history) != 2 {
		t.Fatalf("expected 2 messages after reload, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected first message 'hello', got %q", history[0].Content)
	}
	if history[1].Content != "hi!" {
		t.Errorf("expected second message 'hi!', got %q", history[1].Content)
	}

	summary := sm2.GetSummary("chat-1")
	if summary != "Greeting exchange" {
		t.Errorf("expected summary 'Greeting exchange', got %q", summary)
	}
}

func TestSave_NoStorage(t *testing.T) {
	sm := NewSessionManager("")
	sm.AddMessage("key", "user", "hello")
	session := sm.GetOrCreate("key")

	err := sm.Save(session)
	if err != nil {
		t.Errorf("Save with no storage should return nil, got: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	sm := NewSessionManager("")
	var wg sync.WaitGroup

	// Concurrent writes to different sessions
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "session-" + string(rune('A'+i%5))
			sm.AddMessage(key, "user", "message")
			sm.GetHistory(key)
			sm.GetOrCreate(key)
		}(i)
	}

	wg.Wait()

	// Verify no panics and sessions exist
	for i := 0; i < 5; i++ {
		key := "session-" + string(rune('A'+i))
		history := sm.GetHistory(key)
		if len(history) == 0 {
			t.Errorf("expected messages for %s", key)
		}
	}
}
