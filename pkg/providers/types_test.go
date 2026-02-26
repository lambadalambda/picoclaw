package providers

import (
	"strings"
	"testing"
)

func TestAssistantMessageFromResponse_CanonicalizesToolCalls(t *testing.T) {
	resp := &LLMResponse{
		ToolCalls: []ToolCall{
			{
				ID:   "call_1",
				Name: "exec",
				Arguments: map[string]interface{}{
					"command": "pwd",
				},
			},
		},
	}

	msg := AssistantMessageFromResponse(resp)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(msg.ToolCalls))
	}

	tc := msg.ToolCalls[0]
	if tc.Type != "function" {
		t.Fatalf("Type = %q, want function", tc.Type)
	}
	if tc.Function == nil {
		t.Fatal("Function is nil, want non-nil")
	}
	if tc.Function.Name != "exec" {
		t.Fatalf("Function.Name = %q, want exec", tc.Function.Name)
	}
	if !strings.Contains(tc.Function.Arguments, `"command":"pwd"`) {
		t.Fatalf("Function.Arguments = %q, want JSON containing command=pwd", tc.Function.Arguments)
	}
	if got, _ := tc.Arguments["command"].(string); got != "pwd" {
		t.Fatalf("Arguments[command] = %q, want pwd", got)
	}
}

func TestCanonicalizeToolCalls_BackfillsArgumentsMapFromFunctionJSON(t *testing.T) {
	out := canonicalizeToolCalls([]ToolCall{
		{
			ID: "call_2",
			Function: &FunctionCall{
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			},
		},
	})

	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}

	tc := out[0]
	if tc.Type != "function" {
		t.Fatalf("Type = %q, want function", tc.Type)
	}
	if tc.Name != "read_file" {
		t.Fatalf("Name = %q, want read_file", tc.Name)
	}
	if got, _ := tc.Arguments["path"].(string); got != "README.md" {
		t.Fatalf("Arguments[path] = %q, want README.md", got)
	}
}

func TestCanonicalizeToolCalls_PullsDescriptionFromArguments(t *testing.T) {
	out := canonicalizeToolCalls([]ToolCall{
		{
			ID:   "call_3",
			Name: "exec",
			Arguments: map[string]interface{}{
				"description": "Check git status",
				"command":     "git status -sb",
			},
		},
	})

	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}

	tc := out[0]
	if tc.Description != "Check git status" {
		t.Fatalf("Description = %q, want %q", tc.Description, "Check git status")
	}
}
