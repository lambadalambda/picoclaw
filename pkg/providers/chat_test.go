package providers

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type scriptedChatProvider struct {
	results []scriptedChatResult
	calls   int
}

type scriptedChatResult struct {
	resp *LLMResponse
	err  error
}

func (p *scriptedChatProvider) Chat(_ context.Context, _ []Message, _ []ToolDefinition, _ string, _ map[string]interface{}) (*LLMResponse, error) {
	p.calls++
	if len(p.results) == 0 {
		return &LLMResponse{Content: "ok"}, nil
	}
	idx := p.calls - 1
	if idx >= len(p.results) {
		idx = len(p.results) - 1
	}
	r := p.results[idx]
	if r.err != nil {
		return nil, r.err
	}
	if r.resp != nil {
		return r.resp, nil
	}
	return &LLMResponse{Content: "ok"}, nil
}

func (p *scriptedChatProvider) GetDefaultModel() string {
	return "test-model"
}

func TestChatWithTimeout_RetriesDeadlineExceeded(t *testing.T) {
	p := &scriptedChatProvider{results: []scriptedChatResult{
		{err: fmt.Errorf("failed to send request: Post \"https://api.z.ai/api/coding/paas/v4/chat/completions\": %w", context.DeadlineExceeded)},
		{resp: &LLMResponse{Content: "ok-after-retry"}},
	}}

	resp, err := ChatWithTimeout(context.Background(), 10*time.Millisecond, p, nil, nil, "glm-5", nil)
	if err != nil {
		t.Fatalf("ChatWithTimeout() error = %v", err)
	}
	if resp == nil || resp.Content != "ok-after-retry" {
		t.Fatalf("ChatWithTimeout() response = %#v, want retry success", resp)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", p.calls)
	}
}

func TestChatWithTimeout_DoesNotRetryOnNonTimeoutError(t *testing.T) {
	p := &scriptedChatProvider{results: []scriptedChatResult{{err: fmt.Errorf("bad request")}}}

	_, err := ChatWithTimeout(context.Background(), 10*time.Millisecond, p, nil, nil, "glm-5", nil)
	if err == nil {
		t.Fatal("ChatWithTimeout() error = nil, want error")
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
}

func TestChatWithTimeout_DoesNotRetryWithoutTimeout(t *testing.T) {
	p := &scriptedChatProvider{results: []scriptedChatResult{
		{err: fmt.Errorf("failed to send request: %w", context.DeadlineExceeded)},
		{resp: &LLMResponse{Content: "ok-after-retry"}},
	}}

	_, err := ChatWithTimeout(context.Background(), 0, p, nil, nil, "glm-5", nil)
	if err == nil {
		t.Fatal("ChatWithTimeout() error = nil, want error")
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
}
