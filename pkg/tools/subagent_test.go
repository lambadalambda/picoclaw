package tools

import (
	"context"
	"errors"
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
	_, err := sm.Spawn(context.Background(), "do work", "imggen", "telegram", "chat1", "")
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

func TestSubagentManager_CancelRunningTask(t *testing.T) {
	prov := &blockingProvider{started: make(chan struct{})}
	sm := NewSubagentManager(prov, "test-model", t.TempDir(), nil)

	taskID, err := sm.Spawn(context.Background(), "do long work", "long", "telegram", "chat1", "")
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

	taskID, err := sm.Spawn(context.Background(), "quick work", "quick", "telegram", "chat1", "")
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
		_, err := sm.Spawn(context.Background(), "task", "", "telegram", "chat1", "")
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
	sm.tasks["old"] = &SubagentTask{ID: "old", Status: "completed", Created: time.Now().Add(-10 * time.Second).UnixMilli(), Finished: time.Now().Add(-10 * time.Second).UnixMilli()}
	sm.tasks["new"] = &SubagentTask{ID: "new", Status: "completed", Created: time.Now().UnixMilli(), Finished: time.Now().UnixMilli()}
	sm.cleanupLocked(time.Now())
	sm.mu.Unlock()

	if _, ok := sm.GetTask("old"); ok {
		t.Fatal("expected old completed task to be removed by TTL cleanup")
	}
	if _, ok := sm.GetTask("new"); !ok {
		t.Fatal("expected recent completed task to remain after TTL cleanup")
	}
}
