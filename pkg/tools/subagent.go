package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/llmloop"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/utils"
)

var (
	ErrSubagentTaskNotFound = errors.New("subagent task not found")
	ErrSubagentNotRunning   = errors.New("subagent task is not running")
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
	tasks            map[string]*SubagentTask
	cancels          map[string]context.CancelFunc
	mu               sync.RWMutex
	provider         providers.LLMProvider
	model            string
	chatOptions      providers.ChatOptions
	messageBudget    providers.MessageBudget
	llmTimeout       time.Duration
	toolTimeout      time.Duration
	maxParallelTools int
	maxIterations    int
	bus              *bus.MessageBus
	workspace        string
	nextID           int
}

func NewSubagentManager(provider providers.LLMProvider, model string, workspace string, bus *bus.MessageBus) *SubagentManager {
	return &SubagentManager{
		tasks:            make(map[string]*SubagentTask),
		cancels:          make(map[string]context.CancelFunc),
		provider:         provider,
		model:            model,
		chatOptions:      providers.ChatOptions{MaxTokens: 4096, Temperature: 0.3},
		messageBudget:    providers.BudgetFromContextWindow(8192),
		llmTimeout:       120 * time.Second,
		toolTimeout:      60 * time.Second,
		maxParallelTools: 4,
		maxIterations:    10,
		bus:              bus,
		workspace:        workspace,
		nextID:           1,
	}
}

func (sm *SubagentManager) ConfigureExecution(llmTimeout, toolTimeout time.Duration, maxParallelTools, maxIterations int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if llmTimeout >= 0 {
		sm.llmTimeout = llmTimeout
	}
	if toolTimeout >= 0 {
		sm.toolTimeout = toolTimeout
	}
	if maxParallelTools >= 0 {
		sm.maxParallelTools = maxParallelTools
	}
	if maxIterations > 0 {
		sm.maxIterations = maxIterations
	}
}

func (sm *SubagentManager) ConfigureMessageBudget(budget providers.MessageBudget) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.messageBudget = budget
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
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	taskCtx, cancel := context.WithCancel(baseCtx)
	sm.cancels[taskID] = cancel

	go sm.runTask(taskCtx, taskID)

	logger.InfoCF("subagent", "Spawned subagent",
		map[string]interface{}{
			"task_id":        taskID,
			"label":          label,
			"origin_channel": originChannel,
			"origin_chat_id": originChatID,
			"task_preview":   utils.Truncate(task, 120),
		})

	return taskID, nil
}

func (sm *SubagentManager) Cancel(taskID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	task, ok := sm.tasks[taskID]
	if !ok {
		return ErrSubagentTaskNotFound
	}
	if task.Status != "running" {
		return ErrSubagentNotRunning
	}
	cancel, ok := sm.cancels[taskID]
	if !ok {
		return ErrSubagentNotRunning
	}

	task.Status = "cancelling"
	cancel()
	return nil
}

func (sm *SubagentManager) runTask(ctx context.Context, taskID string) {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	if !ok {
		sm.mu.RUnlock()
		return
	}
	initial := cloneSubagentTask(*task)
	maxIterations := sm.maxIterations
	model := sm.model
	chatOptions := sm.chatOptions
	messageBudget := sm.messageBudget
	llmTimeout := sm.llmTimeout
	toolTimeout := sm.toolTimeout
	maxParallelTools := sm.maxParallelTools
	sm.mu.RUnlock()

	if model == "" {
		model = sm.provider.GetDefaultModel()
	}

	// Build a subagent-only tool registry.
	registry := NewToolRegistry()
	RegisterCoreTools(registry, sm.workspace, "", 5) // web search will self-report if key missing
	registry.Register(NewSubagentReportTool(sm.bus, initial.ID, initial.Label, initial.OriginChannel, initial.OriginChatID))

	systemPrompt := sm.buildSubagentSystemPrompt(registry)
	messages := []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: initial.Task},
	}

	loopRes, finalErr := llmloop.Run(ctx, llmloop.RunOptions{
		Provider:      sm.provider,
		Model:         model,
		MaxIterations: maxIterations,
		LLMTimeout:    llmTimeout,
		ChatOptions:   chatOptions.ToMap(),
		MessageBudget: messageBudget,
		Messages:      messages,
		BuildToolDefs: func(iteration int, _ []providers.Message) []providers.ToolDefinition {
			return registry.GetProviderDefinitions()
		},
		ExecuteTools: func(ctx context.Context, toolCalls []providers.ToolCall, iteration int) []providers.Message {
			return registry.ExecuteToolCalls(ctx, toolCalls, ExecuteToolCallsOptions{
				Timeout:      toolTimeout,
				MaxParallel:  maxParallelTools,
				LogComponent: "subagent",
				Iteration:    iteration,
				OnToolComplete: func(completed, total, index int, call providers.ToolCall, _ providers.Message) {
					logger.DebugCF("subagent", fmt.Sprintf("Tool completed: %s (%d/%d)", call.Name, completed, total),
						map[string]interface{}{
							"task_id":   initial.ID,
							"iteration": iteration,
							"tool":      call.Name,
							"completed": completed,
							"total":     total,
						})
				},
			})
		},
		Hooks: llmloop.Hooks{
			MessagesBudgeted: func(iteration int, stats providers.MessageBudgetStats) {
				logger.WarnCF("subagent", "LLM request payload budget applied",
					map[string]interface{}{
						"task_id":            initial.ID,
						"iteration":          iteration,
						"messages_before":    stats.InputMessages,
						"messages_after":     stats.OutputMessages,
						"chars_before":       stats.CharsBefore,
						"chars_after":        stats.CharsAfter,
						"truncated_messages": stats.TruncatedMessages,
						"dropped_messages":   stats.DroppedMessages,
					})
			},
			BeforeLLMCall: func(iteration int, currentMessages []providers.Message, toolDefs []providers.ToolDefinition) {
				logger.InfoCF("subagent", "Calling LLM",
					map[string]interface{}{
						"task_id":        initial.ID,
						"iteration":      iteration,
						"model":          model,
						"messages_count": len(currentMessages),
						"tools_count":    len(toolDefs),
					})
			},
		},
	})

	status := "completed"
	result := loopRes.FinalContent
	if finalErr != nil {
		if errors.Is(finalErr, context.Canceled) {
			status = "cancelled"
			result = "Cancelled"
		} else {
			status = "failed"
			result = fmt.Sprintf("Error: %v", finalErr)
		}
	}

	sm.mu.Lock()
	task, ok = sm.tasks[taskID]
	if ok {
		task.Status = status
		task.Result = result
	}
	delete(sm.cancels, taskID)
	if ok {
		initial = cloneSubagentTask(*task)
	}
	sm.mu.Unlock()

	switch status {
	case "failed":
		logger.ErrorCF("subagent", "Subagent failed",
			map[string]interface{}{
				"task_id": initial.ID,
				"label":   initial.Label,
				"error":   result,
			})
	case "cancelled":
		logger.InfoCF("subagent", "Subagent cancelled",
			map[string]interface{}{
				"task_id": initial.ID,
				"label":   initial.Label,
			})
	default:
		logger.InfoCF("subagent", "Subagent completed",
			map[string]interface{}{
				"task_id":        initial.ID,
				"label":          initial.Label,
				"result_length":  len(result),
				"result_preview": utils.Truncate(result, 200),
			})
	}

	// Send terminal message back to main agent.
	if sm.bus != nil {
		label := initial.Label
		if label == "" {
			label = initial.ID
		}

		stateWord := "completed"
		event := "complete"
		switch status {
		case "failed":
			stateWord = "failed"
			event = "failed"
		case "cancelled":
			stateWord = "cancelled"
			event = "cancelled"
		}

		announceContent := fmt.Sprintf("Task '%s' %s.\n\nResult:\n%s", label, stateWord, result)
		sm.bus.PublishInbound(bus.InboundMessage{
			Channel:  "system",
			SenderID: fmt.Sprintf("subagent:%s", initial.ID),
			// Format: "original_channel:original_chat_id" for routing back
			ChatID:  fmt.Sprintf("%s:%s", initial.OriginChannel, initial.OriginChatID),
			Content: announceContent,
			Metadata: map[string]string{
				"subagent_event":   event,
				"subagent_task_id": initial.ID,
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

func (sm *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[taskID]
	if !ok {
		return nil, false
	}
	taskCopy := cloneSubagentTask(*task)
	return &taskCopy, true
}

func (sm *SubagentManager) ListTasks() []*SubagentTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	tasks := make([]*SubagentTask, 0, len(sm.tasks))
	for _, task := range sm.tasks {
		taskCopy := cloneSubagentTask(*task)
		tasks = append(tasks, &taskCopy)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Created > tasks[j].Created
	})
	return tasks
}

func cloneSubagentTask(task SubagentTask) SubagentTask {
	return task
}
