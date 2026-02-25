package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type fastMockProvider struct{}

func (p *fastMockProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *fastMockProvider) GetDefaultModel() string { return "test-model" }

func TestSpawnTool_Name(t *testing.T) {
	tool := NewSpawnTool(nil)
	if tool.Name() != "spawn" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "spawn")
	}
}

func TestSpawnTool_Execute_NoTask(t *testing.T) {
	tool := NewSpawnTool(nil)
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
}

func TestSpawnTool_ExecuteWithRegistryContext_UsesOriginChat(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)
	registry := NewToolRegistry()
	registry.Register(tool)

	got, err := registry.ExecuteWithContext(context.Background(), "spawn", map[string]interface{}{
		"task": "do the thing",
	}, "telegram", "chat-ctx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "id: subagent-") {
		t.Fatalf("spawn response should include task id, got %q", got)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 spawned task, got %d", len(tasks))
	}
	if tasks[0].OriginChannel != "telegram" {
		t.Fatalf("OriginChannel = %q, want %q", tasks[0].OriginChannel, "telegram")
	}
	if tasks[0].OriginChatID != "chat-ctx" {
		t.Fatalf("OriginChatID = %q, want %q", tasks[0].OriginChatID, "chat-ctx")
	}
}

func TestSpawnTool_StatusAndList(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "spawn",
		"task":   "do work",
		"label":  "demo",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) == 0 {
		t.Fatal("expected at least one task")
	}
	taskID := tasks[0].ID

	status, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":  "status",
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(status, "ID: "+taskID) {
		t.Fatalf("status output missing task id, got %q", status)
	}

	list, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":            "list",
		"include_completed": true,
	})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(list, "ID: "+taskID) {
		t.Fatalf("list output missing task id, got %q", list)
	}
}

func TestSpawnTool_CancelUnknownTask(t *testing.T) {
	tool := NewSpawnTool(NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil))

	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":  "cancel",
		"task_id": "subagent-999",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "not found") {
		t.Fatalf("expected not found message, got %q", got)
	}
}

func TestSpawnTool_WithOptions_Model(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "spawn",
		"task":   "do work",
		"label":  "demo",
		"model":  "claude-sonnet-4",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Options.Model != "claude-sonnet-4" {
		t.Errorf("Options.Model = %q, want %q", tasks[0].Options.Model, "claude-sonnet-4")
	}
}

func TestSpawnTool_WithOptions_MaxIterations(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":         "spawn",
		"task":           "do work",
		"max_iterations": 50,
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Options.MaxIterations != 50 {
		t.Errorf("Options.MaxIterations = %d, want 50", tasks[0].Options.MaxIterations)
	}
}

func TestSpawnTool_WithOptions_Timeouts(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":               "spawn",
		"task":                 "do work",
		"llm_timeout_seconds":  300,
		"tool_timeout_seconds": 120,
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Options.LLMTimeoutSeconds != 300 {
		t.Errorf("Options.LLMTimeoutSeconds = %d, want 300", tasks[0].Options.LLMTimeoutSeconds)
	}
	if tasks[0].Options.ToolTimeoutSeconds != 120 {
		t.Errorf("Options.ToolTimeoutSeconds = %d, want 120", tasks[0].Options.ToolTimeoutSeconds)
	}
}

func TestSpawnTool_WithOptions_AllOptions(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":               "spawn",
		"task":                 "complex task",
		"label":                "research",
		"model":                "claude-opus-4",
		"max_iterations":       100,
		"llm_timeout_seconds":  300,
		"tool_timeout_seconds": 120,
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Options.Model != "claude-opus-4" {
		t.Errorf("Options.Model = %q, want %q", task.Options.Model, "claude-opus-4")
	}
	if task.Options.MaxIterations != 100 {
		t.Errorf("Options.MaxIterations = %d, want 100", task.Options.MaxIterations)
	}
	if task.Options.LLMTimeoutSeconds != 300 {
		t.Errorf("Options.LLMTimeoutSeconds = %d, want 300", task.Options.LLMTimeoutSeconds)
	}
	if task.Options.ToolTimeoutSeconds != 120 {
		t.Errorf("Options.ToolTimeoutSeconds = %d, want 120", task.Options.ToolTimeoutSeconds)
	}
}

func TestSpawnTool_WithOptions_NoOptions(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "spawn",
		"task":   "simple task",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Options.Model != "" {
		t.Errorf("Options.Model = %q, want empty", task.Options.Model)
	}
	if task.Options.MaxIterations != 0 {
		t.Errorf("Options.MaxIterations = %d, want 0", task.Options.MaxIterations)
	}
	if task.Options.LLMTimeoutSeconds != 0 {
		t.Errorf("Options.LLMTimeoutSeconds = %d, want 0", task.Options.LLMTimeoutSeconds)
	}
	if task.Options.ToolTimeoutSeconds != 0 {
		t.Errorf("Options.ToolTimeoutSeconds = %d, want 0", task.Options.ToolTimeoutSeconds)
	}
}

func TestSpawnTool_WithOptions_FloatToIntConversion(t *testing.T) {
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":         "spawn",
		"task":           "float test",
		"max_iterations": float64(25),
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	tasks := mgr.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Options.MaxIterations != 25 {
		t.Errorf("Options.MaxIterations = %d, want 25", tasks[0].Options.MaxIterations)
	}
}
