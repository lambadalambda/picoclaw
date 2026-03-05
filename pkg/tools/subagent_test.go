package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type scriptedProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	callIdx   int
}

func (p *scriptedProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.callIdx >= len(p.responses) {
		return &providers.LLMResponse{Content: ""}, nil
	}
	r := p.responses[p.callIdx]
	p.callIdx++
	return r, nil
}

func (p *scriptedProvider) GetDefaultModel() string { return "test-model" }

type blockingProvider struct {
	started chan struct{}
	once    sync.Once
}

func (p *blockingProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.once.Do(func() {
		close(p.started)
	})
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingProvider) GetDefaultModel() string { return "test-model" }

type doneProvider struct{}

func (p *doneProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *doneProvider) GetDefaultModel() string { return "test-model" }

type recordingOptionsProvider struct {
	mu      sync.Mutex
	called  chan struct{}
	options map[string]interface{}
	once    sync.Once
}

func (p *recordingOptionsProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	if p.options == nil {
		copy := make(map[string]interface{}, len(options))
		for k, v := range options {
			copy[k] = v
		}
		p.options = copy
	}
	p.mu.Unlock()

	p.once.Do(func() {
		if p.called != nil {
			close(p.called)
		}
	})
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *recordingOptionsProvider) GetDefaultModel() string { return "test-model" }

func TestSubagentManager_SubagentReportPublishesInbound(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	prov := &scriptedProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "tc1",
				Name: "subagent_report",
				Arguments: map[string]interface{}{
					"event":   "progress",
					"content": "step 1",
				},
			}},
		},
		{Content: "done"},
	}}

	sm := NewSubagentManager(prov, "test-model", t.TempDir(), msgBus)
	_, err := sm.Spawn(context.Background(), "do work", "imggen", "telegram", "chat1", "telegram:chat1", "", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	gotProgress := false
	gotComplete := false

	for !(gotProgress && gotComplete) {
		msg, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			break
		}

		if msg.Channel != "system" {
			continue
		}
		if msg.ChatID != "telegram:chat1" {
			continue
		}

		event := ""
		if msg.Metadata != nil {
			event = msg.Metadata["subagent_event"]
		}
		switch event {
		case "progress":
			gotProgress = true
			if msg.Content != "step 1" {
				t.Errorf("progress content = %q, want %q", msg.Content, "step 1")
			}
		case "complete":
			gotComplete = true
			if msg.Content == "" {
				t.Error("expected non-empty completion content")
			}
		}
	}

	if !gotProgress {
		t.Fatal("expected progress report inbound message")
	}
	if !gotComplete {
		t.Fatal("expected completion inbound message")
	}
}

func TestSubagentManager_MessageToolPublishesOutboundToOrigin(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "generated"), 0755); err != nil {
		t.Fatalf("mkdir generated: %v", err)
	}

	rel := filepath.Join("generated", "test.png")
	if err := os.WriteFile(filepath.Join(workspace, rel), []byte("x"), 0644); err != nil {
		t.Fatalf("write media: %v", err)
	}

	prov := &scriptedProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "tc1",
				Name: "message",
				Arguments: map[string]interface{}{
					"content": "Here's your image!",
					"media":   []interface{}{rel},
				},
			}},
		},
		{Content: "done"},
	}}

	sm := NewSubagentManager(prov, "test-model", workspace, msgBus)
	_, err := sm.Spawn(context.Background(), "send", "imggen", "deltachat", "chat1", "deltachat:chat1", "trace-123", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	out, ok := msgBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message from subagent message tool")
	}
	if out.Channel != "deltachat" {
		t.Fatalf("channel = %q, want %q", out.Channel, "deltachat")
	}
	if out.ChatID != "chat1" {
		t.Fatalf("chat_id = %q, want %q", out.ChatID, "chat1")
	}
	if out.Content != "Here's your image!" {
		t.Fatalf("content = %q, want %q", out.Content, "Here's your image!")
	}
	if len(out.Media) != 1 {
		t.Fatalf("media length = %d, want 1", len(out.Media))
	}
	wantMedia := filepath.Clean(filepath.Join(workspace, rel))
	if out.Media[0] != wantMedia {
		t.Fatalf("media[0] = %q, want %q", out.Media[0], wantMedia)
	}
}

func TestSubagentManager_MessageToolFromHeartbeatRoutesToMainChat(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()

	prov := &scriptedProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "tc1",
				Name: "message",
				Arguments: map[string]interface{}{
					"content": "heartbeat artifact ready",
				},
			}},
		},
		{Content: "done"},
	}}

	sm := NewSubagentManager(prov, "test-model", workspace, msgBus)
	_, err := sm.Spawn(context.Background(), "send", "hb", "heartbeat", "deltachat:chat1", "heartbeat:deltachat:chat1", "trace-123", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	out, ok := msgBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound heartbeat subagent message")
	}
	if out.Channel != "deltachat" {
		t.Fatalf("channel = %q, want %q", out.Channel, "deltachat")
	}
	if out.ChatID != "chat1" {
		t.Fatalf("chat_id = %q, want %q", out.ChatID, "chat1")
	}
	if out.Content != "heartbeat artifact ready" {
		t.Fatalf("content = %q, want %q", out.Content, "heartbeat artifact ready")
	}
}

func TestSubagentManager_MessageToolIgnoresExplicitTargetOverride(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()

	prov := &scriptedProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "tc1",
				Name: "message",
				Arguments: map[string]interface{}{
					"content": "hello",
					"channel": "telegram",
					"chat_id": "override-chat",
				},
			}},
		},
		{Content: "done"},
	}}

	sm := NewSubagentManager(prov, "test-model", workspace, msgBus)
	_, err := sm.Spawn(context.Background(), "send", "msg", "deltachat", "chat1", "deltachat:chat1", "trace-123", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	out, ok := msgBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message")
	}
	if out.Channel != "deltachat" {
		t.Fatalf("channel = %q, want %q", out.Channel, "deltachat")
	}
	if out.ChatID != "chat1" {
		t.Fatalf("chat_id = %q, want %q", out.ChatID, "chat1")
	}
}

func TestSubagentManager_MessageToolBlocksMediaOutsideWorkspace(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	workspace := t.TempDir()
	outside := t.TempDir()
	mediaPath := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(mediaPath, []byte("nope"), 0644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	prov := &scriptedProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "tc1",
				Name: "message",
				Arguments: map[string]interface{}{
					"content": "attempt",
					"media":   []interface{}{mediaPath},
				},
			}},
		},
		{Content: "done"},
	}}

	sm := NewSubagentManager(prov, "test-model", workspace, msgBus)
	_, err := sm.Spawn(context.Background(), "send", "msg", "deltachat", "chat1", "deltachat:chat1", "trace-123", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, ok := msgBus.SubscribeOutbound(ctx); ok {
		t.Fatal("expected no outbound message")
	}
}

func TestSubagentManager_CancelRunningTask(t *testing.T) {
	prov := &blockingProvider{started: make(chan struct{})}
	sm := NewSubagentManager(prov, "test-model", t.TempDir(), nil)

	taskID, err := sm.Spawn(context.Background(), "do long work", "long", "telegram", "chat1", "telegram:chat1", "", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	select {
	case <-prov.started:
		// task entered provider call
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subagent did not start provider call")
	}

	if err := sm.Cancel(taskID); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		task, ok := sm.GetTask(taskID)
		if !ok {
			t.Fatalf("task %s disappeared", taskID)
		}
		if task.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected task to become cancelled, current status=%q", task.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubagentManager_CancelNotRunning(t *testing.T) {
	prov := &scriptedProvider{responses: []*providers.LLMResponse{{Content: "done"}}}
	sm := NewSubagentManager(prov, "test-model", t.TempDir(), nil)

	taskID, err := sm.Spawn(context.Background(), "quick work", "quick", "telegram", "chat1", "telegram:chat1", "", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		task, ok := sm.GetTask(taskID)
		if !ok {
			t.Fatalf("task %s disappeared", taskID)
		}
		if task.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected task to complete, current status=%q", task.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	err = sm.Cancel(taskID)
	if !errors.Is(err, ErrSubagentNotRunning) {
		t.Fatalf("expected ErrSubagentNotRunning, got %v", err)
	}
}

func TestSubagentManager_RetentionMaxTasks(t *testing.T) {
	sm := NewSubagentManager(&doneProvider{}, "test-model", t.TempDir(), nil)
	sm.ConfigureRetention(2, 24*time.Hour)

	for i := 0; i < 4; i++ {
		_, err := sm.Spawn(context.Background(), "task", "", "telegram", "chat1", "telegram:chat1", "", SpawnOptions{})
		if err != nil {
			t.Fatalf("spawn failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		tasks := sm.ListTasks()
		allDone := len(tasks) > 0
		for _, task := range tasks {
			if task.Status == "running" || task.Status == "cancelling" {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for tasks to complete")
		}
		time.Sleep(20 * time.Millisecond)
	}

	tasks := sm.ListTasks()
	if len(tasks) > 2 {
		t.Fatalf("expected at most 2 tasks after retention cleanup, got %d", len(tasks))
	}
	if _, ok := sm.GetTask("subagent-1"); ok {
		t.Fatal("expected oldest task subagent-1 to be cleaned up")
	}
}

func TestSubagentManager_RetentionTTL(t *testing.T) {
	sm := NewSubagentManager(&doneProvider{}, "test-model", t.TempDir(), nil)
	sm.ConfigureRetention(100, 1*time.Second)

	sm.mu.Lock()
	sm.tasks["old"] = &SubagentTask{ID: "old", Status: "completed", Created: time.Now().Add(-10 * time.Second).UnixMilli(), Finished: time.Now().Add(-10 * time.Second).UnixMilli(), Options: SpawnOptions{}}
	sm.tasks["new"] = &SubagentTask{ID: "new", Status: "completed", Created: time.Now().UnixMilli(), Finished: time.Now().UnixMilli(), Options: SpawnOptions{}}
	sm.cleanupLocked(time.Now())
	sm.mu.Unlock()

	if _, ok := sm.GetTask("old"); ok {
		t.Fatal("expected old completed task to be removed by TTL cleanup")
	}
	if _, ok := sm.GetTask("new"); !ok {
		t.Fatal("expected recent completed task to remain after TTL cleanup")
	}
}

func TestSubagentManager_PropagatesAnthropicCacheOptions(t *testing.T) {
	prov := &recordingOptionsProvider{called: make(chan struct{})}
	sm := NewSubagentManager(prov, "test-model", t.TempDir(), nil)
	sm.ConfigureCache(true, "1h")

	_, err := sm.Spawn(context.Background(), "do work", "", "telegram", "chat1", "telegram:chat1", "", SpawnOptions{})
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	select {
	case <-prov.called:
		// ok
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for provider Chat call")
	}

	prov.mu.Lock()
	opts := prov.options
	prov.mu.Unlock()

	if got, ok := opts["anthropic_cache"].(bool); !ok || !got {
		t.Fatalf("anthropic_cache = %#v, want true", opts["anthropic_cache"])
	}
	if got, ok := opts["anthropic_cache_ttl"].(string); !ok || got != "1h" {
		t.Fatalf("anthropic_cache_ttl = %#v, want 1h", opts["anthropic_cache_ttl"])
	}
}

func TestToolCallSignature_StableForSamePayload(t *testing.T) {
	callA := []providers.ToolCall{{
		ID:   "tc-1",
		Name: "edit_file",
		Arguments: map[string]interface{}{
			"path":     "/tmp/demo.md",
			"old_text": "[CONTINUE FROM HERE]",
		},
	}}
	callB := []providers.ToolCall{{
		ID:   "tc-2",
		Name: "edit_file",
		Arguments: map[string]interface{}{
			"old_text": "[CONTINUE FROM HERE]",
			"path":     "/tmp/demo.md",
		},
	}}

	sigA := toolCallSignature(callA)
	sigB := toolCallSignature(callB)
	if sigA == "" || sigB == "" {
		t.Fatal("expected non-empty signatures")
	}
	if sigA != sigB {
		t.Fatalf("expected stable signature, got %q vs %q", sigA, sigB)
	}
}

func TestIsMissingRequiredToolError(t *testing.T) {
	msg := providers.ToolResultMessage("tc-1", "Error: Missing required parameter: new_text. Supply correct parameters before retrying.")
	if !isMissingRequiredToolError(msg) {
		t.Fatal("expected missing required tool error to be detected")
	}

	nonErr := providers.ToolResultMessage("tc-2", "ok")
	if isMissingRequiredToolError(nonErr) {
		t.Fatal("did not expect non-error tool result to match")
	}

	otherErr := providers.ToolResultMessage("tc-3", "Error: file not found")
	if isMissingRequiredToolError(otherErr) {
		t.Fatal("did not expect unrelated error to match")
	}

	if strings.TrimSpace(msg.Content) == "" {
		t.Fatal("expected test message content")
	}
}

func TestBuildSubagentSystemPrompt_IncludesSessionHistoryGuidance(t *testing.T) {
	sm := NewSubagentManager(&doneProvider{}, "test-model", t.TempDir(), nil)
	registry := NewToolRegistry()
	RegisterCoreTools(registry, t.TempDir(), WebSearchToolConfig{MaxResults: 5})

	prompt := sm.buildSubagentSystemPrompt(registry)
	if !strings.Contains(prompt, "session_history") {
		t.Fatalf("expected prompt to mention session_history guidance, got:\n%s", prompt)
	}
}
