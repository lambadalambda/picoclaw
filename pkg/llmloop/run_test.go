package llmloop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type mockProvider struct {
	responses []*providers.LLMResponse
	err       error
	calls     int
	seenMsgs  [][]providers.Message
}

func (m *mockProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	m.calls++
	cloned := make([]providers.Message, len(messages))
	copy(cloned, messages)
	m.seenMsgs = append(m.seenMsgs, cloned)
	if m.err != nil {
		return nil, m.err
	}
	if len(m.responses) == 0 {
		return &providers.LLMResponse{Content: ""}, nil
	}
	r := m.responses[0]
	m.responses = m.responses[1:]
	return r, nil
}

func (m *mockProvider) GetDefaultModel() string { return "test-model" }

func TestRun_DirectResponse(t *testing.T) {
	p := &mockProvider{responses: []*providers.LLMResponse{{Content: "hello"}}}

	res, err := Run(context.Background(), RunOptions{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 3,
		Messages:      []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.FinalContent != "hello" {
		t.Fatalf("FinalContent = %q, want %q", res.FinalContent, "hello")
	}
	if res.Exhausted {
		t.Fatal("expected exhausted=false")
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1", res.Iterations)
	}
}

func TestRun_ToolCallFlow(t *testing.T) {
	p := &mockProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "tool", Arguments: map[string]interface{}{}}}},
		{Content: "done"},
	}}

	res, err := Run(context.Background(), RunOptions{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 3,
		Messages:      []providers.Message{{Role: "user", Content: "run"}},
		BuildToolDefs: func(iteration int, messages []providers.Message) []providers.ToolDefinition {
			return []providers.ToolDefinition{{
				Type: "function",
				Function: providers.ToolFunctionDefinition{
					Name: "tool",
				},
			}}
		},
		ExecuteTools: func(ctx context.Context, toolCalls []providers.ToolCall, iteration int) []providers.Message {
			return []providers.Message{providers.ToolResultMessage("tc1", "tool_ok")}
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.FinalContent != "done" {
		t.Fatalf("FinalContent = %q, want %q", res.FinalContent, "done")
	}
	if res.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2", res.Iterations)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("Messages len = %d, want 3", len(res.Messages))
	}
	if res.Messages[1].Role != "assistant" {
		t.Fatalf("message[1].Role = %q, want assistant", res.Messages[1].Role)
	}
	if res.Messages[2].Role != "tool" {
		t.Fatalf("message[2].Role = %q, want tool", res.Messages[2].Role)
	}
}

func TestRun_Exhausted(t *testing.T) {
	p := &mockProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "tool", Arguments: map[string]interface{}{}}}},
	}}

	res, err := Run(context.Background(), RunOptions{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 1,
		Messages:      []providers.Message{{Role: "user", Content: "run"}},
		ExecuteTools: func(ctx context.Context, toolCalls []providers.ToolCall, iteration int) []providers.Message {
			return []providers.Message{providers.ToolResultMessage("tc1", "tool_ok")}
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Exhausted {
		t.Fatal("expected exhausted=true")
	}
	if res.FinalContent != "" {
		t.Fatalf("FinalContent = %q, want empty", res.FinalContent)
	}
}

func TestRun_ProviderError(t *testing.T) {
	p := &mockProvider{err: errors.New("provider down")}

	failedCalled := false
	_, err := Run(context.Background(), RunOptions{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 2,
		Messages:      []providers.Message{{Role: "user", Content: "run"}},
		Hooks: Hooks{
			LLMCallFailed: func(iteration int, err error) {
				failedCalled = true
			},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !failedCalled {
		t.Fatal("expected failure hook to be called")
	}
}

func TestRun_AppliesMessageBudget_BeforeProviderCall(t *testing.T) {
	p := &mockProvider{responses: []*providers.LLMResponse{{Content: "ok"}}}

	longTool := strings.Repeat("x", 120)
	_, err := Run(context.Background(), RunOptions{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 1,
		MessageBudget: providers.MessageBudget{
			MaxToolMessageChars: 24,
		},
		Messages: []providers.Message{
			{Role: "system", Content: "sys"},
			{Role: "tool", Content: longTool},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.seenMsgs) != 1 || len(p.seenMsgs[0]) != 2 {
		t.Fatalf("unexpected captured messages: %+v", p.seenMsgs)
	}
	if got := len(p.seenMsgs[0][1].Content); got > 24 {
		t.Fatalf("tool message len = %d, want <= 24", got)
	}
	if !strings.Contains(p.seenMsgs[0][1].Content, "truncated") {
		t.Fatalf("expected truncation marker, got %q", p.seenMsgs[0][1].Content)
	}
}

func TestRun_AppliesMessageBudget_MaxTotalChars(t *testing.T) {
	p := &mockProvider{responses: []*providers.LLMResponse{{Content: "ok"}}}

	_, err := Run(context.Background(), RunOptions{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 1,
		MessageBudget: providers.MessageBudget{
			MaxTotalChars:   32,
			MaxMessageChars: 32,
		},
		Messages: []providers.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: strings.Repeat("a", 20)},
			{Role: "user", Content: strings.Repeat("b", 20)},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.seenMsgs) != 1 {
		t.Fatalf("expected 1 captured call, got %d", len(p.seenMsgs))
	}
	call := p.seenMsgs[0]
	if len(call) != 2 {
		t.Fatalf("expected 2 messages after budgeting, got %d", len(call))
	}
	if call[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", call[0].Role)
	}
	if !strings.Contains(call[1].Content, "b") {
		t.Fatalf("expected latest user message to be kept, got %q", call[1].Content)
	}
}
