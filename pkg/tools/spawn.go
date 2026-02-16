package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/utils"
)

type SpawnTool struct {
	manager *SubagentManager
}

func NewSpawnTool(manager *SubagentManager) *SpawnTool {
	return &SpawnTool{
		manager: manager,
	}
}

func (t *SpawnTool) Name() string {
	return "spawn"
}

func (t *SpawnTool) Description() string {
	return "Manage background subagent tasks. Use action='spawn' for long multi-step or skill-based work (e.g. image generation, complex builds, research). Use action='status' to check one task, action='list' to view tasks, and action='cancel' to stop a running task."
}

func (t *SpawnTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"spawn", "status", "list", "cancel"},
				"description": "Operation to perform. Defaults to 'spawn' if omitted.",
			},
			"task": map[string]interface{}{
				"type":        "string",
				"description": "Task for subagent to complete (required for action='spawn')",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Task ID (required for action='status' and action='cancel')",
			},
			"include_completed": map[string]interface{}{
				"type":        "boolean",
				"description": "For action='list': include completed/failed/cancelled tasks (default false)",
			},
		},
	}
}

func (t *SpawnTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	if action == "" {
		action = "spawn"
	}

	switch strings.ToLower(action) {
	case "spawn":
		task, ok := args["task"].(string)
		if !ok || strings.TrimSpace(task) == "" {
			return "", fmt.Errorf("task is required for action=spawn")
		}

		label, _ := args["label"].(string)
		originChannel, originChatID := getExecutionContext(args)
		if originChannel == "" {
			originChannel = "cli"
		}
		if originChatID == "" {
			originChatID = "direct"
		}

		mgr := t.manager
		if mgr == nil {
			return "Error: Subagent manager not configured", nil
		}

		taskID, err := mgr.Spawn(ctx, task, label, originChannel, originChatID)
		if err != nil {
			return "", fmt.Errorf("failed to spawn subagent: %w", err)
		}
		if label != "" {
			return fmt.Sprintf("Spawned subagent '%s' (id: %s) for task: %s", label, taskID, task), nil
		}
		return fmt.Sprintf("Spawned subagent (id: %s) for task: %s", taskID, task), nil

	case "status":
		mgr := t.manager
		if mgr == nil {
			return "Error: Subagent manager not configured", nil
		}

		taskID, _ := args["task_id"].(string)
		if strings.TrimSpace(taskID) == "" {
			return "", fmt.Errorf("task_id is required for action=status")
		}
		task, ok := mgr.GetTask(taskID)
		if !ok {
			return fmt.Sprintf("Task %s not found", taskID), nil
		}
		return formatSubagentTask(*task), nil

	case "cancel":
		mgr := t.manager
		if mgr == nil {
			return "Error: Subagent manager not configured", nil
		}

		taskID, _ := args["task_id"].(string)
		if strings.TrimSpace(taskID) == "" {
			return "", fmt.Errorf("task_id is required for action=cancel")
		}
		err := mgr.Cancel(taskID)
		if err != nil {
			if errors.Is(err, ErrSubagentTaskNotFound) {
				return fmt.Sprintf("Task %s not found", taskID), nil
			}
			if errors.Is(err, ErrSubagentNotRunning) {
				task, ok := mgr.GetTask(taskID)
				if ok {
					return fmt.Sprintf("Task %s is not running (status: %s)", taskID, task.Status), nil
				}
				return fmt.Sprintf("Task %s is not running", taskID), nil
			}
			return "", err
		}
		return fmt.Sprintf("Cancellation requested for task %s", taskID), nil

	case "list":
		mgr := t.manager
		if mgr == nil {
			return "Error: Subagent manager not configured", nil
		}

		includeCompleted := false
		if raw, ok := args["include_completed"].(bool); ok {
			includeCompleted = raw
		}

		tasks := mgr.ListTasks()
		if len(tasks) == 0 {
			return "No subagent tasks.", nil
		}

		lines := make([]string, 0, len(tasks))
		for _, task := range tasks {
			if !includeCompleted {
				switch task.Status {
				case "completed", "failed", "cancelled":
					continue
				}
			}
			lines = append(lines, formatSubagentTask(*task))
		}

		if len(lines) == 0 {
			if includeCompleted {
				return "No subagent tasks.", nil
			}
			return "No running subagent tasks.", nil
		}

		return strings.Join(lines, "\n\n"), nil

	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func formatSubagentTask(task SubagentTask) string {
	label := task.Label
	if label == "" {
		label = task.ID
	}
	result := task.Result
	if strings.TrimSpace(result) == "" {
		result = "(no result yet)"
	}
	result = utils.Truncate(result, 200)

	return fmt.Sprintf("Task %s\nID: %s\nStatus: %s\nResult: %s", label, task.ID, task.Status, result)
}
