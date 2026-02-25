package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/sipeed/picoclaw/pkg/utils"
)

func parseIntArg(args map[string]interface{}, key string) (int, bool) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return 0, false
	}

	switch v := raw.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		return int(v), true
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), true
	case uint64:
		return int(v), true
	case float64:
		if math.Trunc(v) != v {
			return 0, false
		}
		return int(v), true
	case float32:
		if math.Trunc(float64(v)) != float64(v) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

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
			"model": map[string]interface{}{
				"type":        "string",
				"description": "Optional model override for the subagent (e.g., 'claude-sonnet-4', 'glm-4.7')",
			},
			"max_iterations": map[string]interface{}{
				"type":        "integer",
				"description": "Optional max tool iterations override for the subagent (default: 10)",
			},
			"llm_timeout_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "Optional LLM timeout in seconds for the subagent (default: 120)",
			},
			"tool_timeout_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "Optional tool execution timeout in seconds for the subagent (default: 60)",
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
		parentTraceID := getExecutionTraceID(args)
		if originChannel == "" {
			originChannel = "cli"
		}
		if originChatID == "" {
			originChatID = "direct"
		}

		opts := SpawnOptions{}
		if model, ok := args["model"].(string); ok && strings.TrimSpace(model) != "" {
			opts.Model = strings.TrimSpace(model)
		}
		if maxIter, ok := parseIntArg(args, "max_iterations"); ok && maxIter > 0 {
			opts.MaxIterations = maxIter
		}
		if llmTimeout, ok := parseIntArg(args, "llm_timeout_seconds"); ok && llmTimeout > 0 {
			opts.LLMTimeoutSeconds = llmTimeout
		}
		if toolTimeout, ok := parseIntArg(args, "tool_timeout_seconds"); ok && toolTimeout > 0 {
			opts.ToolTimeoutSeconds = toolTimeout
		}

		mgr := t.manager
		if mgr == nil {
			return "Error: Subagent manager not configured", nil
		}

		taskID, err := mgr.Spawn(ctx, task, label, originChannel, originChatID, parentTraceID, opts)
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
