package session

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	// transcriptToolResultMaxChars caps how much tool output we persist per tool
	// result message. Tool outputs can be huge (and sometimes sensitive), and the
	// transcript is meant to be a lightweight debug/continuity aid.
	transcriptToolResultMaxChars = 8000

	// transcriptArgMaxStringChars caps individual tool-call argument string values
	// stored in the transcript.
	transcriptArgMaxStringChars = 2000

	// transcriptLargeArgMaxStringChars caps very large/sensitive argument fields
	// like write_file.content and edit_file.{old_text,new_text}.
	transcriptLargeArgMaxStringChars = 500

	transcriptMaxArgDepth   = 4
	transcriptMaxMapKeys    = 50
	transcriptMaxArrayItems = 50
)

// TranscriptEntry is one append-only record in workspace/transcripts/<session>.jsonl.
// It intentionally stores a simplified representation (role/content/tool calls)
// rather than the full provider wire format.
type TranscriptEntry struct {
	TSMs          int64                `json:"ts_ms"`
	Role          string               `json:"role"`
	Content       string               `json:"content"`
	ToolCalls     []TranscriptToolCall `json:"tool_calls,omitempty"`
	ToolCallID    string               `json:"tool_call_id,omitempty"`
	Truncated     bool                 `json:"truncated,omitempty"`
	OriginalChars int                  `json:"original_chars,omitempty"`
}

type TranscriptToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// TranscriptPath returns the canonical transcript path for a session key.
// In normal runtime storage, this is: <workspace>/transcripts/<session>.jsonl
func TranscriptPath(workspace, sessionKey string) string {
	return filepath.Join(workspace, "transcripts", SanitizeSessionKeyForFilename(sessionKey)+".jsonl")
}

// SanitizeSessionKeyForFilename converts a session key (usually channel:chat_id)
// to a safe filename fragment.
func SanitizeSessionKeyForFilename(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "unknown"
	}

	// Replace path separators and other problematic characters.
	// Keep ':' for readability (mirrors existing sessions/<key>.json naming).
	var b strings.Builder
	b.Grow(len(sessionKey))
	for _, r := range sessionKey {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ':' || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "_")
	}
	out = strings.Trim(out, ".:_-")
	if out == "" {
		return "unknown"
	}
	return out
}

// BuildTranscriptEntry converts a providers.Message into a persisted transcript entry.
// Tool results are truncated, and tool-call arguments are lightly sanitized.
func BuildTranscriptEntry(msg providers.Message) TranscriptEntry {
	entry := TranscriptEntry{
		TSMs:       time.Now().UnixMilli(),
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCallID: msg.ToolCallID,
	}

	if msg.Role == "tool" {
		if len(entry.Content) > transcriptToolResultMaxChars {
			entry.Truncated = true
			entry.OriginalChars = len(entry.Content)
			entry.Content = entry.Content[:transcriptToolResultMaxChars]
		}
	}

	if len(msg.ToolCalls) > 0 {
		entry.ToolCalls = make([]TranscriptToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			name := tc.Name
			if name == "" && tc.Function != nil {
				name = tc.Function.Name
			}
			call := TranscriptToolCall{ID: tc.ID, Name: name}
			if tc.Arguments != nil {
				call.Arguments = sanitizeArgs(tc.Arguments)
			}
			entry.ToolCalls = append(entry.ToolCalls, call)
		}
	}

	return entry
}

func sanitizeArgs(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		max := transcriptArgMaxStringChars
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "content", "old_text", "new_text":
			max = transcriptLargeArgMaxStringChars
		}
		out[k] = sanitizeValue(v, 0, max)
	}
	return out
}

func sanitizeValue(v interface{}, depth int, maxStringChars int) interface{} {
	if depth > transcriptMaxArgDepth {
		return "<max-depth>"
	}

	switch x := v.(type) {
	case string:
		return truncateString(x, maxStringChars)
	case []interface{}:
		if len(x) > transcriptMaxArrayItems {
			x = x[:transcriptMaxArrayItems]
		}
		out := make([]interface{}, 0, len(x))
		for _, item := range x {
			out = append(out, sanitizeValue(item, depth+1, transcriptArgMaxStringChars))
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, 0)
		count := 0
		for k, item := range x {
			if count >= transcriptMaxMapKeys {
				break
			}
			out[k] = sanitizeValue(item, depth+1, transcriptArgMaxStringChars)
			count++
		}
		return out
	default:
		return v
	}
}

func truncateString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	const suffix = "... (truncated)"
	if max <= len(suffix) {
		return s[:max]
	}
	return s[:max-len(suffix)] + suffix
}
