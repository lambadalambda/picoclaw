package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/session"
)

func writeTranscriptFile(t *testing.T, path string, entries []session.TranscriptEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	defer f.Close()
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}
}

func TestSessionHistoryTool_Execute_WindowBeforeAnchor(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	entries := []session.TranscriptEntry{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "ok"},
		{Role: "assistant", Content: "", ToolCalls: []session.TranscriptToolCall{{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "go test ./..."}}}},
		{Role: "tool", Content: "PASS", ToolCallID: "tc1"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "Now do the same tool like you did earlier"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionHistoryTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		execContextChannelKey: "telegram",
		execContextChatIDKey:  "chat-1",
		"limit":               3,
		"before_contains":     "same tool like you did earlier",
		"before_role":         "user",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "tool_calls=exec") {
		t.Fatalf("expected tool_calls=exec in output, got:\n%s", out)
	}
	if !strings.Contains(out, "go test ./...") {
		t.Fatalf("expected command in output, got:\n%s", out)
	}
	if strings.Contains(out, "] user: Now do the same tool like you did earlier") {
		t.Fatalf("did not expect anchor message to be included by default, got:\n%s", out)
	}
}

func TestSessionHistoryTool_Execute_FilterToolName(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	entries := []session.TranscriptEntry{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []session.TranscriptToolCall{{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "pwd"}}}},
		{Role: "tool", Content: "/tmp", ToolCallID: "tc1"},
		{Role: "assistant", Content: "done"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionHistoryTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"session_key": key,
		"limit":       20,
		"tool_name":   "exec",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "tool_calls=exec") {
		t.Fatalf("expected exec tool call in output, got:\n%s", out)
	}
	if !strings.Contains(out, "tool=exec") {
		t.Fatalf("expected tool result to be labeled with tool=exec, got:\n%s", out)
	}
}

func TestSessionHistoryTool_Execute_RequiresSessionKeyOrContext(t *testing.T) {
	tool := NewSessionHistoryTool(t.TempDir())
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestSessionHistoryTool_Execute_UsesExecutionSessionKey(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-ctx"
	transcriptPath := session.TranscriptPath(workspace, key)

	entries := []session.TranscriptEntry{{Role: "user", Content: "hello from transcript"}}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionHistoryTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		execContextSessionKey: key,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "Session: "+key) {
		t.Fatalf("expected output to use execution session key, got:\n%s", out)
	}
	if !strings.Contains(out, "hello from transcript") {
		t.Fatalf("expected transcript content in output, got:\n%s", out)
	}
}
