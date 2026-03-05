package tools

import (
	"context"
	"fmt"
	"strings"
)

type CompactCallback func(sessionKey string, mode string) error

type CompactTool struct {
	callback CompactCallback
}

func NewCompactTool() *CompactTool {
	return &CompactTool{}
}

func (t *CompactTool) Name() string {
	return "compact"
}

func (t *CompactTool) Description() string {
	return "Trigger context compaction to summarize conversation history. Use this to free up context when switching topics or after completing a complex task. " +
		"Soft mode (default) keeps recent messages; hard mode summarizes everything for a fresh start."
}

func (t *CompactTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Compaction mode: 'soft' (default) keeps recent messages for continuity, 'hard' summarizes everything for a complete fresh start",
				"enum":        []string{"soft", "hard"},
			},
		},
	}
}

func (t *CompactTool) SetCallback(callback CompactCallback) {
	t.callback = callback
}

func (t *CompactTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if t.callback == nil {
		return "", fmt.Errorf("compact callback not configured")
	}

	sessionKey := getExecutionSessionKey(args)
	if sessionKey == "" {
		return "", fmt.Errorf("session key not available")
	}

	mode := "soft"
	if m, ok := args["mode"].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(m))
	}

	if mode != "soft" && mode != "hard" {
		return "", fmt.Errorf("invalid mode '%s': must be 'soft' or 'hard'", mode)
	}

	if err := t.callback(sessionKey, mode); err != nil {
		return fmt.Sprintf("Compaction failed: %v", err), nil
	}

	if mode == "hard" {
		return "Context compacted (hard mode): All messages summarized. Starting fresh.", nil
	}
	return "Context compacted (soft mode): Earlier messages summarized, recent context preserved.", nil
}
