package tools

import (
	"context"
	"encoding/json"
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

type SpawnOptions struct {
	Model              string
	MaxIterations      int
	LLMTimeoutSeconds  int
	ToolTimeoutSeconds int
}

type SubagentTask struct {
	ID               string
	Task             string
	Label            string
	OriginChannel    string
	OriginChatID     string
	OriginSessionKey string
	ParentTraceID    string
	Status           string
	Result           string
	Created          int64
	Finished         int64
	Options          SpawnOptions
}

type SubagentManager struct {
	tasks            map[string]*SubagentTask
	cancels          map[string]context.CancelFunc
	mu               sync.RWMutex
	provider         providers.LLMProvider
	model            string
	chatOptions      providers.ChatOptions
	messageBudget    providers.MessageBudget
	maxStoredTasks   int
	completedTTL     time.Duration
	llmTimeout       time.Duration
	toolTimeout      time.Duration
	maxParallelTools int
	maxIterations    int
	bus              *bus.MessageBus
	workspace        string
	nextID           int
}

func toolCallSignature(toolCalls []providers.ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	type sig struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments,omitempty"`
	}
	payload := make([]sig, 0, len(toolCalls))
	for _, tc := range toolCalls {
		payload = append(payload, sig{Name: tc.Name, Arguments: tc.Arguments})
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}

func isMissingRequiredToolError(msg providers.Message) bool {
	if msg.Role != "tool" {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(msg.Content))
	if !strings.HasPrefix(content, "error:") {
		return false
	}
	return strings.Contains(content, "missing required parameter") || strings.Contains(content, "missing required parameters")
}

func NewSubagentManager(provider providers.LLMProvider, model string, workspace string, bus *bus.MessageBus) *SubagentManager {
	return &SubagentManager{
		tasks:            make(map[string]*SubagentTask),
		cancels:          make(map[string]context.CancelFunc),
		provider:         provider,
		model:            model,
		chatOptions:      providers.ChatOptions{MaxTokens: 4096, Temperature: 0.3},
		messageBudget:    providers.BudgetFromContextWindow(8192),
		maxStoredTasks:   200,
		completedTTL:     24 * time.Hour,
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

func (sm *SubagentManager) ConfigureRetention(maxStoredTasks int, completedTTL time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if maxStoredTasks > 0 {
		sm.maxStoredTasks = maxStoredTasks
	}
	if completedTTL >= 0 {
		sm.completedTTL = completedTTL
	}
}

func (sm *SubagentManager) ConfigureCache(anthropicCache bool, anthropicCacheTTL string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.chatOptions.AnthropicCache = anthropicCache
	sm.chatOptions.AnthropicCacheTTL = strings.TrimSpace(anthropicCacheTTL)
}

func (sm *SubagentManager) Spawn(ctx context.Context, task, label, originChannel, originChatID, originSessionKey, parentTraceID string, opts SpawnOptions) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.cleanupLocked(time.Now())

	taskID := fmt.Sprintf("subagent-%d", sm.nextID)
	sm.nextID++

	subagentTask := &SubagentTask{
		ID:               taskID,
		Task:             task,
		Label:            label,
		OriginChannel:    originChannel,
		OriginChatID:     originChatID,
		OriginSessionKey: strings.TrimSpace(originSessionKey),
		ParentTraceID:    parentTraceID,
		Status:           "running",
		Created:          time.Now().UnixMilli(),
		Finished:         0,
		Options:          opts,
	}
	if subagentTask.OriginSessionKey == "" && originChannel != "" && originChatID != "" {
		subagentTask.OriginSessionKey = fmt.Sprintf("%s:%s", originChannel, originChatID)
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
			"trace_id":       parentTraceID,
			"task_preview":   utils.Truncate(task, 120),
			"model":          opts.Model,
			"max_iterations": opts.MaxIterations,
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

	if initial.Options.Model != "" {
		model = initial.Options.Model
	}
	if initial.Options.MaxIterations > 0 {
		maxIterations = initial.Options.MaxIterations
	}
	if initial.Options.LLMTimeoutSeconds > 0 {
		llmTimeout = time.Duration(initial.Options.LLMTimeoutSeconds) * time.Second
	}
	if initial.Options.ToolTimeoutSeconds > 0 {
		toolTimeout = time.Duration(initial.Options.ToolTimeoutSeconds) * time.Second
	}

	if model == "" {
		model = sm.provider.GetDefaultModel()
	}

	// Build a subagent-only tool registry.
	registry := NewToolRegistry()
	RegisterCoreTools(registry, sm.workspace, WebSearchToolConfig{MaxResults: 5}) // web search will self-report if key missing
	registry.Register(NewSubagentReportTool(sm.bus, initial.ID, initial.Label, initial.OriginChannel, initial.OriginChatID))

	systemPrompt := sm.buildSubagentSystemPrompt(registry)
	messages := []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: initial.Task},
	}

	lastRepeatedSignature := ""
	consecutiveMissingArgLoops := 0

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
			sessionKey := strings.TrimSpace(initial.OriginSessionKey)
			if sessionKey == "" && initial.OriginChannel != "" && initial.OriginChatID != "" {
				sessionKey = fmt.Sprintf("%s:%s", initial.OriginChannel, initial.OriginChatID)
			}

			results := registry.ExecuteToolCalls(ctx, toolCalls, ExecuteToolCallsOptions{
				SessionKey:   sessionKey,
				TraceID:      initial.ParentTraceID,
				Timeout:      toolTimeout,
				MaxParallel:  maxParallelTools,
				LogComponent: "subagent",
				Iteration:    iteration,
				OnToolComplete: func(completed, total, index int, call providers.ToolCall, _ providers.Message) {
					logger.DebugCF("subagent", fmt.Sprintf("Tool completed: %s (%d/%d)", call.Name, completed, total),
						map[string]interface{}{
							"task_id":   initial.ID,
							"trace_id":  initial.ParentTraceID,
							"iteration": iteration,
							"tool":      call.Name,
							"completed": completed,
							"total":     total,
						})
				},
			})

			signature := toolCallSignature(toolCalls)
			if len(toolCalls) == 1 && len(results) == 1 && isMissingRequiredToolError(results[0]) {
				if signature != "" && signature == lastRepeatedSignature {
					consecutiveMissingArgLoops++
				} else {
					lastRepeatedSignature = signature
					consecutiveMissingArgLoops = 1
				}

				if consecutiveMissingArgLoops >= 3 {
					toolName := toolCalls[0].Name
					escalated := fmt.Sprintf("Error: Repeated invalid tool call detected (%d attempts) for `%s`. The previous call is missing required parameters. Do NOT retry the same call. Fix the arguments before retrying. For `edit_file`, include `path`, `old_text`, and `new_text`.", consecutiveMissingArgLoops, toolName)
					results[0] = providers.ToolResultMessage(results[0].ToolCallID, escalated)
					logger.WarnCF("subagent", "Detected repeated invalid tool call loop",
						map[string]interface{}{
							"task_id":      initial.ID,
							"trace_id":     initial.ParentTraceID,
							"iteration":    iteration,
							"tool":         toolName,
							"repeat_count": consecutiveMissingArgLoops,
						})
				}
			} else {
				lastRepeatedSignature = ""
				consecutiveMissingArgLoops = 0
			}

			return results
		},
		Hooks: llmloop.Hooks{
			MessagesBudgeted: func(iteration int, stats providers.MessageBudgetStats) {
				logger.WarnCF("subagent", "LLM request payload budget applied",
					map[string]interface{}{
						"task_id":            initial.ID,
						"trace_id":           initial.ParentTraceID,
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
				logger.DebugCF("subagent", "Full LLM request",
					map[string]interface{}{
						"task_id":       initial.ID,
						"trace_id":      initial.ParentTraceID,
						"iteration":     iteration,
						"messages_json": formatMessagesForLog(currentMessages),
						"tools_json":    formatToolsForLog(toolDefs),
					})

				logger.InfoCF("subagent", "Calling LLM",
					map[string]interface{}{
						"task_id":        initial.ID,
						"trace_id":       initial.ParentTraceID,
						"iteration":      iteration,
						"model":          model,
						"messages_count": len(currentMessages),
						"tools_count":    len(toolDefs),
					})
			},
			ToolCallsRequested: func(iteration int, toolCalls []providers.ToolCall) {
				toolNames := make([]string, 0, len(toolCalls))
				for _, tc := range toolCalls {
					toolNames = append(toolNames, tc.Name)
				}
				logger.InfoCF("subagent", "LLM requested tool calls",
					map[string]interface{}{
						"task_id":   initial.ID,
						"trace_id":  initial.ParentTraceID,
						"iteration": iteration,
						"tools":     toolNames,
						"count":     len(toolNames),
					})
			},
			ToolResultMessage: func(iteration int, msg providers.Message) {
				preview := utils.Truncate(msg.Content, 220)
				fields := map[string]interface{}{
					"task_id":      initial.ID,
					"trace_id":     initial.ParentTraceID,
					"iteration":    iteration,
					"tool_call_id": msg.ToolCallID,
					"result_chars": len(msg.Content),
					"preview":      preview,
				}
				if strings.HasPrefix(strings.TrimSpace(msg.Content), "Error:") {
					logger.WarnCF("subagent", "Tool result error", fields)
					return
				}
				logger.DebugCF("subagent", "Tool result", fields)
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
		task.Finished = time.Now().UnixMilli()
	}
	delete(sm.cancels, taskID)
	sm.cleanupLocked(time.Now())
	if ok {
		initial = cloneSubagentTask(*task)
	}
	sm.mu.Unlock()

	switch status {
	case "failed":
		logger.ErrorCF("subagent", "Subagent failed",
			map[string]interface{}{
				"task_id":  initial.ID,
				"label":    initial.Label,
				"trace_id": initial.ParentTraceID,
				"error":    result,
			})
	case "cancelled":
		logger.InfoCF("subagent", "Subagent cancelled",
			map[string]interface{}{
				"task_id":  initial.ID,
				"label":    initial.Label,
				"trace_id": initial.ParentTraceID,
			})
	default:
		logger.InfoCF("subagent", "Subagent completed",
			map[string]interface{}{
				"task_id":        initial.ID,
				"label":          initial.Label,
				"trace_id":       initial.ParentTraceID,
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
				"trace_id":         initial.ParentTraceID,
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
		"3. If you need prior context, use `session_history` to inspect the parent chat transcript (you have execution context for the originating session).",
		"4. When finished, provide a clear result and include any artifact file paths.",
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

func (sm *SubagentManager) cleanupLocked(now time.Time) {
	if len(sm.tasks) == 0 {
		return
	}

	nowMS := now.UnixMilli()

	// TTL-based cleanup for terminal tasks.
	if sm.completedTTL > 0 {
		ttlMS := sm.completedTTL.Milliseconds()
		for id, task := range sm.tasks {
			if !isTerminalSubagentStatus(task.Status) {
				continue
			}
			finished := task.Finished
			if finished == 0 {
				finished = task.Created
			}
			if nowMS-finished >= ttlMS {
				delete(sm.tasks, id)
				delete(sm.cancels, id)
			}
		}
	}

	if sm.maxStoredTasks <= 0 || len(sm.tasks) <= sm.maxStoredTasks {
		return
	}

	// Capacity cleanup: remove oldest terminal tasks first.
	type candidate struct {
		id       string
		finished int64
	}
	terminals := make([]candidate, 0, len(sm.tasks))
	for id, task := range sm.tasks {
		if !isTerminalSubagentStatus(task.Status) {
			continue
		}
		finished := task.Finished
		if finished == 0 {
			finished = task.Created
		}
		terminals = append(terminals, candidate{id: id, finished: finished})
	}

	sort.Slice(terminals, func(i, j int) bool {
		return terminals[i].finished < terminals[j].finished
	})

	for _, c := range terminals {
		if len(sm.tasks) <= sm.maxStoredTasks {
			break
		}
		delete(sm.tasks, c.id)
		delete(sm.cancels, c.id)
	}
}

func isTerminalSubagentStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
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

func formatMessagesForLog(messages []providers.Message) string {
	if len(messages) == 0 {
		return "[]"
	}

	var b strings.Builder
	b.WriteString("[\n")
	for i, msg := range messages {
		b.WriteString(fmt.Sprintf("  [%d] role=%s\n", i, msg.Role))
		if msg.Content != "" {
			b.WriteString(fmt.Sprintf("      content=%s\n", utils.Truncate(msg.Content, 200)))
		}
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				args := ""
				if tc.Function != nil {
					args = tc.Function.Arguments
				}
				b.WriteString(fmt.Sprintf("      tool_call id=%s name=%s args=%s\n", tc.ID, tc.Name, utils.Truncate(args, 200)))
			}
		}
		if msg.ToolCallID != "" {
			b.WriteString(fmt.Sprintf("      tool_call_id=%s\n", msg.ToolCallID))
		}
	}
	b.WriteString("]")
	return b.String()
}

func formatToolsForLog(tools []providers.ToolDefinition) string {
	if len(tools) == 0 {
		return "[]"
	}

	var b strings.Builder
	b.WriteString("[\n")
	for i, tool := range tools {
		b.WriteString(fmt.Sprintf("  [%d] name=%s type=%s\n", i, tool.Function.Name, tool.Type))
		if tool.Function.Description != "" {
			b.WriteString(fmt.Sprintf("      description=%s\n", utils.Truncate(tool.Function.Description, 140)))
		}
		if len(tool.Function.Parameters) > 0 {
			b.WriteString(fmt.Sprintf("      parameters=%s\n", utils.Truncate(fmt.Sprintf("%v", tool.Function.Parameters), 220)))
		}
	}
	b.WriteString("]")
	return b.String()
}
