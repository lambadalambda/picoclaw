package main

import (
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestParseUsageMonthArg_DefaultAndExplicit(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)

	month, err := parseUsageMonthArg(nil, now)
	if err != nil {
		t.Fatalf("parseUsageMonthArg() unexpected error: %v", err)
	}
	if month != "2026-03" {
		t.Fatalf("default month = %q, want 2026-03", month)
	}

	month, err = parseUsageMonthArg([]string{"--month", "2026-02"}, now)
	if err != nil {
		t.Fatalf("parseUsageMonthArg() unexpected error: %v", err)
	}
	if month != "2026-02" {
		t.Fatalf("explicit month = %q, want 2026-02", month)
	}
}

func TestParseUsageMonthArg_Invalid(t *testing.T) {
	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)

	if _, err := parseUsageMonthArg([]string{"--month"}, now); err == nil {
		t.Fatal("expected error for missing --month value")
	}

	if _, err := parseUsageMonthArg([]string{"--month", "2026/03"}, now); err == nil {
		t.Fatal("expected error for invalid month format")
	}

	if _, err := parseUsageMonthArg([]string{"--wat"}, now); err == nil {
		t.Fatal("expected error for unknown option")
	}
}

func TestSummarizeTokenUsage_FiltersAndAggregatesByMonth(t *testing.T) {
	records := []providers.TokenUsageRecord{
		{
			TSMs:                     time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC).UnixMilli(),
			Provider:                 "anthropic",
			Model:                    "claude-opus-4-6",
			PromptTokens:             100,
			CompletionTokens:         20,
			TotalTokens:              120,
			CacheReadInputTokens:     1000,
			CacheCreationInputTokens: 200,
		},
		{
			TSMs:               time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC).UnixMilli(),
			Provider:           "openai-compatible",
			Model:              "glm-5",
			PromptTokens:       50,
			CompletionTokens:   10,
			TotalTokens:        60,
			CachedPromptTokens: 40,
		},
		{
			TSMs:               time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC).UnixMilli(),
			Provider:           "anthropic",
			Model:              "claude-opus-4-6",
			PromptTokens:       999,
			CompletionTokens:   999,
			TotalTokens:        1998,
			CachedPromptTokens: 999,
		},
	}

	summary := summarizeTokenUsage(records, "2026-03")
	if summary.Records != 2 {
		t.Fatalf("summary.Records = %d, want 2", summary.Records)
	}
	if summary.FirstDay != "2026-03-10" || summary.LastDay != "2026-03-11" {
		t.Fatalf("day range = %s..%s, want 2026-03-10..2026-03-11", summary.FirstDay, summary.LastDay)
	}
	if summary.Totals.Calls != 2 {
		t.Fatalf("totals.calls = %d, want 2", summary.Totals.Calls)
	}
	if summary.Totals.PromptTokens != 150 {
		t.Fatalf("totals.prompt_tokens = %d, want 150", summary.Totals.PromptTokens)
	}
	if summary.Totals.CompletionTokens != 30 {
		t.Fatalf("totals.completion_tokens = %d, want 30", summary.Totals.CompletionTokens)
	}
	if summary.Totals.TotalTokens != 180 {
		t.Fatalf("totals.total_tokens = %d, want 180", summary.Totals.TotalTokens)
	}
	if summary.Totals.CacheReadInputTokens != 1000 {
		t.Fatalf("totals.cache_read_input_tokens = %d, want 1000", summary.Totals.CacheReadInputTokens)
	}
	if summary.Totals.CachedPromptTokens != 40 {
		t.Fatalf("totals.cached_prompt_tokens = %d, want 40", summary.Totals.CachedPromptTokens)
	}

	if len(summary.ByProvider) != 2 {
		t.Fatalf("len(summary.ByProvider) = %d, want 2", len(summary.ByProvider))
	}
}
