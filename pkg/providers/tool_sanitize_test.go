package providers

import "testing"

func TestSanitizeToolTranscript_DropsLeadingToolMessage(t *testing.T) {
	in := []Message{
		{Role: "tool", Content: "orphan", ToolCallID: "call-orphan"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call-1", Name: "exec"}}},
		{Role: "tool", Content: "ok", ToolCallID: "call-1"},
		{Role: "assistant", Content: "done"},
	}

	out, dropped := SanitizeToolTranscript(in)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	if out[0].Role != "assistant" {
		t.Fatalf("out[0].Role = %q, want assistant", out[0].Role)
	}
}

func TestSanitizeToolTranscript_RollsBackIncompleteToolBatch(t *testing.T) {
	in := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call-1", Name: "exec"}, {ID: "call-2", Name: "exec"}}},
		{Role: "tool", Content: "out1", ToolCallID: "call-1"},
		{Role: "assistant", Content: "done"},
	}

	out, dropped := SanitizeToolTranscript(in)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Role != "user" || out[1].Role != "assistant" {
		t.Fatalf("roles = [%q, %q], want [user, assistant]", out[0].Role, out[1].Role)
	}
	if out[1].Content != "done" {
		t.Fatalf("out[1].Content = %q, want done", out[1].Content)
	}
}

func TestSanitizeToolTranscript_LeavesCompleteToolBatch(t *testing.T) {
	in := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call-1", Name: "exec"}}},
		{Role: "tool", Content: "out1", ToolCallID: "call-1"},
		{Role: "assistant", Content: "done"},
	}

	out, dropped := SanitizeToolTranscript(in)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
}
