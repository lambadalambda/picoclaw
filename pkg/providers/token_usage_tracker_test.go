package providers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type staticUsageProvider struct {
	response *LLMResponse
}

func (p *staticUsageProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	return p.response, nil
}

func (p *staticUsageProvider) GetDefaultModel() string {
	return ""
}

func TestUsageInfoFromMap_OpenAICompatible(t *testing.T) {
	usage := map[string]interface{}{
		"prompt_tokens":     1200.0,
		"completion_tokens": 30.0,
		"cache_creation": map[string]interface{}{
			"ephemeral_1h_input_tokens": 45.0,
		},
		"prompt_tokens_details": map[string]interface{}{
			"cached_tokens": 800.0,
		},
	}

	info := usageInfoFromMap(usage, "openai-compatible")
	if info == nil {
		t.Fatal("usageInfoFromMap() = nil, want usage info")
	}
	if info.Provider != "openai-compatible" {
		t.Fatalf("Provider = %q, want openai-compatible", info.Provider)
	}
	if info.PromptTokens != 1200 {
		t.Fatalf("PromptTokens = %d, want 1200", info.PromptTokens)
	}
	if info.CompletionTokens != 30 {
		t.Fatalf("CompletionTokens = %d, want 30", info.CompletionTokens)
	}
	if info.TotalTokens != 1230 {
		t.Fatalf("TotalTokens = %d, want 1230", info.TotalTokens)
	}
	if info.CachedPromptTokens != 800 {
		t.Fatalf("CachedPromptTokens = %d, want 800", info.CachedPromptTokens)
	}
	if info.CacheCreationInputTokens != 45 {
		t.Fatalf("CacheCreationInputTokens = %d, want 45", info.CacheCreationInputTokens)
	}
	if info.CacheCreationEphemeral1hInputTokens != 45 {
		t.Fatalf("CacheCreationEphemeral1hInputTokens = %d, want 45", info.CacheCreationEphemeral1hInputTokens)
	}
}

func TestUsageTrackingProvider_PersistsUsageRecord(t *testing.T) {
	workspace := t.TempDir()
	inner := &staticUsageProvider{response: &LLMResponse{
		Content:      "ok",
		FinishReason: "stop",
		Usage: &UsageInfo{
			Provider:                            "anthropic",
			PromptTokens:                        123,
			CompletionTokens:                    45,
			TotalTokens:                         168,
			InputTokens:                         3,
			OutputTokens:                        45,
			CacheReadInputTokens:                100,
			CacheCreationInputTokens:            20,
			CacheCreationEphemeral5mInputTokens: 0,
			CacheCreationEphemeral1hInputTokens: 20,
		},
	}}

	provider := NewUsageTrackingProvider(inner, workspace)
	if provider == nil {
		t.Fatal("NewUsageTrackingProvider() returned nil")
	}

	if _, err := provider.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-opus-4-6", map[string]interface{}{}); err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	path := TokenUsagePath(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", path, err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("record lines = %d, want 1", len(lines))
	}

	var rec TokenUsageRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("json.Unmarshal record error: %v", err)
	}
	if rec.Provider != "anthropic" {
		t.Fatalf("Provider = %q, want anthropic", rec.Provider)
	}
	if rec.Model != "claude-opus-4-6" {
		t.Fatalf("Model = %q, want claude-opus-4-6", rec.Model)
	}
	if rec.PromptTokens != 123 {
		t.Fatalf("PromptTokens = %d, want 123", rec.PromptTokens)
	}
	if rec.CompletionTokens != 45 {
		t.Fatalf("CompletionTokens = %d, want 45", rec.CompletionTokens)
	}
	if rec.TotalTokens != 168 {
		t.Fatalf("TotalTokens = %d, want 168", rec.TotalTokens)
	}
	if rec.CacheReadInputTokens != 100 {
		t.Fatalf("CacheReadInputTokens = %d, want 100", rec.CacheReadInputTokens)
	}
	if rec.CacheCreationInputTokens != 20 {
		t.Fatalf("CacheCreationInputTokens = %d, want 20", rec.CacheCreationInputTokens)
	}
}
