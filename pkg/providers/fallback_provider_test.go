package providers

import (
	"context"
	"fmt"
	"testing"
)

type scriptedProvider struct {
	results []scriptedResult
	calls   []string
}

type scriptedResult struct {
	resp *LLMResponse
	err  error
}

func (p *scriptedProvider) Chat(_ context.Context, _ []Message, _ []ToolDefinition, model string, _ map[string]interface{}) (*LLMResponse, error) {
	p.calls = append(p.calls, model)
	if len(p.results) == 0 {
		return &LLMResponse{Content: "ok"}, nil
	}
	idx := len(p.calls) - 1
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

func (p *scriptedProvider) GetDefaultModel() string {
	return "mock-default"
}

func TestFallbackProvider_UsesFallbackModelOnAvailabilityErrors(t *testing.T) {
	primary := &scriptedProvider{results: []scriptedResult{{err: fmt.Errorf("provider error: 503 service unavailable")}}}
	backup := &scriptedProvider{results: []scriptedResult{{resp: &LLMResponse{Content: "from-backup"}}}}

	p := newFallbackProvider("primary-model", []fallbackCandidate{
		{model: "primary-model", provider: primary},
		{model: "backup-model", provider: backup},
	})

	resp, err := p.Chat(context.Background(), nil, nil, "primary-model", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil || resp.Content != "from-backup" {
		t.Fatalf("Chat() response = %#v, want backup response", resp)
	}
	if len(primary.calls) != 1 || primary.calls[0] != "primary-model" {
		t.Fatalf("primary calls = %v, want [primary-model]", primary.calls)
	}
	if len(backup.calls) != 1 || backup.calls[0] != "backup-model" {
		t.Fatalf("backup calls = %v, want [backup-model]", backup.calls)
	}
}

func TestFallbackProvider_DoesNotFallbackOnNonAvailabilityErrors(t *testing.T) {
	primary := &scriptedProvider{results: []scriptedResult{{err: fmt.Errorf("provider error: 400 invalid_request_error")}}}
	backup := &scriptedProvider{results: []scriptedResult{{resp: &LLMResponse{Content: "from-backup"}}}}

	p := newFallbackProvider("primary-model", []fallbackCandidate{
		{model: "primary-model", provider: primary},
		{model: "backup-model", provider: backup},
	})

	_, err := p.Chat(context.Background(), nil, nil, "primary-model", nil)
	if err == nil {
		t.Fatal("Chat() error = nil, want primary error")
	}
	if len(backup.calls) != 0 {
		t.Fatalf("backup should not be called, got calls=%v", backup.calls)
	}
}

func TestFallbackProvider_PrioritizesRequestedModelWhenKnown(t *testing.T) {
	primary := &scriptedProvider{results: []scriptedResult{{resp: &LLMResponse{Content: "from-primary"}}}}
	backup := &scriptedProvider{results: []scriptedResult{{resp: &LLMResponse{Content: "from-backup"}}}}

	p := newFallbackProvider("primary-model", []fallbackCandidate{
		{model: "primary-model", provider: primary},
		{model: "backup-model", provider: backup},
	})

	resp, err := p.Chat(context.Background(), nil, nil, "backup-model", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil || resp.Content != "from-backup" {
		t.Fatalf("Chat() response = %#v, want backup response", resp)
	}
	if len(backup.calls) != 1 || backup.calls[0] != "backup-model" {
		t.Fatalf("backup calls = %v, want [backup-model]", backup.calls)
	}
	if len(primary.calls) != 0 {
		t.Fatalf("primary should not be called first when backup requested, calls=%v", primary.calls)
	}
}

func TestIsModelFallbackEligibleError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "503 unavailable", err: fmt.Errorf("provider error: 503 service unavailable"), want: true},
		{name: "429 rate limit", err: fmt.Errorf("HTTP 429 too many requests"), want: true},
		{name: "model not found", err: fmt.Errorf("invalid_request_error: model not found"), want: true},
		{name: "generic bad request", err: fmt.Errorf("invalid_request_error: bad tool arguments"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isModelFallbackEligibleError(tt.err); got != tt.want {
				t.Fatalf("isModelFallbackEligibleError() = %v, want %v", got, tt.want)
			}
		})
	}
}
