package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// mockProvider is a test LLM provider that returns pre-configured responses.
type mockProvider struct {
	mu    sync.Mutex
	calls []mockProviderCall
	// responses is a queue; each Chat() call pops the first element.
	responses []mockResponse
}

type mockProviderCall struct {
	Messages []providers.Message
	Tools    []providers.ToolDefinition
}

type mockResponse struct {
	Content   string
	ToolCalls []providers.ToolCall
	Usage     *providers.UsageInfo
	Err       error
}

type blockingProvider struct{}

func (p *blockingProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingProvider) GetDefaultModel() string { return "test-model" }

type interruptibleProvider struct {
	canceledCalls atomic.Int32
}

func (p *interruptibleProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	select {
	case <-ctx.Done():
		p.canceledCalls.Add(1)
		return nil, ctx.Err()
	default:
	}

	lastUser := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = messages[i].Content
			break
		}
	}

	if strings.Contains(lastUser, "first message") {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{ID: "wait-1", Name: "wait_tool", Arguments: map[string]interface{}{}}},
		}, nil
	}

	if strings.Contains(lastUser, "second message") {
		return &providers.LLMResponse{Content: "handled second"}, nil
	}

	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *interruptibleProvider) GetDefaultModel() string { return "test-model" }

type waitTool struct {
	started chan struct{}
	once    sync.Once
}

func (t *waitTool) Name() string        { return "wait_tool" }
func (t *waitTool) Description() string { return "waits until cancelled" }
func (t *waitTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func (t *waitTool) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	t.once.Do(func() {
		close(t.started)
	})
	<-ctx.Done()
	return "", ctx.Err()
}

func (m *mockProvider) Chat(_ context.Context, messages []providers.Message, tdefs []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, mockProviderCall{
		Messages: messages,
		Tools:    tdefs,
	})

	if len(m.responses) == 0 {
		return &providers.LLMResponse{Content: "no more responses"}, nil
	}

	resp := m.responses[0]
	m.responses = m.responses[1:]

	if resp.Err != nil {
		return nil, resp.Err
	}
	return &providers.LLMResponse{
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
		Usage:     resp.Usage,
	}, nil
}

func (m *mockProvider) GetDefaultModel() string { return "test-model" }

func (m *mockProvider) getCalls() []mockProviderCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockProviderCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// noopTool is a minimal tool for testing that returns a fixed result.
type noopTool struct {
	name   string
	result string
}

func (t *noopTool) Name() string        { return t.name }
func (t *noopTool) Description() string { return "test tool" }
func (t *noopTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *noopTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return t.result, nil
}

// newTestAgentLoop creates a minimal AgentLoop for testing with the given
// provider, maxIterations, and pre-registered tools.
func newTestAgentLoop(t *testing.T, provider providers.LLMProvider, maxIter int, testTools []tools.Tool) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	registry := tools.NewToolRegistry()
	for _, tool := range testTools {
		registry.Register(tool)
	}
	contextBuilder := NewContextBuilder(tmpDir)
	contextBuilder.SetToolsRegistry(registry)

	return &AgentLoop{
		bus:            bus.NewMessageBus(),
		provider:       provider,
		workspace:      tmpDir,
		model:          "test-model",
		contextWindow:  128000,
		chatOptions:    providers.ChatOptions{MaxTokens: 8192, Temperature: 0.7},
		compactOptions: providers.ChatOptions{MaxTokens: 1024, Temperature: 0.3},
		maxIterations:  maxIter,
		sessions:       session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		contextBuilder: contextBuilder,
		tools:          registry,
		summarizing:    sync.Map{},
	}
}

func TestRun_InterruptsActiveSessionOnNewUserMessage(t *testing.T) {
	provider := &interruptibleProvider{}
	tool := &waitTool{started: make(chan struct{})}
	al := newTestAgentLoop(t, provider, 5, []tools.Tool{tool})

	runCtx, runCancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- al.Run(runCtx)
	}()

	cleanup := func() {
		al.Stop()
		runCancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Fatal("agent loop did not stop")
		}
		al.bus.Close()
	}
	defer cleanup()

	al.bus.PublishInbound(bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user-1",
		ChatID:     "chat-1",
		Content:    "first message",
		SessionKey: "telegram:chat-1",
	})

	select {
	case <-tool.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first tool call did not start")
	}

	al.bus.PublishInbound(bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user-1",
		ChatID:     "chat-1",
		Content:    "second message",
		SessionKey: "telegram:chat-1",
	})

	outCtx, outCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer outCancel()
	out, ok := al.bus.SubscribeOutbound(outCtx)
	if !ok {
		t.Fatal("expected outbound response for second message")
	}
	if out.Content != "handled second" {
		t.Fatalf("outbound content = %q, want %q", out.Content, "handled second")
	}

	extraCtx, extraCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer extraCancel()
	if extra, ok := al.bus.SubscribeOutbound(extraCtx); ok {
		t.Fatalf("unexpected extra outbound message: %+v", extra)
	}

	if provider.canceledCalls.Load() == 0 {
		t.Fatal("expected at least one canceled provider call from interrupted run")
	}
}

func TestRunLLMIteration_FinalSummaryOnMaxIterations(t *testing.T) {
	// Provider always returns a tool call, except the very last call
	// (which should be made with no tools) returns a summary.
	prov := &mockProvider{
		responses: []mockResponse{
			// Iteration 1: return a tool call
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "noop", Arguments: map[string]interface{}{}}}},
			// Iteration 2: return a tool call
			{ToolCalls: []providers.ToolCall{{ID: "tc2", Name: "noop", Arguments: map[string]interface{}{}}}},
			// Final summary call (no tools provided): return text
			{Content: "Here's what I did so far and what remains."},
		},
	}

	al := newTestAgentLoop(t, prov, 2, []tools.Tool{
		&noopTool{name: "noop", result: "ok"},
	})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "You are a test bot."},
		{Role: "user", Content: "Do stuff"},
	}
	opts := processOptions{
		SessionKey: "test",
		Channel:    "telegram",
		ChatID:     "chat1",
	}

	content, iterations, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have used all iterations
	if iterations != 2 {
		t.Errorf("iterations = %d, want 2", iterations)
	}

	// Should have gotten the summary, not an empty string
	if content == "" {
		t.Fatal("expected final summary content, got empty string")
	}
	if content != "Here's what I did so far and what remains." {
		t.Errorf("content = %q, want summary text", content)
	}

	// Verify a 3rd call was made (the summary call) with zero tools
	calls := prov.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 provider calls (2 iterations + 1 summary), got %d", len(calls))
	}
	finalCall := calls[2]
	if len(finalCall.Tools) != 0 {
		t.Errorf("final summary call should have 0 tools, got %d", len(finalCall.Tools))
	}
}

func TestRunLLMIteration_NoSummaryCallWhenNotExhausted(t *testing.T) {
	// Provider returns a tool call on iteration 1, then a direct answer on iteration 2.
	prov := &mockProvider{
		responses: []mockResponse{
			// Iteration 1: tool call
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "noop", Arguments: map[string]interface{}{}}}},
			// Iteration 2: direct answer (no tool calls)
			{Content: "Here is your answer."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{
		&noopTool{name: "noop", result: "ok"},
	})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "You are a test bot."},
		{Role: "user", Content: "Do stuff"},
	}
	opts := processOptions{
		SessionKey: "test",
		Channel:    "telegram",
		ChatID:     "chat1",
	}

	content, iterations, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if iterations != 2 {
		t.Errorf("iterations = %d, want 2", iterations)
	}
	if content != "Here is your answer." {
		t.Errorf("content = %q, want %q", content, "Here is your answer.")
	}

	// Should have made exactly 2 calls (no extra summary call)
	calls := prov.getCalls()
	if len(calls) != 2 {
		t.Errorf("expected 2 provider calls, got %d", len(calls))
	}
}

func TestRunLLMIteration_SummaryCallIncludesHint(t *testing.T) {
	// Verify that the final summary call includes a message hinting
	// the LLM to summarize progress.
	prov := &mockProvider{
		responses: []mockResponse{
			// Iteration 1: tool call
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "noop", Arguments: map[string]interface{}{}}}},
			// Summary call response
			{Content: "Summary of progress."},
		},
	}

	al := newTestAgentLoop(t, prov, 1, []tools.Tool{
		&noopTool{name: "noop", result: "ok"},
	})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "You are a test bot."},
		{Role: "user", Content: "Do stuff"},
	}
	opts := processOptions{
		SessionKey: "test",
		Channel:    "telegram",
		ChatID:     "chat1",
	}

	_, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The final call should have a user message hinting to summarize
	calls := prov.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(calls))
	}
	finalMessages := calls[len(calls)-1].Messages
	lastMsg := finalMessages[len(finalMessages)-1]
	if lastMsg.Role != "user" {
		t.Errorf("last message role = %q, want %q", lastMsg.Role, "user")
	}
	// Should mention iteration limit / summarize
	if !containsStr(lastMsg.Content, "limit") && !containsStr(lastMsg.Content, "summar") {
		t.Errorf("summary hint message %q should mention limit or summarize", lastMsg.Content)
	}
}

func TestRunAgentLoop_SummarizesBasedOnReportedPromptTokens(t *testing.T) {
	// The session history is short (so the char/4 heuristic would NOT trigger
	// compaction), but the provider reports a high prompt token count.
	prov := &mockProvider{responses: []mockResponse{
		{Content: "ok", Usage: &providers.UsageInfo{PromptTokens: 80}},
		{Content: "summary"},
	}}

	al := newTestAgentLoop(t, prov, 1, nil)
	al.contextWindow = 100 // threshold = 75
	defer al.bus.Close()

	sessionKey := "test"
	// Seed with 4 small messages so summary has something to compact.
	al.sessions.AddMessage(sessionKey, "user", "a")
	al.sessions.AddMessage(sessionKey, "assistant", "b")
	al.sessions.AddMessage(sessionKey, "user", "c")
	al.sessions.AddMessage(sessionKey, "assistant", "d")

	_, err := al.runAgentLoop(context.Background(), processOptions{
		SessionKey:      sessionKey,
		Channel:         "telegram",
		ChatID:          "chat1",
		UserMessage:     "next",
		DefaultResponse: "default",
		EnableSummary:   true,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop() error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if al.sessions.GetSummary(sessionKey) != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := al.sessions.GetSummary(sessionKey); got != "summary" {
		t.Fatalf("summary = %q, want %q", got, "summary")
	}
	if got := len(al.sessions.GetHistory(sessionKey)); got != 4 {
		t.Fatalf("history len = %d, want 4 after compaction", got)
	}
}

func TestRunAgentLoop_SuppressesDefaultResponseAfterMessageTool(t *testing.T) {
	defaultResp := "I've completed processing but have no response to give."
	prov := &mockProvider{responses: []mockResponse{
		{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "message", Arguments: map[string]interface{}{"content": "hi"}}}},
		{Content: ""},
	}}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{
		&noopTool{name: "message", result: "Message sent to telegram:chat1"},
	})
	defer al.bus.Close()

	got, err := al.runAgentLoop(context.Background(), processOptions{
		SessionKey:      "telegram:chat1",
		Channel:         "telegram",
		ChatID:          "chat1",
		UserMessage:     "do it",
		DefaultResponse: defaultResp,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop() error: %v", err)
	}
	if got != "" {
		t.Fatalf("response = %q, want empty string (already delivered via message tool)", got)
	}

	history := al.sessions.GetHistory("telegram:chat1")
	for _, msg := range history {
		if msg.Role == "assistant" && msg.Content == defaultResp {
			t.Fatalf("session history should not include default response after message tool delivery")
		}
	}
}

func TestRunAgentLoop_SuppressesLLMFillerDefaultResponseAfterMessageTool(t *testing.T) {
	defaultResp := "I've completed processing but have no response to give."
	prov := &mockProvider{responses: []mockResponse{
		{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "message", Arguments: map[string]interface{}{"content": "hi"}}}},
		{Content: defaultResp},
	}}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{
		&noopTool{name: "message", result: "Message sent to telegram:chat1"},
	})
	defer al.bus.Close()

	got, err := al.runAgentLoop(context.Background(), processOptions{
		SessionKey:      "telegram:chat1",
		Channel:         "telegram",
		ChatID:          "chat1",
		UserMessage:     "do it",
		DefaultResponse: defaultResp,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop() error: %v", err)
	}
	if got != "" {
		t.Fatalf("response = %q, want empty string (suppress filler)", got)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- parseMemoryLines tests ---

func TestParseMemoryLines_ValidLines(t *testing.T) {
	input := `MEMORY(preference): User likes dark mode
MEMORY(fact): User's name is Alice
MEMORY(event): Deployed v2.0 today`

	got := parseMemoryLines(input)
	if len(got) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(got))
	}

	want := []parsedMemory{
		{Category: "preference", Content: "User likes dark mode"},
		{Category: "fact", Content: "User's name is Alice"},
		{Category: "event", Content: "Deployed v2.0 today"},
	}
	for i, w := range want {
		if got[i].Category != w.Category {
			t.Errorf("[%d] category = %q, want %q", i, got[i].Category, w.Category)
		}
		if got[i].Content != w.Content {
			t.Errorf("[%d] content = %q, want %q", i, got[i].Content, w.Content)
		}
	}
}

func TestParseMemoryLines_IgnoresNonMemoryLines(t *testing.T) {
	input := `Here are the extracted memories:

MEMORY(preference): User prefers Go
Some extra commentary here.
MEMORY(fact): Project uses SQLite

That's all I found.
NONE`

	got := parseMemoryLines(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 memories, got %d: %+v", len(got), got)
	}
	if got[0].Content != "User prefers Go" {
		t.Errorf("[0] content = %q", got[0].Content)
	}
	if got[1].Content != "Project uses SQLite" {
		t.Errorf("[1] content = %q", got[1].Content)
	}
}

func TestParseMemoryLines_EmptyAndNone(t *testing.T) {
	for _, input := range []string{"", "NONE", "No notable memories.", "   "} {
		got := parseMemoryLines(input)
		if len(got) != 0 {
			t.Errorf("input %q: expected 0 memories, got %d", input, len(got))
		}
	}
}

func TestParseMemoryLines_SkipsEmptyContent(t *testing.T) {
	input := `MEMORY(preference):
MEMORY(fact): Valid content here`

	got := parseMemoryLines(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 memory (skip empty content), got %d", len(got))
	}
	if got[0].Content != "Valid content here" {
		t.Errorf("content = %q", got[0].Content)
	}
}

// --- extractAndStoreMemories integration test ---

func TestExtractAndStoreMemories_StoresExtractedMemories(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{Content: "MEMORY(preference): User likes cats\nMEMORY(fact): User lives in Tokyo"},
		},
	}

	al := newTestAgentLoop(t, prov, 5, nil)
	defer al.bus.Close()

	// Set up a real memory store
	memDB, err := newTestMemoryStore(t)
	if err != nil {
		t.Fatalf("failed to create test memory store: %v", err)
	}
	al.memoryStore = memDB

	messages := []providers.Message{
		{Role: "user", Content: "I like cats. I live in Tokyo."},
		{Role: "assistant", Content: "Noted! You like cats and live in Tokyo."},
	}

	al.extractAndStoreMemories(context.Background(), messages)

	// Verify memories were stored
	results, err := memDB.Search("cats", 5, "")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected memory about cats, found none")
	}

	results, err = memDB.Search("Tokyo", 5, "")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected memory about Tokyo, found none")
	}
}

func TestExtractAndStoreMemories_NilMemoryStoreIsNoop(t *testing.T) {
	prov := &mockProvider{}
	al := newTestAgentLoop(t, prov, 5, nil)
	defer al.bus.Close()
	// al.memoryStore is nil — should not panic or call the provider
	al.extractAndStoreMemories(context.Background(), []providers.Message{
		{Role: "user", Content: "hello"},
	})

	calls := prov.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected 0 provider calls when memoryStore is nil, got %d", len(calls))
	}
}

func TestExtractAndStoreMemories_NoneResponse(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{Content: "NONE"},
		},
	}

	al := newTestAgentLoop(t, prov, 5, nil)
	defer al.bus.Close()

	memDB, err := newTestMemoryStore(t)
	if err != nil {
		t.Fatalf("failed to create test memory store: %v", err)
	}
	al.memoryStore = memDB

	messages := []providers.Message{
		{Role: "user", Content: "What time is it?"},
		{Role: "assistant", Content: "It's 3pm."},
	}

	al.extractAndStoreMemories(context.Background(), messages)

	// Should not store anything
	results, err := memDB.Search("time", 5, "")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 stored memories for trivial conversation, got %d", len(results))
	}
}

// newTestMemoryStore creates a temporary in-memory SQLite memory store for testing.
func newTestMemoryStore(t *testing.T) (*memory.MemoryStore, error) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "memory", "test.db")
	return memory.NewMemoryStore(dbPath, tmpDir)
}

// --- Parallel tool execution tests ---

// slowTool is a tool that sleeps for a configurable duration and tracks execution.
type slowTool struct {
	name     string
	delay    time.Duration
	result   string
	started  atomic.Int32
	finished atomic.Int32
}

type timeoutAwareTool struct {
	name  string
	delay time.Duration
}

func (t *timeoutAwareTool) Name() string        { return t.name }
func (t *timeoutAwareTool) Description() string { return "timeout-aware test tool" }
func (t *timeoutAwareTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *timeoutAwareTool) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	select {
	case <-time.After(t.delay):
		return "done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type concurrencyTracker struct {
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

type trackedTool struct {
	name    string
	delay   time.Duration
	tracker *concurrencyTracker
}

func (t *trackedTool) Name() string        { return t.name }
func (t *trackedTool) Description() string { return "tracked test tool" }
func (t *trackedTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *trackedTool) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	current := t.tracker.inFlight.Add(1)
	for {
		prev := t.tracker.maxSeen.Load()
		if current <= prev || t.tracker.maxSeen.CompareAndSwap(prev, current) {
			break
		}
	}
	defer t.tracker.inFlight.Add(-1)

	select {
	case <-time.After(t.delay):
		return t.name + "_ok", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// panicTool simulates a buggy tool implementation that panics during Execute.
type panicTool struct {
	name string
}

func (t *panicTool) Name() string        { return t.name }
func (t *panicTool) Description() string { return "panic test tool" }
func (t *panicTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *panicTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	panic("boom")
}

func (t *slowTool) Name() string        { return t.name }
func (t *slowTool) Description() string { return "slow test tool" }
func (t *slowTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *slowTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	t.started.Add(1)
	time.Sleep(t.delay)
	t.finished.Add(1)
	return t.result, nil
}

func TestRunLLMIteration_ParallelToolExecution(t *testing.T) {
	// LLM returns 3 tool calls at once. Each tool takes 100ms.
	// Sequential = ~300ms, parallel = ~100ms.
	toolA := &slowTool{name: "tool_a", delay: 100 * time.Millisecond, result: "a_done"}
	toolB := &slowTool{name: "tool_b", delay: 100 * time.Millisecond, result: "b_done"}
	toolC := &slowTool{name: "tool_c", delay: 100 * time.Millisecond, result: "c_done"}

	prov := &mockProvider{
		responses: []mockResponse{
			// Iteration 1: 3 parallel tool calls
			{ToolCalls: []providers.ToolCall{
				{ID: "tc1", Name: "tool_a", Arguments: map[string]interface{}{}},
				{ID: "tc2", Name: "tool_b", Arguments: map[string]interface{}{}},
				{ID: "tc3", Name: "tool_c", Arguments: map[string]interface{}{}},
			}},
			// Iteration 2: direct answer
			{Content: "All done."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{toolA, toolB, toolC})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "run all three"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	start := time.Now()
	content, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "All done." {
		t.Errorf("content = %q, want %q", content, "All done.")
	}

	// All tools should have executed
	if toolA.finished.Load() != 1 || toolB.finished.Load() != 1 || toolC.finished.Load() != 1 {
		t.Errorf("not all tools finished: a=%d b=%d c=%d",
			toolA.finished.Load(), toolB.finished.Load(), toolC.finished.Load())
	}

	// Should be significantly faster than sequential (300ms).
	// Allow 200ms to account for test overhead, but must be under 280ms.
	if elapsed > 280*time.Millisecond {
		t.Errorf("parallel execution too slow: %v (sequential would be ~300ms)", elapsed)
	}
}

func TestRunLLMIteration_ParallelToolResults_CorrectOrder(t *testing.T) {
	// Tool B finishes faster than Tool A, but results should be in call order.
	toolA := &slowTool{name: "tool_a", delay: 80 * time.Millisecond, result: "result_a"}
	toolB := &slowTool{name: "tool_b", delay: 10 * time.Millisecond, result: "result_b"}

	prov := &mockProvider{
		responses: []mockResponse{
			{ToolCalls: []providers.ToolCall{
				{ID: "tc1", Name: "tool_a", Arguments: map[string]interface{}{}},
				{ID: "tc2", Name: "tool_b", Arguments: map[string]interface{}{}},
			}},
			{Content: "Done."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{toolA, toolB})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "run both"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	_, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the second LLM call received tool results in the right order
	calls := prov.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(calls))
	}

	// The second call's messages should contain tool results.
	// Find tool result messages.
	secondCallMsgs := calls[1].Messages
	var toolResults []providers.Message
	for _, m := range secondCallMsgs {
		if m.Role == "tool" {
			toolResults = append(toolResults, m)
		}
	}

	if len(toolResults) != 2 {
		t.Fatalf("expected 2 tool result messages, got %d", len(toolResults))
	}
	if toolResults[0].ToolCallID != "tc1" {
		t.Errorf("first tool result ID = %q, want %q", toolResults[0].ToolCallID, "tc1")
	}
	if toolResults[0].Content != "result_a" {
		t.Errorf("first tool result content = %q, want %q", toolResults[0].Content, "result_a")
	}
	if toolResults[1].ToolCallID != "tc2" {
		t.Errorf("second tool result ID = %q, want %q", toolResults[1].ToolCallID, "tc2")
	}
	if toolResults[1].Content != "result_b" {
		t.Errorf("second tool result content = %q, want %q", toolResults[1].Content, "result_b")
	}
}

func TestRunLLMIteration_ParallelToolNoLeakedToolNames(t *testing.T) {
	// Verify that tool names are NOT sent to the bus — they should only
	// appear in logs, not in user-facing messages.
	toolA := &slowTool{name: "tool_a", delay: 30 * time.Millisecond, result: "a"}
	toolB := &slowTool{name: "tool_b", delay: 30 * time.Millisecond, result: "b"}

	prov := &mockProvider{
		responses: []mockResponse{
			{ToolCalls: []providers.ToolCall{
				{ID: "tc1", Name: "tool_a", Arguments: map[string]interface{}{}},
				{ID: "tc2", Name: "tool_b", Arguments: map[string]interface{}{}},
			}},
			{Content: "Done."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{toolA, toolB})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "go"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	_, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain outbound messages
	var outbound []bus.OutboundMessage
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer drainCancel()
	for {
		msg, ok := al.bus.SubscribeOutbound(drainCtx)
		if !ok {
			break
		}
		outbound = append(outbound, msg)
	}

	// No outbound message should contain tool names
	for _, msg := range outbound {
		if containsStr(msg.Content, "tool_a") || containsStr(msg.Content, "tool_b") {
			t.Errorf("outbound message leaked tool name to user: %q", msg.Content)
		}
	}
}

func TestRunLLMIteration_ParallelToolPanic_Recovered(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{ToolCalls: []providers.ToolCall{
				{ID: "tc1", Name: "panic_tool", Arguments: map[string]interface{}{}},
			}},
			{Content: "Recovered and continued."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{&panicTool{name: "panic_tool"}})
	defer al.bus.Close()

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "run panic tool"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	content, _, _, _, err := al.runLLMIteration(ctx, messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "Recovered and continued." {
		t.Errorf("content = %q, want %q", content, "Recovered and continued.")
	}

	// Ensure the second provider call received a tool error result rather than crashing.
	calls := prov.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(calls))
	}
	foundToolResult := false
	for _, msg := range calls[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "tc1" {
			foundToolResult = true
			if !containsStr(msg.Content, "panic") {
				t.Errorf("tool result should mention panic, got %q", msg.Content)
			}
		}
	}
	if !foundToolResult {
		t.Fatal("expected tool result message for panicking tool")
	}
}

func TestRunLLMIteration_LLMCallTimeout(t *testing.T) {
	al := newTestAgentLoop(t, &blockingProvider{}, 3, nil)
	defer al.bus.Close()
	al.llmTimeout = 50 * time.Millisecond

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "hello"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	start := time.Now()
	_, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from provider call")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got: %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestRunLLMIteration_ToolTimeoutProducesErrorResult(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "slow_timeout", Arguments: map[string]interface{}{}}}},
			{Content: "done"},
		},
	}

	tool := &timeoutAwareTool{name: "slow_timeout", delay: 300 * time.Millisecond}
	al := newTestAgentLoop(t, prov, 5, []tools.Tool{tool})
	defer al.bus.Close()
	al.toolTimeout = 50 * time.Millisecond

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "run timeout tool"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	start := time.Now()
	content, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "done" {
		t.Fatalf("content = %q, want %q", content, "done")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("tool timeout not applied; elapsed=%v", elapsed)
	}

	calls := prov.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(calls))
	}

	foundToolMsg := false
	for _, msg := range calls[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "tc1" {
			foundToolMsg = true
			if !containsStr(msg.Content, "context deadline") {
				t.Fatalf("expected timeout content, got %q", msg.Content)
			}
		}
	}
	if !foundToolMsg {
		t.Fatal("expected tool result message for timed out tool")
	}
}

func TestRunLLMIteration_RespectsMaxParallelTools(t *testing.T) {
	tracker := &concurrencyTracker{}
	t1 := &trackedTool{name: "tracked_1", delay: 120 * time.Millisecond, tracker: tracker}
	t2 := &trackedTool{name: "tracked_2", delay: 120 * time.Millisecond, tracker: tracker}
	t3 := &trackedTool{name: "tracked_3", delay: 120 * time.Millisecond, tracker: tracker}
	t4 := &trackedTool{name: "tracked_4", delay: 120 * time.Millisecond, tracker: tracker}

	prov := &mockProvider{
		responses: []mockResponse{
			{ToolCalls: []providers.ToolCall{
				{ID: "tc1", Name: "tracked_1", Arguments: map[string]interface{}{}},
				{ID: "tc2", Name: "tracked_2", Arguments: map[string]interface{}{}},
				{ID: "tc3", Name: "tracked_3", Arguments: map[string]interface{}{}},
				{ID: "tc4", Name: "tracked_4", Arguments: map[string]interface{}{}},
			}},
			{Content: "done"},
		},
	}

	al := newTestAgentLoop(t, prov, 5, []tools.Tool{t1, t2, t3, t4})
	defer al.bus.Close()
	al.maxParallelTools = 2

	messages := []providers.Message{
		{Role: "system", Content: "test"},
		{Role: "user", Content: "run all"},
	}
	opts := processOptions{SessionKey: "test", Channel: "telegram", ChatID: "chat1"}

	content, _, _, _, err := al.runLLMIteration(context.Background(), messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "done" {
		t.Fatalf("content = %q, want %q", content, "done")
	}

	if got := tracker.maxSeen.Load(); got > 2 {
		t.Fatalf("max concurrent tools = %d, want <= 2", got)
	}
}

func TestProcessSystemMessage_SubagentProgress_IsInternal(t *testing.T) {
	// Subagent progress updates should be stored as internal notes and
	// must not produce user-facing outbound messages.
	al := newTestAgentLoop(t, &mockProvider{responses: []mockResponse{{Content: "unused"}}}, 1, nil)
	defer al.bus.Close()

	msg := bus.InboundMessage{
		Channel:  "system",
		SenderID: "subagent:subagent-1",
		ChatID:   "telegram:chat1",
		Content:  "step 1",
		Metadata: map[string]string{"subagent_event": "progress"},
	}

	resp, err := al.processSystemMessage(context.Background(), msg, "trace-test-1")
	if err != nil {
		t.Fatalf("processSystemMessage error: %v", err)
	}
	if resp != "" {
		t.Errorf("response = %q, want empty", resp)
	}

	// No outbound user message should be published
	outCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := al.bus.SubscribeOutbound(outCtx); ok {
		t.Fatal("unexpected outbound message for subagent progress event")
	}

	// Internal note should be stored in session history
	history := al.sessions.GetHistory("telegram:chat1")
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].Role != "assistant" {
		t.Errorf("history role = %q, want %q", history[0].Role, "assistant")
	}
	if !containsStr(history[0].Content, "Internal") {
		t.Errorf("history content should look like internal note; got %q", history[0].Content)
	}
}

func TestProcessSystemMessage_SubagentCancelled_IsInternal(t *testing.T) {
	al := newTestAgentLoop(t, &mockProvider{responses: []mockResponse{{Content: "unused"}}}, 1, nil)
	defer al.bus.Close()

	msg := bus.InboundMessage{
		Channel:  "system",
		SenderID: "subagent:subagent-2",
		ChatID:   "telegram:chat2",
		Content:  "Task was cancelled",
		Metadata: map[string]string{"subagent_event": "cancelled"},
	}

	resp, err := al.processSystemMessage(context.Background(), msg, "trace-test-2")
	if err != nil {
		t.Fatalf("processSystemMessage error: %v", err)
	}
	if resp != "" {
		t.Errorf("response = %q, want empty", resp)
	}

	// No outbound user message should be published
	outCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := al.bus.SubscribeOutbound(outCtx); ok {
		t.Fatal("unexpected outbound message for subagent cancelled event")
	}
}

func TestProcessSystemMessage_HeartbeatSubagentComplete_IsInternal(t *testing.T) {
	// Subagent completion events that originate from a heartbeat session should be
	// stored internally in the heartbeat session transcript and must not produce
	// user-facing outbound messages.
	al := newTestAgentLoop(t, &mockProvider{responses: []mockResponse{{Content: "unused"}}}, 1, nil)
	defer al.bus.Close()

	msg := bus.InboundMessage{
		Channel:  "system",
		SenderID: "subagent:subagent-9",
		ChatID:   "heartbeat:telegram:chat1",
		Content:  "Task complete",
		Metadata: map[string]string{"subagent_event": "complete"},
	}

	resp, err := al.processSystemMessage(context.Background(), msg, "trace-test-hb")
	if err != nil {
		t.Fatalf("processSystemMessage error: %v", err)
	}
	if resp != "" {
		t.Errorf("response = %q, want empty", resp)
	}

	// No outbound message should be published
	outCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := al.bus.SubscribeOutbound(outCtx); ok {
		t.Fatal("unexpected outbound message for heartbeat subagent completion")
	}

	// Internal note should be stored on the heartbeat session key
	history := al.sessions.GetHistory("heartbeat:telegram:chat1")
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].Role != "assistant" {
		t.Errorf("history role = %q, want %q", history[0].Role, "assistant")
	}
	if !containsStr(history[0].Content, "Internal") {
		t.Errorf("history content should look like internal note; got %q", history[0].Content)
	}
}

func TestMessageBudgetFromDefaults_AppliesOverrides(t *testing.T) {
	d := config.AgentDefaults{
		MaxTokens:                  8192,
		RequestMaxMessages:         123,
		RequestMaxTotalChars:       4567,
		RequestMaxMessageChars:     890,
		RequestMaxToolMessageChars: 321,
	}
	b := messageBudgetFromDefaults(d)

	if b.MaxMessages != 123 {
		t.Fatalf("MaxMessages = %d, want 123", b.MaxMessages)
	}
	if b.MaxTotalChars != 4567 {
		t.Fatalf("MaxTotalChars = %d, want 4567", b.MaxTotalChars)
	}
	if b.MaxMessageChars != 890 {
		t.Fatalf("MaxMessageChars = %d, want 890", b.MaxMessageChars)
	}
	if b.MaxToolMessageChars != 321 {
		t.Fatalf("MaxToolMessageChars = %d, want 321", b.MaxToolMessageChars)
	}
}

func TestMessageBudgetFromDefaults_DefaultsDisabled(t *testing.T) {
	d := config.AgentDefaults{MaxTokens: 8192}
	b := messageBudgetFromDefaults(d)

	if b.Enabled() {
		t.Fatalf("expected request budget disabled by default, got %+v", b)
	}
}

func TestNewAgentLoop_PropagatesAnthropicCacheDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.AnthropicCache = true
	cfg.Agents.Defaults.AnthropicCacheTTL = "1h"

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	if !al.chatOptions.AnthropicCache {
		t.Fatal("chatOptions.AnthropicCache = false, want true")
	}
	if al.chatOptions.AnthropicCacheTTL != "1h" {
		t.Fatalf("chatOptions.AnthropicCacheTTL = %q, want 1h", al.chatOptions.AnthropicCacheTTL)
	}
	if !al.compactOptions.AnthropicCache {
		t.Fatal("compactOptions.AnthropicCache = false, want true")
	}
	if al.compactOptions.AnthropicCacheTTL != "1h" {
		t.Fatalf("compactOptions.AnthropicCacheTTL = %q, want 1h", al.compactOptions.AnthropicCacheTTL)
	}
}

func TestNewAgentLoop_ToolSafeguardsDisabled_DisablesPolicyAndGuards(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Policy.Enabled = true
	cfg.Tools.Policy.SafeMode = true
	cfg.Tools.Policy.Deny = []string{"exec", "read_file"}
	cfg.Tools.Safeguards.Disabled = true

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	if al.unsafeGate != nil {
		t.Fatal("expected unsafe gate to be disabled")
	}

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside-ok"), 0644); err != nil {
		t.Fatalf("setup outside file: %v", err)
	}

	readResult, err := al.tools.ExecuteWithContext(context.Background(), "read_file", map[string]interface{}{
		"path": outsideFile,
	}, "", "")
	if err != nil {
		t.Fatalf("read_file should be unrestricted when safeguards are disabled, got err: %v", err)
	}
	if readResult != "outside-ok" {
		t.Fatalf("read_file result = %q, want %q", readResult, "outside-ok")
	}

	execResult, err := al.tools.ExecuteWithContext(context.Background(), "exec", map[string]interface{}{
		"command": "echo rm -rf /",
	}, "", "")
	if err != nil {
		t.Fatalf("exec should not be blocked by policy/guard when safeguards are disabled, got err: %v", err)
	}
	if strings.Contains(execResult, "Command blocked by safety guard") {
		t.Fatalf("expected exec guard disabled, got %q", execResult)
	}
	if !strings.Contains(execResult, "rm -rf /") {
		t.Fatalf("expected command output, got %q", execResult)
	}

	startup := al.GetStartupInfo()
	toolsInfo, ok := startup["tools"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tools info map in startup info")
	}
	disabled, ok := toolsInfo["safeguards_disabled"].(bool)
	if !ok {
		t.Fatalf("expected safeguards_disabled bool in startup info")
	}
	if !disabled {
		t.Fatalf("expected safeguards_disabled=true in startup info")
	}
}

func TestCompactSession_SynchronousHistoryTruncationAndSummaryUpdate(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{Content: "Summary of earlier conversation: user asked about topic A and B."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, nil)
	defer al.bus.Close()

	sessionKey := "test-compact-sync"

	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "Message 1"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", Content: "Response 1"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "Message 2"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", Content: "Response 2"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "Message 3"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", Content: "Response 3"})

	historyBefore := al.sessions.GetHistory(sessionKey)
	if len(historyBefore) != 6 {
		t.Fatalf("expected 6 messages before compaction, got %d", len(historyBefore))
	}

	err := al.CompactSession(sessionKey, "soft")
	if err != nil {
		t.Fatalf("CompactSession failed: %v", err)
	}

	historyAfter := al.sessions.GetHistory(sessionKey)
	if len(historyAfter) >= len(historyBefore) {
		t.Errorf("history should be truncated after compaction, but got len=%d before, len=%d after", len(historyBefore), len(historyAfter))
	}

	if len(historyAfter) > 4 {
		t.Errorf("soft mode should keep last 4 messages, got %d", len(historyAfter))
	}

	summary := al.sessions.GetSummary(sessionKey)
	if summary == "" {
		t.Error("summary should be set after compaction")
	}

	if !strings.Contains(summary, "Summary of earlier conversation") {
		t.Errorf("summary should contain provider response, got %q", summary)
	}
}

func TestCompactSession_HardModeTruncatesAllHistory(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{Content: "Complete summary of entire conversation."},
		},
	}

	al := newTestAgentLoop(t, prov, 5, nil)
	defer al.bus.Close()

	sessionKey := "test-compact-hard"

	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "Message 1"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", Content: "Response 1"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "Message 2"})
	al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", Content: "Response 2"})

	historyBefore := al.sessions.GetHistory(sessionKey)
	if len(historyBefore) != 4 {
		t.Fatalf("expected 4 messages before compaction, got %d", len(historyBefore))
	}

	err := al.CompactSession(sessionKey, "hard")
	if err != nil {
		t.Fatalf("CompactSession failed: %v", err)
	}

	historyAfter := al.sessions.GetHistory(sessionKey)
	if len(historyAfter) != 0 {
		t.Errorf("hard mode should truncate all messages, got %d remaining", len(historyAfter))
	}

	summary := al.sessions.GetSummary(sessionKey)
	if summary == "" {
		t.Error("summary should be set after hard compaction")
	}
}

func TestCompactSession_PreventsConcurrentCompaction(t *testing.T) {
	prov := &mockProvider{
		responses: []mockResponse{
			{Content: "Summary 1"},
			{Content: "Summary 2"},
		},
	}

	al := newTestAgentLoop(t, prov, 5, nil)
	defer al.bus.Close()

	sessionKey := "test-compact-concurrent"

	for i := 0; i < 10; i++ {
		al.sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "Message"})
	}

	al.summarizing.Store(sessionKey, true)

	err := al.CompactSession(sessionKey, "soft")
	if err == nil {
		t.Fatal("expected error when compaction already in progress")
	}

	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' error, got %v", err)
	}

	al.summarizing.Delete(sessionKey)
}
