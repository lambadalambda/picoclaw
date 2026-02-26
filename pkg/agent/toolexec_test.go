package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestMaybeEchoToolCalls_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		provider:      nil,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: false,
	}

	toolCalls := []providers.ToolCall{
		{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "ls -la"}},
	}

	al.maybeEchoToolCalls(toolCalls, "telegram", "chat1")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, ok := testBus.SubscribeOutbound(ctx)
	if ok {
		t.Errorf("unexpected outbound message when echo disabled")
	}
}

func TestMaybeEchoToolCalls_Enabled(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		provider:      nil,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: true,
	}

	toolCalls := []providers.ToolCall{
		{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "ls -la"}},
	}

	al.maybeEchoToolCalls(toolCalls, "telegram", "chat1")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, ok := testBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message but got none")
	}

	if msg.Channel != "telegram" {
		t.Errorf("channel = %q, want telegram", msg.Channel)
	}
	if msg.ChatID != "chat1" {
		t.Errorf("chatID = %q, want chat1", msg.ChatID)
	}
	expected := "🔧 exec ls -la"
	if msg.Content != expected {
		t.Errorf("content = %q, want %q", msg.Content, expected)
	}
}

func TestMaybeEchoToolCalls_SystemChannel(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		provider:      nil,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: true,
	}

	toolCalls := []providers.ToolCall{
		{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "ls -la"}},
	}

	al.maybeEchoToolCalls(toolCalls, "system", "chat1")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, ok := testBus.SubscribeOutbound(ctx)
	if ok {
		t.Errorf("unexpected outbound message for system channel")
	}
}

func TestMaybeEchoToolCalls_MultipleTools(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		provider:      nil,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: true,
	}

	toolCalls := []providers.ToolCall{
		{ID: "tc1", Name: "exec", Arguments: map[string]interface{}{"command": "ls -la"}},
		{ID: "tc2", Name: "edit_file", Arguments: map[string]interface{}{"path": "/tmp/test.go"}},
	}

	al.maybeEchoToolCalls(toolCalls, "telegram", "chat1")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, ok := testBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message but got none")
	}

	expected := "🔧 exec ls -la\n🔧 edit_file /tmp/test.go"
	if msg.Content != expected {
		t.Errorf("content = %q, want %q", msg.Content, expected)
	}
}

func TestMaybeEchoToolCalls_SkipsNonEchoTools(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		provider:      nil,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: true,
	}

	toolCalls := []providers.ToolCall{
		{ID: "tc1", Name: "message", Arguments: map[string]interface{}{"content": "hello"}},
	}

	al.maybeEchoToolCalls(toolCalls, "telegram", "chat1")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, ok := testBus.SubscribeOutbound(ctx)
	if ok {
		t.Errorf("unexpected outbound message for non-echo tool")
	}
}

func TestShouldEchoToolCallsForSession(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
		want       bool
	}{
		{name: "empty", sessionKey: "", want: true},
		{name: "normal", sessionKey: "telegram:chat1", want: true},
		{name: "heartbeat root", sessionKey: "heartbeat", want: false},
		{name: "heartbeat scoped", sessionKey: "heartbeat:telegram:chat1", want: false},
		{name: "heartbeat uppercase", sessionKey: "HEARTBEAT:telegram:chat1", want: false},
		{name: "cron root", sessionKey: "cron", want: false},
		{name: "cron dashed", sessionKey: "cron-job-123", want: false},
		{name: "cron scoped", sessionKey: "cron:telegram:chat1", want: false},
		{name: "cron uppercase", sessionKey: "CRON:telegram:chat1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldEchoToolCallsForSession(tt.sessionKey); got != tt.want {
				t.Fatalf("shouldEchoToolCallsForSession(%q) = %v, want %v", tt.sessionKey, got, tt.want)
			}
		})
	}
}

func TestExecuteToolsConcurrently_DoesNotEchoForHeartbeatSession(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: true,
	}

	toolCalls := []providers.ToolCall{{
		ID:        "tc1",
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "pwd"},
	}}

	opts := processOptions{SessionKey: "heartbeat:telegram:chat1", Channel: "telegram", ChatID: "chat1", TraceID: "trace-test"}
	_ = al.executeToolsConcurrently(context.Background(), toolCalls, 1, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if msg, ok := testBus.SubscribeOutbound(ctx); ok {
		t.Fatalf("unexpected tool echo for heartbeat session: %+v", msg)
	}
}

func TestExecuteToolsConcurrently_DoesNotEchoForCronSession(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	testBus := bus.NewMessageBus()
	defer testBus.Close()

	al := &AgentLoop{
		bus:           testBus,
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
		echoToolCalls: true,
	}

	toolCalls := []providers.ToolCall{{
		ID:        "tc1",
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "pwd"},
	}}

	opts := processOptions{SessionKey: "cron-job-42", Channel: "telegram", ChatID: "chat1", TraceID: "trace-test"}
	_ = al.executeToolsConcurrently(context.Background(), toolCalls, 1, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if msg, ok := testBus.SubscribeOutbound(ctx); ok {
		t.Fatalf("unexpected tool echo for cron session: %+v", msg)
	}
}

func TestFormatToolCallSummary_Exec(t *testing.T) {
	tc := providers.ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "echo hello"},
	}
	got := formatToolCallSummary(tc)
	want := "exec echo hello"
	if got != want {
		t.Errorf("formatToolCallSummary() = %q, want %q", got, want)
	}
}

func TestFormatToolCallSummary_EditFile(t *testing.T) {
	tc := providers.ToolCall{
		Name:      "edit_file",
		Arguments: map[string]interface{}{"path": "/tmp/test.go"},
	}
	got := formatToolCallSummary(tc)
	want := "edit_file /tmp/test.go"
	if got != want {
		t.Errorf("formatToolCallSummary() = %q, want %q", got, want)
	}
}

func TestFormatToolCallSummary_WebSearch(t *testing.T) {
	tc := providers.ToolCall{
		Name:      "web_search",
		Arguments: map[string]interface{}{"query": "golang testing"},
	}
	got := formatToolCallSummary(tc)
	want := `web_search "golang testing"`
	if got != want {
		t.Errorf("formatToolCallSummary() = %q, want %q", got, want)
	}
}

func TestFormatToolCallSummary_Truncation(t *testing.T) {
	longCmd := "this is a very long command that should be truncated because it exceeds the maximum length"
	tc := providers.ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": longCmd},
	}
	got := formatToolCallSummary(tc)
	if len(got) > 70 {
		t.Errorf("formatToolCallSummary() = %q (len %d), should be truncated", got, len(got))
	}
}

func TestFormatToolCallSummary_IncludesDescriptionAndArgsSnippet(t *testing.T) {
	tc := providers.ToolCall{
		Name:        "exec",
		Description: "Check repository status",
		Arguments:   map[string]interface{}{"command": "git status -sb"},
	}
	got := formatToolCallSummary(tc)
	want := "exec - Check repository status (git status -sb)"
	if got != want {
		t.Errorf("formatToolCallSummary() = %q, want %q", got, want)
	}
}

func TestFormatToolCallSummary_UsesDescriptionArgumentWhenFieldMissing(t *testing.T) {
	tc := providers.ToolCall{
		Name: "web_fetch",
		Arguments: map[string]interface{}{
			"description": "Fetch release notes",
			"url":         "https://example.com/releases/latest",
		},
	}
	got := formatToolCallSummary(tc)
	want := "web_fetch - Fetch release notes (https://example.com/releases/latest)"
	if got != want {
		t.Errorf("formatToolCallSummary() = %q, want %q", got, want)
	}
}

func TestRedactSensitive_AuthHeader(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"curl -H 'Authorization: Bearer abc123token456'", "[REDACTED]"},
		{"curl -H 'Authorization: Basic dXNlcjpwYXNz'", "[REDACTED]"},
		{"Authorization: Token xyz789", "[REDACTED]"},
	}
	for _, tt := range tests {
		got := redactSensitive(tt.input)
		if !strings.Contains(got, tt.contains) {
			t.Errorf("redactSensitive(%q) = %q, want to contain %q", tt.input, got, tt.contains)
		}
		if strings.Contains(got, "abc123token456") || strings.Contains(got, "dXNlcjpwYXNz") || strings.Contains(got, "xyz789") {
			t.Errorf("redactSensitive(%q) = %q, should not contain secret", tt.input, got)
		}
	}
}

func TestRedactSensitive_APIKeys(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"export API_KEY=sk_live_abc123def456ghi789", "[REDACTED]"},
		{"api_key=supersecretkey123456", "[REDACTED]"},
		{"TOKEN=ghp_xxxxxxxxxxxxxxxxxxxx", "[REDACTED]"},
		{"--header 'X-API-Key: myapikey12345678'", "[REDACTED]"},
	}
	for _, tt := range tests {
		got := redactSensitive(tt.input)
		if !strings.Contains(got, tt.contains) {
			t.Errorf("redactSensitive(%q) = %q, want to contain %q", tt.input, got, tt.contains)
		}
	}
}

func TestRedactSensitive_EnvVars(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"SECRET=mysecretvalue12345678", "[REDACTED]"},
		{"PASSWORD=supersecretpassword", "[REDACTED]"},
		{"export AUTH_TOKEN=mytokenvalue12345678", "[REDACTED]"},
	}
	for _, tt := range tests {
		got := redactSensitive(tt.input)
		if !strings.Contains(got, tt.contains) {
			t.Errorf("redactSensitive(%q) = %q, want to contain %q", tt.input, got, tt.contains)
		}
	}
}

func TestRedactSensitive_SignedURLs(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"https://storage.googleapis.com/bucket/file?X-Goog-Signature=abc123&X-Goog-Date=now", "[REDACTED]"},
		{"https://s3.amazonaws.com/bucket?X-Amz-Signature=xyz789&X-Amz-Date=now", "[REDACTED]"},
		{"https://example.com/download?sig=mysignature123&expires=123", "[REDACTED]"},
		{"https://cdn.example.com/file?Signature=abcdefg123456", "[REDACTED]"},
	}
	for _, tt := range tests {
		got := redactSensitive(tt.input)
		if !strings.Contains(got, tt.contains) {
			t.Errorf("redactSensitive(%q) = %q, want to contain %q", tt.input, got, tt.contains)
		}
	}
}

func TestRedactSensitive_BearerToken(t *testing.T) {
	input := "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	got := redactSensitive(input)
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("redactSensitive(%q) = %q, want to contain [REDACTED]", input, got)
	}
	if strings.Contains(got, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Errorf("redactSensitive(%q) = %q, should not contain token", input, got)
	}
}

func TestRedactSensitive_NoRedactionNeeded(t *testing.T) {
	inputs := []string{
		"ls -la",
		"cat /etc/passwd",
		"echo hello world",
		"npm install lodash",
	}
	for _, input := range inputs {
		got := redactSensitive(input)
		if got != input {
			t.Errorf("redactSensitive(%q) = %q, want unchanged", input, got)
		}
	}
}

func TestExecuteToolsConcurrently_MirrorsMessageToolSendToTargetSession(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	msgTool := tools.NewMessageTool()
	msgTool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return nil
	})
	registry.Register(msgTool)

	al := &AgentLoop{
		bus:           bus.NewMessageBus(),
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
	}
	defer al.bus.Close()

	opts := processOptions{SessionKey: "heartbeat:telegram:chat1", Channel: "telegram", ChatID: "chat1", TraceID: "trace-test"}
	toolCalls := []providers.ToolCall{{
		ID:   "tc1",
		Name: "message",
		Arguments: map[string]interface{}{
			"content": "hello",
			"media":   []interface{}{"/tmp/a.png"},
		},
	}}

	results := al.executeToolsConcurrently(context.Background(), toolCalls, 1, opts)
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}

	history := al.sessions.GetHistory("telegram:chat1")
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].Role != "assistant" {
		t.Fatalf("history role = %q, want assistant", history[0].Role)
	}
	if !strings.Contains(history[0].Content, "hello") {
		t.Fatalf("expected mirrored content to include message text, got %q", history[0].Content)
	}
	if !strings.Contains(history[0].Content, "/tmp/a.png") {
		t.Fatalf("expected mirrored content to include media path, got %q", history[0].Content)
	}
}

func TestExecuteToolsConcurrently_DoesNotMirrorMessageToolWhenSameSession(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	msgTool := tools.NewMessageTool()
	msgTool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return nil
	})
	registry.Register(msgTool)

	al := &AgentLoop{
		bus:           bus.NewMessageBus(),
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
	}
	defer al.bus.Close()

	opts := processOptions{SessionKey: "telegram:chat1", Channel: "telegram", ChatID: "chat1", TraceID: "trace-test"}
	toolCalls := []providers.ToolCall{{
		ID:        "tc1",
		Name:      "message",
		Arguments: map[string]interface{}{"content": "hello"},
	}}

	_ = al.executeToolsConcurrently(context.Background(), toolCalls, 1, opts)

	history := al.sessions.GetHistory("telegram:chat1")
	if len(history) != 0 {
		t.Fatalf("history len = %d, want 0", len(history))
	}
}

func TestExecuteToolsConcurrently_DoesNotMirrorMessageToolOnSendFailure(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	msgTool := tools.NewMessageTool()
	msgTool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return errors.New("send failed")
	})
	registry.Register(msgTool)

	al := &AgentLoop{
		bus:           bus.NewMessageBus(),
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
	}
	defer al.bus.Close()

	opts := processOptions{SessionKey: "heartbeat:telegram:chat1", Channel: "telegram", ChatID: "chat1", TraceID: "trace-test"}
	toolCalls := []providers.ToolCall{{
		ID:        "tc1",
		Name:      "message",
		Arguments: map[string]interface{}{"content": "hello"},
	}}

	_ = al.executeToolsConcurrently(context.Background(), toolCalls, 1, opts)

	history := al.sessions.GetHistory("telegram:chat1")
	if len(history) != 0 {
		t.Fatalf("history len = %d, want 0", len(history))
	}
}

func TestExecuteToolsConcurrently_MirrorsMessageToolSend_UsesAliasArgs(t *testing.T) {
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	msgTool := tools.NewMessageTool()
	msgTool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		return nil
	})
	registry.Register(msgTool)

	al := &AgentLoop{
		bus:           bus.NewMessageBus(),
		workspace:     tmpDir,
		model:         "test-model",
		chatOptions:   providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		maxIterations: 5,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
	}
	defer al.bus.Close()

	// Intentionally set fallback ChatID to something else; the tool call uses alias
	// args (text/target) which should be honored by mirroring.
	opts := processOptions{SessionKey: "heartbeat:telegram:chatX", Channel: "telegram", ChatID: "chatX", TraceID: "trace-test"}
	toolCalls := []providers.ToolCall{{
		ID:   "tc1",
		Name: "message",
		Arguments: map[string]interface{}{
			"text":    "hello",
			"channel": "telegram",
			"target":  "chat1",
		},
	}}

	_ = al.executeToolsConcurrently(context.Background(), toolCalls, 1, opts)

	history := al.sessions.GetHistory("telegram:chat1")
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if !strings.Contains(history[0].Content, "hello") {
		t.Fatalf("expected mirrored content to include message text, got %q", history[0].Content)
	}
}

func TestFormatToolCallSummary_RedactsSecrets(t *testing.T) {
	tc := providers.ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "curl -H 'Authorization: Bearer supersecret123' https://api.example.com"},
	}
	got := formatToolCallSummary(tc)
	if strings.Contains(got, "supersecret123") {
		t.Errorf("formatToolCallSummary() = %q, should not contain secret", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("formatToolCallSummary() = %q, should contain [REDACTED]", got)
	}
}
