package tools

import (
	"context"
	"fmt"
	"sync"
)

type SpawnTool struct {
	mu            sync.RWMutex
	manager       *SubagentManager
	originChannel string
	originChatID  string
}

func NewSpawnTool(manager *SubagentManager) *SpawnTool {
	return &SpawnTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

func (t *SpawnTool) Name() string {
	return "spawn"
}

func (t *SpawnTool) Description() string {
	return "Spawn a subagent to handle a task in the background. Use this for complex or time-consuming tasks that can run independently. The subagent will complete the task and report back when done."
}

func (t *SpawnTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task for subagent to complete",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SpawnTool) SetContext(channel, chatID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.originChannel = channel
	t.originChatID = chatID
}

func (t *SpawnTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	task, ok := args["task"].(string)
	if !ok {
		return "", fmt.Errorf("task is required")
	}

	label, _ := args["label"].(string)

	t.mu.RLock()
	mgr := t.manager
	originChannel := t.originChannel
	originChatID := t.originChatID
	t.mu.RUnlock()

	if mgr == nil {
		return "Error: Subagent manager not configured", nil
	}

	result, err := mgr.Spawn(ctx, task, label, originChannel, originChatID)
	if err != nil {
		return "", fmt.Errorf("failed to spawn subagent: %w", err)
	}

	return result, nil
}
