package providers

import (
	"strings"
	"testing"
)

func TestApplyMessageBudget_TruncatesToolMessage(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "tool", Content: strings.Repeat("x", 120)},
	}

	out, stats := ApplyMessageBudget(messages, MessageBudget{
		MaxMessageChars:     80,
		MaxToolMessageChars: 24,
	})

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if len(out[1].Content) > 24 {
		t.Fatalf("tool message len = %d, want <= 24", len(out[1].Content))
	}
	if !strings.Contains(out[1].Content, "truncated") {
		t.Fatalf("expected truncation marker in tool message, got %q", out[1].Content)
	}
	if stats.TruncatedMessages != 1 {
		t.Fatalf("TruncatedMessages = %d, want 1", stats.TruncatedMessages)
	}
}

func TestApplyMessageBudget_MaxMessagesKeepsSystemAndLatest(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "tool", Content: "t2"},
	}

	out, stats := ApplyMessageBudget(messages, MessageBudget{MaxMessages: 3})

	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("first role = %q, want system", out[0].Role)
	}
	if out[1].Content != "u2" || out[2].Content != "t2" {
		t.Fatalf("expected newest non-system messages preserved, got %+v", out)
	}
	if stats.DroppedMessages != 2 {
		t.Fatalf("DroppedMessages = %d, want 2", stats.DroppedMessages)
	}
}

func TestApplyMessageBudget_MaxTotalCharsKeepsNewestContext(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("a", 40)},
		{Role: "user", Content: strings.Repeat("b", 40)},
	}

	out, stats := ApplyMessageBudget(messages, MessageBudget{MaxTotalChars: 50, MaxMessageChars: 100})

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("first role = %q, want system", out[0].Role)
	}
	if !strings.Contains(out[1].Content, "b") {
		t.Fatalf("expected newest user message kept, got %q", out[1].Content)
	}
	if stats.CharsAfter > 50 {
		t.Fatalf("CharsAfter = %d, want <= 50", stats.CharsAfter)
	}
}

func TestApplyMessageBudget_AlwaysKeepsLatestNonSystem(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: strings.Repeat("s", 4)},
		{Role: "user", Content: strings.Repeat("u", 20)},
	}

	out, _ := ApplyMessageBudget(messages, MessageBudget{MaxTotalChars: 5, MaxMessageChars: 50})

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Role != "system" || out[1].Role != "user" {
		t.Fatalf("expected system + latest user, got roles %q, %q", out[0].Role, out[1].Role)
	}
	if len(out[1].Content) == 0 {
		t.Fatal("expected latest user content to remain non-empty")
	}
}

func TestBudgetFromContextWindow_Defaults(t *testing.T) {
	b := BudgetFromContextWindow(0)
	if b.MaxMessages <= 0 || b.MaxTotalChars <= 0 || b.MaxMessageChars <= 0 || b.MaxToolMessageChars <= 0 {
		t.Fatalf("expected positive budget limits, got %+v", b)
	}
	if b.MaxToolMessageChars > b.MaxMessageChars {
		t.Fatalf("expected tool cap <= message cap, got tool=%d message=%d", b.MaxToolMessageChars, b.MaxMessageChars)
	}
}
