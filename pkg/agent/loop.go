// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type AgentLoop struct {
	bus              *bus.MessageBus
	provider         providers.LLMProvider
	workspace        string
	model            string
	contextWindow    int // Maximum context window size in tokens
	maxIterations    int
	llmTimeout       time.Duration // Per-LLM-call timeout (0 = disabled)
	toolTimeout      time.Duration // Per-tool-call timeout (0 = disabled)
	maxParallelTools int           // Max concurrent tools per iteration (<=0 = unlimited)
	sessions         *session.SessionManager
	contextBuilder   *ContextBuilder
	tools            *tools.ToolRegistry
	running          atomic.Bool
	summarizing      sync.Map            // Tracks which sessions are currently being summarized
	statusDelay      time.Duration       // Delay before sending "still working" status updates (0 = disabled)
	memoryStore      *memory.MemoryStore // Searchable memory DB (nil = disabled)
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string // Session identifier for history/context
	Channel         string // Target channel for tool execution
	ChatID          string // Target chat ID for tool execution
	UserMessage     string // User message content (may include prefix)
	DefaultResponse string // Response when LLM returns empty
	EnableSummary   bool   // Whether to trigger summarization
	SendResponse    bool   // Whether to send response via bus
}

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) *AgentLoop {
	workspace := cfg.WorkspacePath()
	os.MkdirAll(workspace, 0755)

	toolsRegistry := tools.NewToolRegistry()
	tools.RegisterCoreTools(toolsRegistry, workspace, cfg.Tools.Web.Search.APIKey, cfg.Tools.Web.Search.MaxResults)

	// Register message tool
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
			Media:   media,
		})
		return nil
	})
	toolsRegistry.Register(messageTool)

	// Register spawn tool
	subagentManager := tools.NewSubagentManager(provider, cfg.Agents.Defaults.Model, workspace, msgBus)
	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)

	// Register memory tools (graceful degradation if SQLite init fails)
	memoryDBPath := filepath.Join(workspace, "memory", "memory.db")
	memoryDB, err := memory.NewMemoryStore(memoryDBPath, workspace)
	if err != nil {
		logger.WarnCF("agent", "Memory DB unavailable, memory tools disabled", map[string]interface{}{"error": err.Error()})
	} else {
		// Reindex existing markdown files into the search index
		if reindexErr := memoryDB.Reindex(); reindexErr != nil {
			logger.WarnCF("agent", "Memory reindex failed", map[string]interface{}{"error": reindexErr.Error()})
		}
		toolsRegistry.Register(tools.NewMemorySearchTool(memoryDB))
		toolsRegistry.Register(tools.NewMemoryStoreTool(memoryDB))
	}

	// memoryDB may be nil — that's fine, extractAndStoreMemories handles it

	sessionsManager := session.NewSessionManager(filepath.Join(workspace, "sessions"))

	// Create context builder and set tools registry
	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)

	return &AgentLoop{
		bus:              msgBus,
		provider:         provider,
		workspace:        workspace,
		model:            cfg.Agents.Defaults.Model,
		contextWindow:    cfg.Agents.Defaults.MaxTokens, // Restore context window for summarization
		maxIterations:    cfg.Agents.Defaults.MaxToolIterations,
		llmTimeout:       time.Duration(cfg.Agents.Defaults.LLMTimeoutSeconds) * time.Second,
		toolTimeout:      time.Duration(cfg.Agents.Defaults.ToolTimeoutSeconds) * time.Second,
		maxParallelTools: cfg.Agents.Defaults.MaxParallelToolCalls,
		sessions:         sessionsManager,
		contextBuilder:   contextBuilder,
		tools:            toolsRegistry,
		summarizing:      sync.Map{},
		statusDelay:      30 * time.Second,
		memoryStore:      memoryDB,
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			response, err := al.processMessage(ctx, msg)
			if err != nil {
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: response,
				})
			}
		}
	}

	return nil
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.tools.Register(tool)
}

func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.processMessage(ctx, msg)
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log
	preview := utils.Truncate(msg.Content, 80)
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, preview),
		map[string]interface{}{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	// Process as user message
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      msg.SessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
	})
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Verify this is a system message
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		// Fallback
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Use the origin session for context
	sessionKey := fmt.Sprintf("%s:%s", originChannel, originChatID)

	// Subagent internal reports should not be forwarded to the end user.
	// They can be stored as internal notes for later integration.
	if strings.HasPrefix(msg.SenderID, "subagent:") {
		event := ""
		if msg.Metadata != nil {
			event = msg.Metadata["subagent_event"]
		}

		// Progress-like events are internal only: store and return no user response.
		switch event {
		case "progress", "note", "warning":
			internal := fmt.Sprintf("[Internal: %s] %s", msg.SenderID, msg.Content)
			al.sessions.AddMessage(sessionKey, "assistant", internal)
			_ = al.sessions.Save(al.sessions.GetOrCreate(sessionKey))
			logger.InfoCF("agent", "Stored subagent update (internal)",
				map[string]interface{}{
					"session_key": sessionKey,
					"event":       event,
					"sender_id":   msg.SenderID,
				})
			return "", nil
		}
	}

	// Process as system message with routing back to origin
	_, err := al.runAgentLoop(ctx, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   false,
		SendResponse:    true, // Send response back to original channel
	})
	if err != nil {
		// Avoid routing errors to the non-existent "system" channel. Send a fallback
		// message directly to the origin channel/chat.
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: originChannel,
			ChatID:  originChatID,
			Content: fmt.Sprintf("Error processing background task: %v", err),
		})
	}
	return "", nil
}

// runAgentLoop is the core message processing logic.
// It handles context building, LLM calls, tool execution, and response handling.
func (al *AgentLoop) runAgentLoop(ctx context.Context, opts processOptions) (string, error) {
	// 1. Update tool contexts
	al.updateToolContexts(opts.Channel, opts.ChatID)

	// 2. Build messages
	history := al.sessions.GetHistory(opts.SessionKey)
	summary := al.sessions.GetSummary(opts.SessionKey)
	messages := al.contextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		nil,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	// 4. Run LLM iteration loop
	finalContent, iteration, err := al.runLLMIteration(ctx, messages, opts)
	if err != nil {
		return "", err
	}

	// 5. Handle empty response
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 6. Save final assistant message to session
	al.sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	al.sessions.Save(al.sessions.GetOrCreate(opts.SessionKey))

	// 7. Optional: summarization
	if opts.EnableSummary {
		al.maybeSummarize(opts.SessionKey)
	}

	// 8. Optional: send response via bus
	if opts.SendResponse {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	// 9. Log response
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"session_key":  opts.SessionKey,
			"iterations":   iteration,
			"final_length": len(finalContent),
		})

	return finalContent, nil
}

// runLLMIteration executes the LLM call loop with tool handling.
// Returns the final content, iteration count, and any error.
func (al *AgentLoop) runLLMIteration(ctx context.Context, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string
	exhausted := true // assume exhausted; set false on clean exit

	for iteration < al.maxIterations {
		iteration++

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"iteration": iteration,
				"max":       al.maxIterations,
			})

		// Build tool definitions
		providerToolDefs := al.tools.GetProviderDefinitions()

		// Log LLM request details
		logger.DebugCF("agent", "LLM request",
			map[string]interface{}{
				"iteration":         iteration,
				"model":             al.model,
				"messages_count":    len(messages),
				"tools_count":       len(providerToolDefs),
				"max_tokens":        8192,
				"temperature":       0.7,
				"system_prompt_len": len(messages[0].Content),
			})

		// Log full messages (detailed)
		logger.DebugCF("agent", "Full LLM request",
			map[string]interface{}{
				"iteration":     iteration,
				"messages_json": formatMessagesForLog(messages),
				"tools_json":    formatToolsForLog(providerToolDefs),
			})

		// Call LLM
		logger.InfoCF("agent", "Calling LLM",
			map[string]interface{}{
				"iteration":      iteration,
				"model":          al.model,
				"messages_count": len(messages),
				"tools_count":    len(providerToolDefs),
			})
		response, err := al.chatWithTimeout(ctx, messages, providerToolDefs, map[string]interface{}{
			"max_tokens":  8192,
			"temperature": 0.7,
		})

		if err != nil {
			logger.ErrorCF("agent", "LLM call failed",
				map[string]interface{}{
					"iteration": iteration,
					"error":     err.Error(),
				})
			return "", iteration, fmt.Errorf("LLM call failed: %w", err)
		}

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			exhausted = false
			logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
				map[string]interface{}{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		// Log tool calls
		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("agent", "LLM requested tool calls",
			map[string]interface{}{
				"tools":     toolNames,
				"count":     len(toolNames),
				"iteration": iteration,
			})

		// Build assistant message with tool calls
		assistantMsg := providers.AssistantMessageFromResponse(response)
		messages = append(messages, assistantMsg)

		// Save assistant message with tool calls to session
		al.sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls concurrently and collect results
		toolResults := al.executeToolsConcurrently(ctx, response.ToolCalls, iteration, opts)

		for _, tr := range toolResults {
			messages = append(messages, tr)
			al.sessions.AddFullMessage(opts.SessionKey, tr)
		}
	}

	// If the loop exhausted all iterations without a direct answer,
	// make one final LLM call with no tools to get a progress summary.
	// The user can then say "continue" to resume.
	if exhausted {
		logger.WarnCF("agent", "Tool iteration limit reached, requesting summary",
			map[string]interface{}{
				"iterations": iteration,
				"max":        al.maxIterations,
			})

		messages = append(messages, providers.Message{
			Role:    "user",
			Content: "You've reached your tool call iteration limit. Please summarize what you've accomplished so far and what still needs to be done. The user can tell you to continue.",
		})

		response, err := al.chatWithTimeout(ctx, messages, nil, map[string]interface{}{
			"max_tokens":  8192,
			"temperature": 0.7,
		})
		if err != nil {
			logger.ErrorCF("agent", "Summary call failed after iteration limit",
				map[string]interface{}{"error": err.Error()})
			finalContent = fmt.Sprintf("I reached my tool call limit (%d iterations) before finishing. Ask me to continue and I'll pick up where I left off.", al.maxIterations)
		} else {
			finalContent = response.Content
		}
	}

	return finalContent, iteration, nil
}

func (al *AgentLoop) chatWithTimeout(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	options map[string]interface{},
) (*providers.LLMResponse, error) {
	callCtx := ctx
	cancel := func() {}
	if al.llmTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, al.llmTimeout)
	}
	defer cancel()

	return al.provider.Chat(callCtx, messages, toolDefs, al.model, options)
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (al *AgentLoop) updateToolContexts(channel, chatID string) {
	if tool, ok := al.tools.Get("message"); ok {
		if mt, ok := tool.(*tools.MessageTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("spawn"); ok {
		if st, ok := tool.(*tools.SpawnTool); ok {
			st.SetContext(channel, chatID)
		}
	}
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// When contextWindow is configured, compaction triggers at 75% token usage.
// Otherwise, falls back to a message count heuristic.
func (al *AgentLoop) maybeSummarize(sessionKey string) {
	newHistory := al.sessions.GetHistory(sessionKey)

	var shouldSummarize bool
	if al.contextWindow > 0 {
		tokenEstimate := al.estimateTokens(newHistory)
		threshold := al.contextWindow * 75 / 100
		shouldSummarize = tokenEstimate > threshold
	} else {
		shouldSummarize = len(newHistory) > 20
	}

	if shouldSummarize {
		if _, loading := al.summarizing.LoadOrStore(sessionKey, true); !loading {
			go func() {
				defer al.summarizing.Delete(sessionKey)
				al.summarizeSession(sessionKey)
			}()
		}
	}
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	// Tools info
	tools := al.tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(tools),
		"names": tools,
	}

	// Skills info
	info["skills"] = al.contextBuilder.GetSkillsInfo()

	return info
}

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(messages []providers.Message) string {
	if len(messages) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, msg := range messages {
		result += fmt.Sprintf("  [%d] Role: %s\n", i, msg.Role)
		if msg.ToolCalls != nil && len(msg.ToolCalls) > 0 {
			result += "  ToolCalls:\n"
			for _, tc := range msg.ToolCalls {
				result += fmt.Sprintf("    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					result += fmt.Sprintf("      Arguments: %s\n", utils.Truncate(tc.Function.Arguments, 200))
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			result += fmt.Sprintf("  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			result += fmt.Sprintf("  ToolCallID: %s\n", msg.ToolCallID)
		}
		result += "\n"
	}
	result += "]"
	return result
}

// formatToolsForLog formats tool definitions for logging
func formatToolsForLog(tools []providers.ToolDefinition) string {
	if len(tools) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, tool := range tools {
		result += fmt.Sprintf("  [%d] Type: %s, Name: %s\n", i, tool.Type, tool.Function.Name)
		result += fmt.Sprintf("      Description: %s\n", tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			result += fmt.Sprintf("      Parameters: %s\n", utils.Truncate(fmt.Sprintf("%v", tool.Function.Parameters), 200))
		}
	}
	result += "]"
	return result
}

// summarizeSession summarizes the conversation history for a session.
func (al *AgentLoop) summarizeSession(sessionKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := al.sessions.GetHistory(sessionKey)
	summary := al.sessions.GetSummary(sessionKey)

	// Keep last 4 messages for continuity
	if len(history) <= 4 {
		return
	}

	toSummarize := history[:len(history)-4]

	// Oversized Message Guard
	// Skip messages larger than 50% of context window to prevent summarizer overflow
	maxMessageTokens := al.contextWindow / 2
	validMessages := make([]providers.Message, 0)
	omitted := false

	for _, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		// Estimate tokens for this message
		msgTokens := len(m.Content) / 4
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, m)
	}

	if len(validMessages) == 0 {
		return
	}

	// Multi-Part Summarization
	// Split into two parts if history is significant
	var finalSummary string
	if len(validMessages) > 10 {
		mid := len(validMessages) / 2
		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := al.summarizeBatch(ctx, part1, "")
		s2, _ := al.summarizeBatch(ctx, part2, "")

		// Merge them
		mergePrompt := fmt.Sprintf("Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s", s1, s2)
		resp, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: mergePrompt}}, nil, al.model, map[string]interface{}{
			"max_tokens":  1024,
			"temperature": 0.3,
		})
		if err == nil {
			finalSummary = resp.Content
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatch(ctx, validMessages, summary)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		al.sessions.SetSummary(sessionKey, finalSummary)
		al.sessions.TruncateHistory(sessionKey, 4)
		al.sessions.Save(al.sessions.GetOrCreate(sessionKey))

		// Extract and store notable memories from the compacted messages
		al.extractAndStoreMemories(ctx, toSummarize)
	}
}

// summarizeBatch summarizes a batch of messages.
func (al *AgentLoop) summarizeBatch(ctx context.Context, batch []providers.Message, existingSummary string) (string, error) {
	prompt := "Provide a concise summary of this conversation segment, preserving core context and key points.\n"
	if existingSummary != "" {
		prompt += "Existing context: " + existingSummary + "\n"
	}
	prompt += "\nCONVERSATION:\n"
	for _, m := range batch {
		prompt += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}

	response, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, al.model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

// estimateTokens estimates the number of tokens in a message list.
func (al *AgentLoop) estimateTokens(messages []providers.Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content) / 4 // Simple heuristic: 4 chars per token
	}
	return total
}
