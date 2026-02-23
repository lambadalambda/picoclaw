package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// SessionHistoryTool reads the append-only transcript log for the current chat
// session and returns a window of messages. This is useful after compaction when
// earlier tool calls/results have been truncated out of the in-context history.
type SessionHistoryTool struct {
	workspace string
}

func NewSessionHistoryTool(workspace string) *SessionHistoryTool {
	return &SessionHistoryTool{workspace: workspace}
}

func (t *SessionHistoryTool) Name() string {
	return "session_history"
}

func (t *SessionHistoryTool) Description() string {
	return "Fetch recent chat history from the on-disk transcript (including tool calls/results). Use this when compaction removed earlier context and you need to see what you did before (e.g., the prior exec/edit_file call)."
}

func (t *SessionHistoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "How many transcript entries to include (default 20, max 200)",
			},
			"before_contains": map[string]interface{}{
				"type":        "string",
				"description": "Optional anchor: find the most recent message whose content contains this substring, then return entries before it",
			},
			"before_role": map[string]interface{}{
				"type":        "string",
				"description": "Optional: only anchor on messages with this role (user, assistant, tool)",
			},
			"include_anchor": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, include the anchor message itself at the end",
			},
			"roles": map[string]interface{}{
				"type":        "array",
				"description": "Optional filter: only include entries with these roles (user, assistant, tool)",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"has_tool_calls": map[string]interface{}{
				"type":        "boolean",
				"description": "Optional filter: only include assistant messages that requested tools",
			},
			"tool_name": map[string]interface{}{
				"type":        "string",
				"description": "Optional filter: only include entries related to this tool name (e.g. exec, edit_file)",
			},
			"max_chars": map[string]interface{}{
				"type":        "integer",
				"description": "Truncate returned message content to this many chars (default 500)",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional: explicit session key (defaults to current channel/chat context)",
			},
		},
	}
}

type transcriptItem struct {
	Line  int
	Entry session.TranscriptEntry
}

func (t *SessionHistoryTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	limit, err := parseOptionalIntArg(args, "limit", 20)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	maxChars, err := parseOptionalIntArg(args, "max_chars", 500)
	if err != nil {
		return "", err
	}
	if maxChars <= 0 {
		maxChars = 500
	}

	beforeContains, _ := args["before_contains"].(string)
	beforeContains = strings.TrimSpace(beforeContains)
	beforeRole, _ := args["before_role"].(string)
	beforeRole = strings.TrimSpace(beforeRole)

	includeAnchor, _ := args["include_anchor"].(bool)
	hasToolCalls, _ := args["has_tool_calls"].(bool)
	toolName, _ := args["tool_name"].(string)
	toolName = strings.TrimSpace(toolName)

	rolesFilter := parseStringArray(args["roles"])
	rolesSet := make(map[string]struct{})
	for _, r := range rolesFilter {
		rolesSet[strings.TrimSpace(r)] = struct{}{}
	}
	if len(rolesSet) == 0 {
		rolesSet = nil
	}

	sessionKey, _ := args["session_key"].(string)
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		ch, chatID := getExecutionContext(args)
		if ch != "" && chatID != "" {
			sessionKey = fmt.Sprintf("%s:%s", ch, chatID)
		}
	}
	if sessionKey == "" {
		return "", fmt.Errorf("session_key is required (or run within a chat context)")
	}

	transcriptPath := session.TranscriptPath(t.workspace, sessionKey)
	items, parseErrors, err := readTranscript(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Fallback: best-effort read from the compacted sessions JSON file.
			sessionPath := filepath.Join(t.workspace, "sessions", sessionKey+".json")
			fallback, fbErr := readFromSessionFile(sessionPath)
			if fbErr != nil {
				return fmt.Sprintf("No transcript found for %s. (Tried %s).\nAlso failed to read session file %s: %v", sessionKey, transcriptPath, sessionPath, fbErr), nil
			}
			items = fallback
			transcriptPath = sessionPath
			parseErrors = 0
		} else {
			return "", err
		}
	}

	if len(items) == 0 {
		return fmt.Sprintf("No transcript entries found for %s (%s)", sessionKey, transcriptPath), nil
	}

	toolByCallID := indexToolCalls(items)

	anchorIdx := len(items) // default: end
	anchorDesc := "end-of-transcript"
	if beforeContains != "" {
		anchorIdx = -1
		for i := len(items) - 1; i >= 0; i-- {
			it := items[i]
			if beforeRole != "" && it.Entry.Role != beforeRole {
				continue
			}
			if strings.Contains(it.Entry.Content, beforeContains) {
				anchorIdx = i
				anchorDesc = fmt.Sprintf("line %d", it.Line)
				break
			}
		}
		if anchorIdx < 0 {
			return fmt.Sprintf("Anchor not found (before_contains=%q, before_role=%q). Transcript: %s", beforeContains, beforeRole, transcriptPath), nil
		}
	}

	start := anchorIdx - limit
	if start < 0 {
		start = 0
	}
	window := items[start:anchorIdx]
	if includeAnchor && anchorIdx >= 0 && anchorIdx < len(items) {
		window = append(window, items[anchorIdx])
	}

	filtered := make([]transcriptItem, 0, len(window))
	for _, it := range window {
		e := it.Entry
		if rolesSet != nil {
			if _, ok := rolesSet[e.Role]; !ok {
				continue
			}
		}
		if hasToolCalls {
			if e.Role != "assistant" || len(e.ToolCalls) == 0 {
				continue
			}
		}
		if toolName != "" {
			if !entryMatchesTool(e, toolName, toolByCallID) {
				continue
			}
		}
		filtered = append(filtered, it)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session: %s\n", sessionKey))
	sb.WriteString(fmt.Sprintf("Source: %s\n", transcriptPath))
	sb.WriteString(fmt.Sprintf("Total entries: %d\n", len(items)))
	if beforeContains != "" {
		sb.WriteString(fmt.Sprintf("Anchor: %s (before_contains=%q, before_role=%q)\n", anchorDesc, beforeContains, beforeRole))
	} else {
		sb.WriteString("Anchor: end-of-transcript\n")
	}
	sb.WriteString(fmt.Sprintf("Window: %d entries (returned %d after filters)\n", len(window), len(filtered)))
	if parseErrors > 0 {
		sb.WriteString(fmt.Sprintf("Note: skipped %d invalid transcript lines\n", parseErrors))
	}
	sb.WriteString("\n")

	for _, it := range filtered {
		e := it.Entry
		content := utils.Truncate(e.Content, maxChars)
		meta := ""
		switch e.Role {
		case "assistant":
			if len(e.ToolCalls) > 0 {
				meta = formatToolCalls(e.ToolCalls)
			}
		case "tool":
			tool := toolByCallID[e.ToolCallID]
			if tool != "" {
				meta = fmt.Sprintf("tool=%s call_id=%s", tool, e.ToolCallID)
			} else if e.ToolCallID != "" {
				meta = fmt.Sprintf("call_id=%s", e.ToolCallID)
			}
			if e.Truncated {
				if meta != "" {
					meta += " "
				}
				meta += fmt.Sprintf("(truncated, original_chars=%d)", e.OriginalChars)
			}
		}

		if meta != "" {
			sb.WriteString(fmt.Sprintf("[%d] %s (%s): %s\n", it.Line, e.Role, meta, content))
		} else {
			sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", it.Line, e.Role, content))
		}
	}

	return sb.String(), nil
}

func parseStringArray(raw interface{}) []string {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func readTranscript(path string) ([]transcriptItem, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	items := make([]transcriptItem, 0, 128)
	parseErrors := 0
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e session.TranscriptEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			parseErrors++
			continue
		}
		items = append(items, transcriptItem{Line: lineNo, Entry: e})
	}
	if err := scanner.Err(); err != nil {
		return items, parseErrors, err
	}
	return items, parseErrors, nil
}

func readFromSessionFile(path string) ([]transcriptItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var s session.Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}

	items := make([]transcriptItem, 0, len(s.Messages))
	for i, m := range s.Messages {
		// Convert the session message to a transcript-like entry.
		entry := session.BuildTranscriptEntry(m)
		items = append(items, transcriptItem{Line: i + 1, Entry: entry})
	}
	return items, nil
}

func indexToolCalls(items []transcriptItem) map[string]string {
	idx := make(map[string]string)
	for _, it := range items {
		e := it.Entry
		if e.Role != "assistant" {
			continue
		}
		for _, tc := range e.ToolCalls {
			if tc.ID == "" || tc.Name == "" {
				continue
			}
			idx[tc.ID] = tc.Name
		}
	}
	return idx
}

func entryMatchesTool(e session.TranscriptEntry, toolName string, toolByCallID map[string]string) bool {
	if e.Role == "assistant" {
		for _, tc := range e.ToolCalls {
			if tc.Name == toolName {
				return true
			}
		}
		return false
	}
	if e.Role == "tool" {
		if e.ToolCallID == "" {
			return false
		}
		return toolByCallID[e.ToolCallID] == toolName
	}
	return false
}

func formatToolCalls(calls []session.TranscriptToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(calls))
	for _, tc := range calls {
		args := ""
		if tc.Arguments != nil {
			if b, err := json.Marshal(tc.Arguments); err == nil {
				args = utils.Truncate(string(b), 200)
			}
		}
		if args != "" {
			parts = append(parts, fmt.Sprintf("%s(%s)", tc.Name, args))
		} else {
			parts = append(parts, tc.Name)
		}
	}
	return "tool_calls=" + strings.Join(parts, ", ")
}
