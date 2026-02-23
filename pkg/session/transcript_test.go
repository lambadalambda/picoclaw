package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestSessionManager_AppendsTranscript_WithTruncatedToolResult(t *testing.T) {
	workspace := t.TempDir()
	storage := filepath.Join(workspace, "sessions")
	sm := NewSessionManager(storage)

	sessionKey := "telegram:chat-1"
	sm.AddMessage(sessionKey, "user", "hello")
	sm.AddFullMessage(sessionKey, providers.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{
			{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "go test ./..."}},
		},
	})

	toolOut := strings.Repeat("a", transcriptToolResultMaxChars+50)
	sm.AddFullMessage(sessionKey, providers.Message{Role: "tool", Content: toolOut, ToolCallID: "tc1"})

	transcriptsDir := filepath.Join(workspace, "transcripts")
	transcriptPath := filepath.Join(transcriptsDir, sanitizeSessionKeyForFilename(sessionKey)+".jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) failed: %v", transcriptPath, err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 transcript lines, got %d", len(lines))
	}

	var e1, e2, e3 TranscriptEntry
	if err := json.Unmarshal([]byte(lines[0]), &e1); err != nil {
		t.Fatalf("unmarshal entry 1: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &e2); err != nil {
		t.Fatalf("unmarshal entry 2: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &e3); err != nil {
		t.Fatalf("unmarshal entry 3: %v", err)
	}

	if e1.Role != "user" || e1.Content != "hello" {
		t.Fatalf("entry1 = %+v, want user(hello)", e1)
	}
	if e2.Role != "assistant" || len(e2.ToolCalls) != 1 || e2.ToolCalls[0].Name != "exec" {
		t.Fatalf("entry2 = %+v, want assistant with exec tool call", e2)
	}
	if e3.Role != "tool" || e3.ToolCallID != "tc1" {
		t.Fatalf("entry3 = %+v, want tool with tool_call_id tc1", e3)
	}
	if !e3.Truncated {
		t.Fatalf("expected tool result to be truncated")
	}
	if e3.OriginalChars != len(toolOut) {
		t.Fatalf("original_chars = %d, want %d", e3.OriginalChars, len(toolOut))
	}
	if len(e3.Content) > transcriptToolResultMaxChars {
		t.Fatalf("tool content len = %d, want <= %d", len(e3.Content), transcriptToolResultMaxChars)
	}
}
