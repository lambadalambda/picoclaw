package tools

import (
	"context"
	"fmt"
	"sync"
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

func TestSpawnTool_SetContextConcurrentWithExecute_NoRace(t *testing.T) {
	// Use nil bus so subagent tasks don't publish messages during the test.
	mgr := NewSubagentManager(&fastMockProvider{}, "test-model", t.TempDir(), nil)
	tool := NewSpawnTool(mgr)
	tool.SetContext("telegram", "init")

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			tool.SetContext("telegram", fmt.Sprintf("%d", i))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = tool.Execute(ctx, map[string]interface{}{
				"task": "do the thing",
			})
		}
	}()

	wg.Wait()
}
