package agent

import (
	"context"
	"path/filepath"
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
