package tools

import (
	"context"
	"fmt"
	"strings"
)

// CompactMode defines the compaction behavior
type CompactMode string

const (
	CompactModeSoft CompactMode = "soft" // Summarize everything except last N messages (default)
	CompactModeHard CompactMode = "hard" // Summarize EVERYTHING including recent messages
)

// CompactCallback is the function signature for triggering compaction
// Returns the summary text and any error
type CompactCallback func(sessionKey string, mode CompactMode, keepLast int) (string, error)

// CompactTool lets the agent explicitly trigger context compaction.
// This is useful when the agent recognizes a natural topic boundary
// or wants to clear context for a fresh start.
type CompactTool struct {
	callback CompactCallback
}

func NewCompactTool(callback CompactCallback) *CompactTool {
	return &CompactTool{callback: callback}
}

func (t *CompactTool) Name() string {
	return "compact"
}

func (t *CompactTool) Description() string {
	return "Trigger context compaction explicitly. Use this to summarize and compress conversation history, " +
		"freeing up context window. Useful after completing a complex task, when switching topics, " +
		"or when you recognize the conversation is getting noisy/unfocused."
}

func (t *CompactTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Compaction mode: 'soft' (default) keeps last N messages for continuity, 'hard' summarizes everything for a true fresh start",
				"enum":        []string{"soft", "hard"},
			},
			"keep_last": map[string]interface{}{
				"type":        "integer",
				"description": "Number of recent messages to keep (soft mode only, default 4). Ignored in hard mode.",
			},
		},
	}
}

func (t *CompactTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if t.callback == nil {
		return "Error: Compaction not configured - no callback set", nil
	}

	// Parse mode
	modeStr, _ := args["mode"].(string)
	mode := CompactModeSoft
	if strings.ToLower(modeStr) == "hard" {
		mode = CompactModeHard
	}

	// Parse keep_last (only used in soft mode)
	keepLast := 4
	if raw, ok := args["keep_last"]; ok {
		switch v := raw.(type) {
		case int:
			keepLast = v
		case float64:
			keepLast = int(v)
		}
	}
	if keepLast < 0 {
		keepLast = 0
	}
	if keepLast > 50 {
		keepLast = 50
	}

	// Get session key from execution context
	ch, chatID := getExecutionContext(args)
	if ch == "" || chatID == "" {
		return "", fmt.Errorf("could not determine session key from context")
	}
	sessionKey := fmt.Sprintf("%s:%s", ch, chatID)

	summary, err := t.callback(sessionKey, mode, keepLast)
	if err != nil {
		return "", fmt.Errorf("compaction failed: %w", err)
	}

	modeDesc := "soft"
	if mode == CompactModeHard {
		modeDesc = "hard"
	}

	return fmt.Sprintf("Context compacted (%s mode, kept last %d messages).\n\nSummary:\n%s", modeDesc, keepLast, summary), nil
}
