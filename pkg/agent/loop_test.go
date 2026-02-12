package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
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
	Err       error
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

func (t *noopTool) Name() string                       { return t.name }
func (t *noopTool) Description() string                { return "test tool" }
func (t *noopTool) Parameters() map[string]interface{} { return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}} }
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

	return &AgentLoop{
		bus:           bus.NewMessageBus(),
		provider:      provider,
		workspace:     tmpDir,
		model:         "test-model",
		maxIterations: maxIter,
		sessions:      session.NewSessionManager(filepath.Join(tmpDir, "sessions")),
		tools:         registry,
		summarizing:   sync.Map{},
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

	content, iterations, err := al.runLLMIteration(context.Background(), messages, opts)
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

	content, iterations, err := al.runLLMIteration(context.Background(), messages, opts)
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

	_, _, err := al.runLLMIteration(context.Background(), messages, opts)
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
