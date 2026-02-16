package tools

import (
	"context"
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

	_, err := registry.ExecuteWithContext(context.Background(), "spawn", map[string]interface{}{
		"task": "do the thing",
	}, "telegram", "chat-ctx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
