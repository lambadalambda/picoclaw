package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type SubagentTask struct {
	ID            string
	Task          string
	Label         string
	OriginChannel string
	OriginChatID  string
	Status        string
	Result        string
	Created       int64
}

type SubagentManager struct {
	tasks     map[string]*SubagentTask
	mu        sync.RWMutex
	provider  providers.LLMProvider
	model     string
	bus       *bus.MessageBus
	workspace string
	nextID    int
}

func NewSubagentManager(provider providers.LLMProvider, model string, workspace string, bus *bus.MessageBus) *SubagentManager {
	return &SubagentManager{
		tasks:     make(map[string]*SubagentTask),
		provider:  provider,
		model:     model,
		bus:       bus,
		workspace: workspace,
		nextID:    1,
	}
}

func (sm *SubagentManager) Spawn(ctx context.Context, task, label, originChannel, originChatID string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	taskID := fmt.Sprintf("subagent-%d", sm.nextID)
	sm.nextID++

	subagentTask := &SubagentTask{
		ID:            taskID,
		Task:          task,
		Label:         label,
		OriginChannel: originChannel,
		OriginChatID:  originChatID,
		Status:        "running",
		Created:       time.Now().UnixMilli(),
	}
	sm.tasks[taskID] = subagentTask

	go sm.runTask(ctx, subagentTask)

	if label != "" {
		return fmt.Sprintf("Spawned subagent '%s' for task: %s", label, task), nil
	}
	return fmt.Sprintf("Spawned subagent for task: %s", task), nil
}

func (sm *SubagentManager) runTask(ctx context.Context, task *SubagentTask) {
	// Mark running under lock for race safety
	sm.mu.Lock()
	task.Status = "running"
	task.Created = time.Now().UnixMilli()
	sm.mu.Unlock()

	// Build a subagent-only tool registry.
	registry := NewToolRegistry()
	registry.Register(&ReadFileTool{})
	registry.Register(&WriteFileTool{})
	registry.Register(&ListDirTool{})
	registry.Register(NewExecTool(sm.workspace))
	registry.Register(NewEditFileTool(sm.workspace))
	registry.Register(NewWebFetchTool(50000))
	// Web search requires an API key; the tool will self-report if missing.
	registry.Register(NewWebSearchTool("", 5))
	registry.Register(NewSubagentReportTool(sm.bus, task.ID, task.Label, task.OriginChannel, task.OriginChatID))

	systemPrompt := sm.buildSubagentSystemPrompt(registry)
	messages := []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task.Task},
	}

	maxIterations := 10
	model := sm.model
	if model == "" {
		model = sm.provider.GetDefaultModel()
	}

	var final string
	var finalErr error

	for iteration := 1; iteration <= maxIterations; iteration++ {
		toolDefs := sm.buildProviderToolDefinitions(registry)
		logger.InfoCF("subagent", "Calling LLM",
			map[string]interface{}{
				"task_id":        task.ID,
				"iteration":      iteration,
				"model":          model,
				"messages_count": len(messages),
				"tools_count":    len(toolDefs),
			})

		resp, err := sm.provider.Chat(ctx, messages, toolDefs, model, map[string]interface{}{
			"max_tokens":  4096,
			"temperature": 0.3,
		})
		if err != nil {
			finalErr = err
			break
		}

		if len(resp.ToolCalls) == 0 {
			final = resp.Content
			break
		}

		// Append assistant tool-call message to the conversation.
		messages = append(messages, providers.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tool calls sequentially (keep order).
		for _, tc := range resp.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("subagent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"task_id":    task.ID,
					"iteration":  iteration,
					"tool":       tc.Name,
					"tool_callID": tc.ID,
				})

			result, err := registry.Execute(ctx, tc.Name, tc.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			messages = append(messages, providers.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	if finalErr != nil {
		sm.mu.Lock()
		task.Status = "failed"
		task.Result = fmt.Sprintf("Error: %v", finalErr)
		sm.mu.Unlock()
	} else {
		sm.mu.Lock()
		task.Status = "completed"
		task.Result = final
		sm.mu.Unlock()
	}

	// Send completion message back to main agent.
	if sm.bus != nil {
		label := task.Label
		if label == "" {
			label = task.ID
		}
		announceContent := fmt.Sprintf("Task '%s' completed.\n\nResult:\n%s", label, task.Result)
		sm.bus.PublishInbound(bus.InboundMessage{
			Channel:  "system",
			SenderID: fmt.Sprintf("subagent:%s", task.ID),
			// Format: "original_channel:original_chat_id" for routing back
			ChatID: fmt.Sprintf("%s:%s", task.OriginChannel, task.OriginChatID),
			Content: announceContent,
			Metadata: map[string]string{
				"subagent_event":   "complete",
				"subagent_task_id": task.ID,
			},
		})
	}
}

func (sm *SubagentManager) buildSubagentSystemPrompt(registry *ToolRegistry) string {
	// Build tools section dynamically
	toolsSection := ""
	summaries := registry.GetSummaries()
	if len(summaries) > 0 {
		toolsSection = "## Available Tools\n\n" +
			"**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands.\n\n" +
			"You have access to the following tools:\n\n" +
			strings.Join(summaries, "\n")
	}

	// Skills summary (same loader behavior as main agent: workspace > global > builtin)
	wd, _ := os.Getwd()
	globalSkillsDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		globalSkillsDir = filepath.Join(home, ".picoclaw", "skills")
	}
	loader := skills.NewSkillsLoader(sm.workspace, globalSkillsDir, filepath.Join(wd, "skills"))
	skillsSummary := loader.BuildSkillsSummary()
	if skillsSummary != "" {
		skillsSummary = "## Skills\n\nThe following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.\n\n" + skillsSummary
	}

	workspacePath, _ := filepath.Abs(filepath.Join(sm.workspace))

	parts := []string{
		"# picoclaw subagent",
		"You are a background subagent working for the main picoclaw agent.",
		"\nRules:",
		"1. Use tools when you need to perform an action.",
		"2. Do NOT message the end user. Use `subagent_report` to communicate with the main agent.",
		"3. When finished, provide a clear result and include any artifact file paths.",
		fmt.Sprintf("\nWorkspace: %s", workspacePath),
	}

	if toolsSection != "" {
		parts = append(parts, "\n"+toolsSection)
	}
	if skillsSummary != "" {
		parts = append(parts, "\n"+skillsSummary)
	}

	return strings.Join(parts, "\n")
}

func (sm *SubagentManager) buildProviderToolDefinitions(registry *ToolRegistry) []providers.ToolDefinition {
	schemas := registry.GetDefinitions()
	defs := make([]providers.ToolDefinition, 0, len(schemas))
	for _, td := range schemas {
		fn, ok := td["function"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]interface{})
		typeStr, _ := td["type"].(string)
		if name == "" || typeStr == "" {
			continue
		}
		defs = append(defs, providers.ToolDefinition{
			Type: typeStr,
			Function: providers.ToolFunctionDefinition{
				Name:        name,
				Description: desc,
				Parameters:  params,
			},
		})
	}
	return defs
}

func (sm *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[taskID]
	return task, ok
}

func (sm *SubagentManager) ListTasks() []*SubagentTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	tasks := make([]*SubagentTask, 0, len(sm.tasks))
	for _, task := range sm.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}
