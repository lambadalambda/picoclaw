package tools

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type execTestTool struct {
	name    string
	delay   time.Duration
	result  string
	panicOn bool

	inFlight *atomic.Int32
	maxSeen  *atomic.Int32
}

type contextProbeTool struct {
	name string
}

type richResultTool struct {
	name string
}

func (t *contextProbeTool) Name() string        { return t.name }
func (t *contextProbeTool) Description() string { return "context probe tool" }
func (t *contextProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *contextProbeTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	return getExecutionSessionKey(args), nil
}

func (t *richResultTool) Name() string        { return t.name }
func (t *richResultTool) Description() string { return "rich result tool" }
func (t *richResultTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *richResultTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "legacy", nil
}
func (t *richResultTool) ExecuteResult(_ context.Context, _ map[string]interface{}) (ToolResult, error) {
	return ToolResult{
		Content: "ok",
		Parts: []providers.MessagePart{
			{Type: providers.MessagePartTypeImage, Path: "/tmp/input.png", MediaType: "image/png"},
		},
	}, nil
}

func (t *execTestTool) Name() string        { return t.name }
func (t *execTestTool) Description() string { return "executor test tool" }
func (t *execTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *execTestTool) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	if t.panicOn {
		panic("boom")
	}

	if t.inFlight != nil && t.maxSeen != nil {
		current := t.inFlight.Add(1)
		for {
			prev := t.maxSeen.Load()
			if current <= prev || t.maxSeen.CompareAndSwap(prev, current) {
				break
			}
		}
		defer t.inFlight.Add(-1)
	}

	select {
	case <-time.After(t.delay):
		return t.result, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestExecuteToolCalls_TimeoutProducesError(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&execTestTool{name: "slow", delay: 300 * time.Millisecond, result: "ok"})

	results := registry.ExecuteToolCalls(context.Background(), []providers.ToolCall{
		{ID: "tc1", Name: "slow", Arguments: map[string]interface{}{}},
	}, ExecuteToolCallsOptions{Timeout: 50 * time.Millisecond, MaxParallel: 1})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ToolCallID != "tc1" {
		t.Fatalf("ToolCallID = %q, want %q", results[0].ToolCallID, "tc1")
	}
	if results[0].Content == "ok" {
		t.Fatalf("expected timeout error result, got success content: %q", results[0].Content)
	}
}

func TestExecuteToolCalls_RespectsMaxParallel(t *testing.T) {
	registry := NewToolRegistry()
	inFlight := &atomic.Int32{}
	maxSeen := &atomic.Int32{}

	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("t%d", i)
		registry.Register(&execTestTool{
			name:     name,
			delay:    120 * time.Millisecond,
			result:   name + "_ok",
			inFlight: inFlight,
			maxSeen:  maxSeen,
		})
	}

	toolCalls := []providers.ToolCall{
		{ID: "tc1", Name: "t1", Arguments: map[string]interface{}{}},
		{ID: "tc2", Name: "t2", Arguments: map[string]interface{}{}},
		{ID: "tc3", Name: "t3", Arguments: map[string]interface{}{}},
		{ID: "tc4", Name: "t4", Arguments: map[string]interface{}{}},
	}

	results := registry.ExecuteToolCalls(context.Background(), toolCalls, ExecuteToolCallsOptions{MaxParallel: 2})
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if got := maxSeen.Load(); got > 2 {
		t.Fatalf("max concurrent tools = %d, want <= 2", got)
	}
}

func TestExecuteToolCalls_PanicRecovered(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&execTestTool{name: "panic_tool", panicOn: true})

	results := registry.ExecuteToolCalls(context.Background(), []providers.ToolCall{
		{ID: "tc1", Name: "panic_tool", Arguments: map[string]interface{}{}},
	}, ExecuteToolCallsOptions{})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ToolCallID != "tc1" {
		t.Fatalf("ToolCallID = %q, want %q", results[0].ToolCallID, "tc1")
	}
	if results[0].Content == "" {
		t.Fatal("expected panic error content, got empty result")
	}
}

func TestExecuteToolCalls_PassesExecutionSessionKey(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&contextProbeTool{name: "ctx_probe"})

	results := registry.ExecuteToolCalls(context.Background(), []providers.ToolCall{
		{ID: "tc1", Name: "ctx_probe", Arguments: map[string]interface{}{}},
	}, ExecuteToolCallsOptions{SessionKey: "telegram:chat-42"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "telegram:chat-42" {
		t.Fatalf("tool received session key %q, want %q", results[0].Content, "telegram:chat-42")
	}
}

func TestExecuteToolCalls_AttachesRichToolParts(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&richResultTool{name: "rich"})

	results := registry.ExecuteToolCalls(context.Background(), []providers.ToolCall{
		{ID: "tc1", Name: "rich", Arguments: map[string]interface{}{}},
	}, ExecuteToolCallsOptions{})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "ok" {
		t.Fatalf("Content = %q, want ok", results[0].Content)
	}
	if len(results[0].Parts) != 1 {
		t.Fatalf("len(Parts) = %d, want 1", len(results[0].Parts))
	}
	if results[0].Parts[0].Type != providers.MessagePartTypeImage {
		t.Fatalf("Parts[0].Type = %q, want %q", results[0].Parts[0].Type, providers.MessagePartTypeImage)
	}
}

func TestExecuteToolCalls_CallsOnToolStart(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&execTestTool{name: "slow", delay: 20 * time.Millisecond, result: "ok"})

	var starts atomic.Int32
	results := registry.ExecuteToolCalls(context.Background(), []providers.ToolCall{
		{ID: "tc1", Name: "slow", Arguments: map[string]interface{}{}},
	}, ExecuteToolCallsOptions{
		MaxParallel: 1,
		OnToolStart: func(started, total, index int, call providers.ToolCall) {
			starts.Add(1)
			if started <= 0 {
				t.Errorf("started = %d, want > 0", started)
			}
			if total != 1 {
				t.Errorf("total = %d, want 1", total)
			}
			if index != 0 {
				t.Errorf("index = %d, want 0", index)
			}
			if call.Name != "slow" {
				t.Errorf("call.Name = %q, want slow", call.Name)
			}
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "ok" {
		t.Fatalf("Content = %q, want ok", results[0].Content)
	}
	if starts.Load() != 1 {
		t.Fatalf("OnToolStart calls = %d, want 1", starts.Load())
	}
}
