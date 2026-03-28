package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const tokenUsageFileName = "llm_token_usage.jsonl"

type TokenUsageRecord struct {
	TSMs                                int64  `json:"ts_ms"`
	Provider                            string `json:"provider,omitempty"`
	Model                               string `json:"model,omitempty"`
	PromptTokens                        int    `json:"prompt_tokens"`
	CompletionTokens                    int    `json:"completion_tokens"`
	TotalTokens                         int    `json:"total_tokens"`
	InputTokens                         int    `json:"input_tokens,omitempty"`
	OutputTokens                        int    `json:"output_tokens,omitempty"`
	CacheReadInputTokens                int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens            int    `json:"cache_creation_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int    `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int    `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CachedPromptTokens                  int    `json:"cached_prompt_tokens,omitempty"`
}

func TokenUsagePath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, "usage", tokenUsageFileName)
}

func NewUsageTrackingProvider(inner LLMProvider, workspace string) LLMProvider {
	if inner == nil {
		return nil
	}
	if _, ok := inner.(*usageTrackingProvider); ok {
		return inner
	}

	path := TokenUsagePath(workspace)
	if path == "" {
		return inner
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		logger.WarnCF("provider", "Failed to initialize token usage tracking",
			map[string]interface{}{
				"path":  path,
				"error": err.Error(),
			})
		return inner
	}

	return &usageTrackingProvider{inner: inner, path: path}
}

type usageTrackingProvider struct {
	inner LLMProvider
	path  string
	mu    sync.Mutex
}

func (p *usageTrackingProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	resp, err := p.inner.Chat(ctx, messages, tools, model, options)
	if err != nil || resp == nil || resp.Usage == nil {
		return resp, err
	}

	rec, ok := tokenUsageRecordFromUsage(model, resp.Usage)
	if !ok {
		return resp, err
	}

	if writeErr := p.append(rec); writeErr != nil {
		logger.WarnCF("provider", "Failed to persist token usage record",
			map[string]interface{}{
				"path":  p.path,
				"error": writeErr.Error(),
			})
	}

	return resp, err
}

func (p *usageTrackingProvider) GetDefaultModel() string {
	return p.inner.GetDefaultModel()
}

func (p *usageTrackingProvider) append(rec TokenUsageRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	f, err := os.OpenFile(p.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}
	_, err = f.Write([]byte("\n"))
	return err
}

func tokenUsageRecordFromUsage(model string, usage *UsageInfo) (TokenUsageRecord, bool) {
	if usage == nil {
		return TokenUsageRecord{}, false
	}

	promptTokens := nonNegativeInt(usage.PromptTokens)
	completionTokens := nonNegativeInt(usage.CompletionTokens)
	totalTokens := nonNegativeInt(usage.TotalTokens)
	if totalTokens <= 0 {
		totalTokens = promptTokens + completionTokens
	}

	inputTokens := nonNegativeInt(usage.InputTokens)
	if inputTokens <= 0 {
		inputTokens = promptTokens
	}
	outputTokens := nonNegativeInt(usage.OutputTokens)
	if outputTokens <= 0 {
		outputTokens = completionTokens
	}

	cacheReadTokens := nonNegativeInt(usage.CacheReadInputTokens)
	cacheCreationTokens := nonNegativeInt(usage.CacheCreationInputTokens)
	cacheCreation5m := nonNegativeInt(usage.CacheCreationEphemeral5mInputTokens)
	cacheCreation1h := nonNegativeInt(usage.CacheCreationEphemeral1hInputTokens)
	if cacheCreationTokens <= 0 {
		cacheCreationTokens = cacheCreation5m + cacheCreation1h
	}
	cachedPromptTokens := nonNegativeInt(usage.CachedPromptTokens)

	if promptTokens <= 0 && completionTokens <= 0 && totalTokens <= 0 && inputTokens <= 0 && outputTokens <= 0 && cacheReadTokens <= 0 && cacheCreationTokens <= 0 && cachedPromptTokens <= 0 {
		return TokenUsageRecord{}, false
	}

	rec := TokenUsageRecord{
		TSMs:                                time.Now().UnixMilli(),
		Provider:                            strings.TrimSpace(usage.Provider),
		Model:                               strings.TrimSpace(model),
		PromptTokens:                        promptTokens,
		CompletionTokens:                    completionTokens,
		TotalTokens:                         totalTokens,
		InputTokens:                         inputTokens,
		OutputTokens:                        outputTokens,
		CacheReadInputTokens:                cacheReadTokens,
		CacheCreationInputTokens:            cacheCreationTokens,
		CacheCreationEphemeral5mInputTokens: cacheCreation5m,
		CacheCreationEphemeral1hInputTokens: cacheCreation1h,
		CachedPromptTokens:                  cachedPromptTokens,
	}
	if rec.Provider == "" {
		rec.Provider = "unknown"
	}

	return rec, true
}

func nonNegativeInt(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
