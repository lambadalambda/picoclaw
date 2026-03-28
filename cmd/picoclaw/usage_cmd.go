package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type usageTotals struct {
	Calls                               int64
	PromptTokens                        int64
	CompletionTokens                    int64
	TotalTokens                         int64
	InputTokens                         int64
	OutputTokens                        int64
	CacheReadInputTokens                int64
	CacheCreationInputTokens            int64
	CacheCreationEphemeral5mInputTokens int64
	CacheCreationEphemeral1hInputTokens int64
	CachedPromptTokens                  int64
}

type usageBucket struct {
	Provider string
	Model    string
	Totals   usageTotals
}

type usageSummary struct {
	Month      string
	Records    int
	FirstDay   string
	LastDay    string
	Totals     usageTotals
	ByProvider []usageBucket
}

func usageCmd() {
	args := os.Args[2:]
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		printUsageHelp()
		return
	}

	month, err := parseUsageMonthArg(args, time.Now().UTC())
	if err != nil {
		fmt.Printf("Usage error: %v\n", err)
		printUsageHelp()
		return
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	usagePath := providers.TokenUsagePath(cfg.WorkspacePath())
	records, err := readTokenUsageRecords(usagePath)
	if err != nil {
		fmt.Printf("Error reading usage records: %v\n", err)
		return
	}

	summary := summarizeTokenUsage(records, month)

	fmt.Printf("%s picoclaw Token Usage (%s)\n\n", logo, summary.Month)
	fmt.Printf("Source: %s\n", usagePath)
	if summary.Records == 0 {
		fmt.Println("No token usage records found for this month.")
		return
	}

	fmt.Printf("Records: %d\n", summary.Records)
	fmt.Printf("Range: %s to %s\n\n", summary.FirstDay, summary.LastDay)

	printUsageTotals("All models", summary.Totals)

	for _, bucket := range summary.ByProvider {
		fmt.Println()
		label := strings.TrimSpace(bucket.Provider)
		if label == "" {
			label = "unknown"
		}
		model := strings.TrimSpace(bucket.Model)
		if model == "" {
			model = "(unknown model)"
		}
		fmt.Printf("%s / %s\n", label, model)
		printUsageTotals("", bucket.Totals)
	}
}

func printUsageHelp() {
	fmt.Println("Usage: picoclaw usage [--month YYYY-MM]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  picoclaw usage")
	fmt.Println("  picoclaw usage --month 2026-03")
}

func parseUsageMonthArg(args []string, now time.Time) (string, error) {
	month := now.UTC().Format("2006-01")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--month", "-m":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--month requires a value")
			}
			month = strings.TrimSpace(args[i+1])
			i++
		default:
			return "", fmt.Errorf("unknown option: %s", args[i])
		}
	}

	if _, err := time.Parse("2006-01", month); err != nil {
		return "", fmt.Errorf("invalid month %q (expected YYYY-MM)", month)
	}

	return month, nil
}

func readTokenUsageRecords(path string) ([]providers.TokenUsageRecord, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	records := make([]providers.TokenUsageRecord, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec providers.TokenUsageRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return records, nil
}

func summarizeTokenUsage(records []providers.TokenUsageRecord, month string) usageSummary {
	summary := usageSummary{Month: month}
	buckets := make(map[string]*usageBucket)

	for _, rec := range records {
		if rec.TSMs <= 0 {
			continue
		}

		ts := time.UnixMilli(rec.TSMs).UTC()
		if ts.Format("2006-01") != month {
			continue
		}

		day := ts.Format("2006-01-02")
		if summary.FirstDay == "" || day < summary.FirstDay {
			summary.FirstDay = day
		}
		if summary.LastDay == "" || day > summary.LastDay {
			summary.LastDay = day
		}

		summary.Records++
		accumulateUsage(&summary.Totals, rec)

		provider := strings.TrimSpace(rec.Provider)
		if provider == "" {
			provider = "unknown"
		}
		model := strings.TrimSpace(rec.Model)
		key := provider + "\x00" + model

		bucket := buckets[key]
		if bucket == nil {
			bucket = &usageBucket{Provider: provider, Model: model}
			buckets[key] = bucket
		}
		accumulateUsage(&bucket.Totals, rec)
	}

	summary.ByProvider = make([]usageBucket, 0, len(buckets))
	for _, bucket := range buckets {
		summary.ByProvider = append(summary.ByProvider, *bucket)
	}

	sort.Slice(summary.ByProvider, func(i, j int) bool {
		if summary.ByProvider[i].Totals.PromptTokens != summary.ByProvider[j].Totals.PromptTokens {
			return summary.ByProvider[i].Totals.PromptTokens > summary.ByProvider[j].Totals.PromptTokens
		}
		if summary.ByProvider[i].Totals.Calls != summary.ByProvider[j].Totals.Calls {
			return summary.ByProvider[i].Totals.Calls > summary.ByProvider[j].Totals.Calls
		}
		if summary.ByProvider[i].Provider != summary.ByProvider[j].Provider {
			return summary.ByProvider[i].Provider < summary.ByProvider[j].Provider
		}
		return summary.ByProvider[i].Model < summary.ByProvider[j].Model
	})

	return summary
}

func accumulateUsage(totals *usageTotals, rec providers.TokenUsageRecord) {
	totals.Calls++
	totals.PromptTokens += int64(rec.PromptTokens)
	totals.CompletionTokens += int64(rec.CompletionTokens)
	totals.TotalTokens += int64(rec.TotalTokens)
	totals.InputTokens += int64(rec.InputTokens)
	totals.OutputTokens += int64(rec.OutputTokens)
	totals.CacheReadInputTokens += int64(rec.CacheReadInputTokens)
	totals.CacheCreationInputTokens += int64(rec.CacheCreationInputTokens)
	totals.CacheCreationEphemeral5mInputTokens += int64(rec.CacheCreationEphemeral5mInputTokens)
	totals.CacheCreationEphemeral1hInputTokens += int64(rec.CacheCreationEphemeral1hInputTokens)
	totals.CachedPromptTokens += int64(rec.CachedPromptTokens)
}

func printUsageTotals(label string, totals usageTotals) {
	if label != "" {
		fmt.Println(label)
	}
	fmt.Printf("  calls: %d\n", totals.Calls)
	fmt.Printf("  prompt_tokens: %d\n", totals.PromptTokens)
	fmt.Printf("  completion_tokens: %d\n", totals.CompletionTokens)
	fmt.Printf("  total_tokens: %d\n", totals.TotalTokens)
	if totals.InputTokens > 0 || totals.OutputTokens > 0 {
		fmt.Printf("  input_tokens: %d\n", totals.InputTokens)
		fmt.Printf("  output_tokens: %d\n", totals.OutputTokens)
	}
	if totals.CacheReadInputTokens > 0 || totals.CacheCreationInputTokens > 0 || totals.CachedPromptTokens > 0 {
		fmt.Printf("  cache_read_input_tokens: %d\n", totals.CacheReadInputTokens)
		fmt.Printf("  cache_creation_input_tokens: %d\n", totals.CacheCreationInputTokens)
		if totals.CacheCreationEphemeral5mInputTokens > 0 || totals.CacheCreationEphemeral1hInputTokens > 0 {
			fmt.Printf("  cache_creation_ephemeral_5m_input_tokens: %d\n", totals.CacheCreationEphemeral5mInputTokens)
			fmt.Printf("  cache_creation_ephemeral_1h_input_tokens: %d\n", totals.CacheCreationEphemeral1hInputTokens)
		}
		fmt.Printf("  cached_prompt_tokens: %d\n", totals.CachedPromptTokens)
	}
}
