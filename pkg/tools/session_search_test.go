package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/session"
)

func TestSessionSearchTool_Execute_BasicSearch(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{TSMs: now - 1000, Role: "user", Content: "hello world"},
		{TSMs: now - 500, Role: "assistant", Content: "hi there, how can I help?"},
		{TSMs: now, Role: "user", Content: "discuss the Varga chapter structure"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "Varga chapter",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "Varga chapter") {
		t.Fatalf("expected 'Varga chapter' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Session: "+key) {
		t.Fatalf("expected session key in output, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_ChannelFiltering(t *testing.T) {
	workspace := t.TempDir()

	telegramKey := "telegram:chat-1"
	telegramPath := session.TranscriptPath(workspace, telegramKey)
	slackKey := "slack:team-1"
	slackPath := session.TranscriptPath(workspace, slackKey)

	now := time.Now().UnixMilli()
	writeTranscriptFile(t, telegramPath, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "secret telegram message"},
	})
	writeTranscriptFile(t, slackPath, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "secret slack message"},
	})

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "secret",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "secret telegram message") {
		t.Fatalf("expected telegram message in output, got:\n%s", out)
	}
	if strings.Contains(out, "secret slack message") {
		t.Fatalf("did not expect slack message in output (cross-channel filtering), got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_CrossChannel(t *testing.T) {
	workspace := t.TempDir()

	telegramKey := "telegram:chat-1"
	telegramPath := session.TranscriptPath(workspace, telegramKey)
	slackKey := "slack:team-1"
	slackPath := session.TranscriptPath(workspace, slackKey)

	now := time.Now().UnixMilli()
	writeTranscriptFile(t, telegramPath, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "secret telegram message"},
	})
	writeTranscriptFile(t, slackPath, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "secret slack message"},
	})

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":         "secret",
		"days_back":     7,
		"cross_channel": true,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "secret telegram message") {
		t.Fatalf("expected telegram message in output, got:\n%s", out)
	}
	if !strings.Contains(out, "secret slack message") {
		t.Fatalf("expected slack message in output (cross_channel=true), got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_DateFiltering(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now()
	oldTime := now.AddDate(0, 0, -30).UnixMilli()
	recentTime := now.AddDate(0, 0, -1).UnixMilli()

	entries := []session.TranscriptEntry{
		{TSMs: oldTime, Role: "user", Content: "old message about testing"},
		{TSMs: recentTime, Role: "user", Content: "recent message about testing"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "testing",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "recent message about testing") {
		t.Fatalf("expected recent message in output, got:\n%s", out)
	}
	if strings.Contains(out, "old message about testing") {
		t.Fatalf("did not expect old message in output (outside days_back), got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_RoleFiltering(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{TSMs: now - 2000, Role: "user", Content: "user said testing"},
		{TSMs: now - 1000, Role: "assistant", Content: "assistant said testing"},
		{TSMs: now, Role: "tool", Content: "tool said testing"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "testing",
		"days_back":           7,
		"roles":               []interface{}{"user"},
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "user said testing") {
		t.Fatalf("expected user message in output, got:\n%s", out)
	}
	if strings.Contains(out, "assistant said testing") {
		t.Fatalf("did not expect assistant message in output (role filter), got:\n%s", out)
	}
	if strings.Contains(out, "tool said testing") {
		t.Fatalf("did not expect tool message in output (role filter), got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_Limit(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	var entries []session.TranscriptEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, session.TranscriptEntry{
			TSMs:    now + int64(i*1000),
			Role:    "user",
			Content: "message about testing",
		})
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "testing",
		"days_back":           7,
		"limit":               3,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultCount := strings.Count(out, "--- Result")
	if resultCount != 3 {
		t.Fatalf("expected 3 results, got %d (output:\n%s)", resultCount, out)
	}
}

func TestSessionSearchTool_Execute_SearchToolArguments(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{
			TSMs:    now,
			Role:    "assistant",
			Content: "",
			ToolCalls: []session.TranscriptToolCall{
				{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "npm install special-package"}},
			},
		},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "special-package",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "special-package") {
		t.Fatalf("expected 'special-package' in tool call arguments to be found, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_NoMatches(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "hello world"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "nonexistent",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "No matches found") {
		t.Fatalf("expected 'No matches found' in output, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_NoTranscriptsDir(t *testing.T) {
	workspace := t.TempDir()

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":         "anything",
		"days_back":     7,
		"cross_channel": true,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "No transcripts directory found") {
		t.Fatalf("expected 'No transcripts directory found' in output, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_RequiresQuery(t *testing.T) {
	tool := NewSessionSearchTool(t.TempDir())
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatalf("expected error for missing query")
	}
}

func TestSessionSearchTool_Execute_CaseInsensitive(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "HELLO World Testing"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "hello world",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "HELLO World Testing") {
		t.Fatalf("expected case-insensitive match, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_TimestampFormatting(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	fixedTime := time.Now().Add(-time.Hour * 24)
	entries := []session.TranscriptEntry{
		{TSMs: fixedTime.UnixMilli(), Role: "user", Content: "testing timestamp"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "timestamp",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expectedDate := fixedTime.Format("2006-01-02")
	if !strings.Contains(out, expectedDate) {
		t.Fatalf("expected formatted timestamp in output, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_UsesSessionKeyForChannel(t *testing.T) {
	workspace := t.TempDir()
	key := "discord:server-123"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "discord message about testing"},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "testing",
		"days_back":           7,
		execContextSessionKey: "discord:server-456",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "discord message about testing") {
		t.Fatalf("expected discord message in output (should filter by channel from session key), got:\n%s", out)
	}
}

func TestSortResultsByTime(t *testing.T) {
	now := time.Now().UnixMilli()
	results := []searchResult{
		{Entry: session.TranscriptEntry{TSMs: now - 2000, Content: "oldest"}},
		{Entry: session.TranscriptEntry{TSMs: now, Content: "newest"}},
		{Entry: session.TranscriptEntry{TSMs: now - 1000, Content: "middle"}},
	}

	sortResultsByTime(results)

	if results[0].Entry.Content != "newest" {
		t.Fatalf("expected newest first, got: %s", results[0].Entry.Content)
	}
	if results[1].Entry.Content != "middle" {
		t.Fatalf("expected middle second, got: %s", results[1].Entry.Content)
	}
	if results[2].Entry.Content != "oldest" {
		t.Fatalf("expected oldest last, got: %s", results[2].Entry.Content)
	}
}

func TestSessionSearchTool_Execute_MaxChars(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	longContent := strings.Repeat("x", 500) + "UNIQUE_MARKER" + strings.Repeat("y", 500)
	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: longContent},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "UNIQUE_MARKER",
		"days_back":           7,
		"max_chars":           100,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "UNIQUE_MARKER") {
		t.Fatalf("expected UNIQUE_MARKER in output, got:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("y", 100)) {
		t.Fatalf("expected content to be truncated, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_ToolResultWithTruncation(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{
			TSMs:          now,
			Role:          "tool",
			Content:       "searching for content",
			ToolCallID:    "tc123",
			Truncated:     true,
			OriginalChars: 10000,
		},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "searching",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "call_id=tc123") {
		t.Fatalf("expected call_id in output, got:\n%s", out)
	}
	if !strings.Contains(out, "truncated") || !strings.Contains(out, "original_chars=10000") {
		t.Fatalf("expected truncation info in output, got:\n%s", out)
	}
}

func TestSessionSearchTool_MultipleTranscriptFiles(t *testing.T) {
	workspace := t.TempDir()

	now := time.Now().UnixMilli()

	key1 := "telegram:chat-1"
	path1 := session.TranscriptPath(workspace, key1)
	writeTranscriptFile(t, path1, []session.TranscriptEntry{
		{TSMs: now - 1000, Role: "user", Content: "first session result"},
	})

	key2 := "telegram:chat-2"
	path2 := session.TranscriptPath(workspace, key2)
	writeTranscriptFile(t, path2, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "second session result"},
	})

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "result",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "first session result") {
		t.Fatalf("expected first session content, got:\n%s", out)
	}
	if !strings.Contains(out, "second session result") {
		t.Fatalf("expected second session content, got:\n%s", out)
	}
	if !strings.Contains(out, "Session: "+key1) {
		t.Fatalf("expected first session key, got:\n%s", out)
	}
	if !strings.Contains(out, "Session: "+key2) {
		t.Fatalf("expected second session key, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_FailsClosedWhenChannelContextUnavailable(t *testing.T) {
	workspace := t.TempDir()

	telegramKey := "telegram:chat-1"
	telegramPath := session.TranscriptPath(workspace, telegramKey)
	slackKey := "slack:team-1"
	slackPath := session.TranscriptPath(workspace, slackKey)

	now := time.Now().UnixMilli()
	writeTranscriptFile(t, telegramPath, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "secret telegram message"},
	})
	writeTranscriptFile(t, slackPath, []session.TranscriptEntry{
		{TSMs: now, Role: "user", Content: "secret slack message"},
	})

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":     "secret",
		"days_back": 7,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(out, "Channel context not available") {
		t.Fatalf("expected channel context error message, got:\n%s", out)
	}
	if strings.Contains(out, "secret telegram message") {
		t.Fatalf("did not expect to search across channels when context unavailable, got:\n%s", out)
	}
	if strings.Contains(out, "secret slack message") {
		t.Fatalf("did not expect to search across channels when context unavailable, got:\n%s", out)
	}
}

func TestSessionSearchTool_Execute_DeduplicatesContentAndToolCallMatch(t *testing.T) {
	workspace := t.TempDir()
	key := "telegram:chat-1"
	transcriptPath := session.TranscriptPath(workspace, key)

	now := time.Now().UnixMilli()
	entries := []session.TranscriptEntry{
		{
			TSMs:    now,
			Role:    "assistant",
			Content: "unique-pattern in content",
			ToolCalls: []session.TranscriptToolCall{
				{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "unique-pattern in args"}},
			},
		},
	}
	writeTranscriptFile(t, transcriptPath, entries)

	tool := NewSessionSearchTool(workspace)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":               "unique-pattern",
		"days_back":           7,
		execContextChannelKey: "telegram",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultCount := strings.Count(out, "--- Result")
	if resultCount != 1 {
		t.Fatalf("expected 1 result (deduplicated), got %d (output:\n%s)", resultCount, out)
	}
}
