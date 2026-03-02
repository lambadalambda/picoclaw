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
	"github.com/sipeed/picoclaw/pkg/llmloop"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/vision"
)

type AgentLoop struct {
	bus               *bus.MessageBus
	provider          providers.LLMProvider
	workspace         string
	model             string
	contextWindow     int                   // Maximum context window size in tokens
	chatOptions       providers.ChatOptions // Standard chat response options
	compactOptions    providers.ChatOptions // Summarization/extraction options
	messageBudget     providers.MessageBudget
	maxIterations     int
	llmTimeout        time.Duration // Per-LLM-call timeout (0 = disabled)
	toolTimeout       time.Duration // Per-tool-call timeout (0 = disabled)
	maxParallelTools  int           // Max concurrent tools per iteration (<=0 = unlimited)
	sessions          *session.SessionManager
	contextBuilder    *ContextBuilder
	tools             *tools.ToolRegistry
	unsafeGate        *tools.UnsafeToolGate
	traceSeq          atomic.Uint64
	running           atomic.Bool
	summarizing       sync.Map            // Tracks which sessions are currently being summarized
	memoryStore       *memory.MemoryStore // Searchable memory DB (nil = disabled)
	modelCapabilities providers.ModelCapabilities
	visionAnalyzer    imageAnalyzer
	echoToolCalls     bool // Echo tool calls to chat channel
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string // Session identifier for history/context
	Channel         string // Target channel for tool execution
	ChatID          string // Target chat ID for tool execution
	TraceID         string // Correlation ID for logs across one processing flow
	UserMessage     string // User message content (may include prefix)
	UserMedia       []string
	DefaultResponse string // Response when LLM returns empty
	EnableSummary   bool   // Whether to trigger summarization
	SendResponse    bool   // Whether to send response via bus
}

type processTaskResult struct {
	message     bus.InboundMessage
	sessionKey  string
	response    string
	err         error
	interrupted bool
}

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) *AgentLoop {
	workspace := cfg.WorkspacePath()
	os.MkdirAll(workspace, 0755)
	messageBudget := messageBudgetFromDefaults(cfg.Agents.Defaults)
	webSearchCfg := cfg.Tools.Web.Search
	zaiSearchKey, zaiSearchBase := resolveZAISearchCredentials(webSearchCfg, cfg.Providers)

	toolsRegistry := tools.NewToolRegistry()
	unsafeGate := tools.NewUnsafeToolGate(10 * time.Minute)
	toolsRegistry.SetUnsafeToolGate(unsafeGate)
	tools.RegisterCoreTools(toolsRegistry, workspace, tools.WebSearchToolConfig{
		BraveAPIKey:     webSearchCfg.APIKey,
		MaxResults:      webSearchCfg.MaxResults,
		Provider:        webSearchCfg.Provider,
		ZAIAPIKey:       zaiSearchKey,
		ZAIAPIBase:      zaiSearchBase,
		ZAIMCPURL:       webSearchCfg.ZAIMCPURL,
		ZAILocation:     webSearchCfg.ZAILocation,
		ZAISearchEngine: webSearchCfg.ZAISearchEngine,
	})

	policyEnabled := cfg.Tools.Policy.Enabled || cfg.Tools.Policy.SafeMode || len(cfg.Tools.Policy.Allow) > 0 || len(cfg.Tools.Policy.Deny) > 0
	denyTools := append([]string{}, cfg.Tools.Policy.Deny...)
	if cfg.Tools.Policy.SafeMode {
		denyTools = append(denyTools,
			"exec",
			"write_file",
			"edit_file",
			"unsafe_read_file",
			"unsafe_write_file",
			"unsafe_list_dir",
			"unsafe_edit_file",
		)
	}
	toolsRegistry.SetExecutionPolicy(tools.NewToolExecutionPolicy(policyEnabled, cfg.Tools.Policy.Allow, denyTools))

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
	subagentManager.ConfigureExecution(
		time.Duration(cfg.Agents.Defaults.LLMTimeoutSeconds)*time.Second,
		time.Duration(cfg.Agents.Defaults.ToolTimeoutSeconds)*time.Second,
		cfg.Agents.Defaults.MaxParallelToolCalls,
		cfg.Agents.Defaults.MaxToolIterations,
	)
	subagentManager.ConfigureMessageBudget(messageBudget)
	subagentManager.ConfigureRetention(
		cfg.Agents.Defaults.SubagentMaxTasks,
		time.Duration(cfg.Agents.Defaults.SubagentCompletedTTLSeconds)*time.Second,
	)
	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)
	subagentManager.ConfigureUnsafeToolGate(unsafeGate)

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

	chatTemperature := cfg.Agents.Defaults.Temperature
	if chatTemperature == 0 {
		chatTemperature = 0.7
	}

	outputMaxTokens, contextWindow := resolveTokenLimits(cfg.Agents.Defaults)
	anthropicCacheTTL := strings.TrimSpace(cfg.Agents.Defaults.AnthropicCacheTTL)
	subagentManager.ConfigureCache(cfg.Agents.Defaults.AnthropicCache, anthropicCacheTTL)

	modelCaps := providers.ModelCapabilitiesFor(cfg.Agents.Defaults.Model)

	var visionAnalyzer imageAnalyzer
	visionAnalyzerModel := ""
	visionCfg := cfg.Tools.Vision
	if visionCfg.Enabled {
		visionAPIKey := strings.TrimSpace(visionCfg.APIKey)
		if visionAPIKey == "" {
			visionAPIKey = strings.TrimSpace(cfg.Providers.Zhipu.APIKey)
		}
		visionAPIBase := strings.TrimSpace(visionCfg.APIBase)
		if visionAPIBase == "" {
			visionAPIBase = strings.TrimSpace(cfg.Providers.Zhipu.APIBase)
			if visionAPIBase == "" {
				visionAPIBase = "https://open.bigmodel.cn/api/paas/v4"
			}
		}
		visionAnalyzerModel = strings.TrimSpace(visionCfg.Model)
		if visionAnalyzerModel == "" {
			visionAnalyzerModel = "glm-4.6v"
		}

		if visionAPIKey != "" {
			visionClient := vision.NewClient(visionAPIKey, visionAPIBase, visionAnalyzerModel)
			if visionCfg.TimeoutSeconds > 0 {
				visionClient.Timeout = time.Duration(visionCfg.TimeoutSeconds) * time.Second
			}
			if visionCfg.MaxImages > 0 {
				visionClient.MaxImages = visionCfg.MaxImages
			}
			visionAnalyzer = visionClient
		}
	}

	primaryVisionAnalyzer, primaryVisionModel := resolvePrimaryVisionAnalyzer(cfg)
	inlineVision := modelCaps.SupportsVision && modelCaps.SupportsInlineVision
	if inlineVision {
		inlineVision = providers.SupportsInlineVisionTransport(provider, cfg.Agents.Defaults.Model)
	}
	if primaryVisionAnalyzer != nil || visionAnalyzer != nil || inlineVision {
		toolsRegistry.Register(tools.NewImageInspectTool(
			workspace,
			primaryVisionAnalyzer,
			primaryVisionModel,
			visionAnalyzer,
			visionAnalyzerModel,
		))
	}

	return &AgentLoop{
		bus:           msgBus,
		provider:      provider,
		workspace:     workspace,
		model:         cfg.Agents.Defaults.Model,
		contextWindow: contextWindow,
		chatOptions: providers.ChatOptions{
			MaxTokens:         outputMaxTokens,
			Temperature:       chatTemperature,
			AnthropicCache:    cfg.Agents.Defaults.AnthropicCache,
			AnthropicCacheTTL: anthropicCacheTTL,
		},
		compactOptions: providers.ChatOptions{
			MaxTokens:         1024,
			Temperature:       0.3,
			AnthropicCache:    cfg.Agents.Defaults.AnthropicCache,
			AnthropicCacheTTL: anthropicCacheTTL,
		},
		messageBudget:     messageBudget,
		maxIterations:     cfg.Agents.Defaults.MaxToolIterations,
		llmTimeout:        time.Duration(cfg.Agents.Defaults.LLMTimeoutSeconds) * time.Second,
		toolTimeout:       time.Duration(cfg.Agents.Defaults.ToolTimeoutSeconds) * time.Second,
		maxParallelTools:  cfg.Agents.Defaults.MaxParallelToolCalls,
		sessions:          sessionsManager,
		contextBuilder:    contextBuilder,
		tools:             toolsRegistry,
		unsafeGate:        unsafeGate,
		summarizing:       sync.Map{},
		memoryStore:       memoryDB,
		modelCapabilities: modelCaps,
		visionAnalyzer:    visionAnalyzer,
		echoToolCalls:     cfg.Agents.Defaults.EchoToolCalls,
	}
}

func resolveZAISearchCredentials(webCfg config.WebSearchConfig, providersCfg config.ProvidersConfig) (string, string) {
	zaiSearchKey := strings.TrimSpace(webCfg.ZAIAPIKey)
	if zaiSearchKey == "" {
		zaiSearchKey = strings.TrimSpace(providersCfg.Zhipu.APIKey)
	}
	if zaiSearchKey == "" {
		zaiSearchKey = strings.TrimSpace(providersCfg.Modal.APIKey)
	}

	zaiSearchBase := strings.TrimSpace(webCfg.ZAIAPIBase)
	if zaiSearchBase == "" {
		zaiSearchBase = strings.TrimSpace(providersCfg.Zhipu.APIBase)
	}

	return zaiSearchKey, zaiSearchBase
}

func resolveTokenLimits(d config.AgentDefaults) (outputMaxTokens int, contextWindow int) {
	const defaultOutputMaxTokens = 8192
	const largeMaxTokensAssumeContextWindow = 32768

	outputMaxTokens = d.MaxTokens
	if outputMaxTokens <= 0 {
		outputMaxTokens = defaultOutputMaxTokens
	}

	contextWindow = d.ContextWindowTokens
	if contextWindow <= 0 {
		contextWindow = d.MaxTokens
	}
	if contextWindow <= 0 {
		contextWindow = outputMaxTokens
	}

	// Backwards compatibility: historically agents.defaults.max_tokens was used
	// as a context window estimate. If context_window_tokens is unset and
	// max_tokens looks like a large context window, keep output tokens conservative.
	if d.ContextWindowTokens <= 0 && d.MaxTokens > largeMaxTokensAssumeContextWindow {
		contextWindow = d.MaxTokens
		outputMaxTokens = defaultOutputMaxTokens
	}

	if outputMaxTokens <= 0 {
		outputMaxTokens = defaultOutputMaxTokens
	}
	if contextWindow <= 0 {
		contextWindow = outputMaxTokens
	}

	return outputMaxTokens, contextWindow
}

func resolvePrimaryVisionAnalyzer(cfg *config.Config) (*vision.Client, string) {
	model := strings.TrimSpace(cfg.Agents.Defaults.Model)
	if model == "" {
		return nil, ""
	}

	// The primary LLM model is only usable as a vision analyzer if it supports
	// multimodal inputs. Otherwise we'll get generic 400 errors when sending
	// image_url content.
	if !providers.ModelCapabilitiesFor(model).SupportsVision {
		return nil, ""
	}

	apiKey, apiBase, ok := resolveOpenAICompatibleProviderForModel(cfg, model)
	if !ok || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(apiBase) == "" {
		return nil, ""
	}

	visionClient := vision.NewClient(apiKey, apiBase, model)
	visionCfg := cfg.Tools.Vision
	if visionCfg.TimeoutSeconds > 0 {
		visionClient.Timeout = time.Duration(visionCfg.TimeoutSeconds) * time.Second
	}
	if visionCfg.MaxImages > 0 {
		visionClient.MaxImages = visionCfg.MaxImages
	}

	return visionClient, model
}

func resolveOpenAICompatibleProviderForModel(cfg *config.Config, model string) (apiKey, apiBase string, ok bool) {
	lowerModel := strings.ToLower(strings.TrimSpace(model))

	switch {
	case strings.HasPrefix(model, "openrouter/") || strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "openai/") || strings.HasPrefix(model, "meta-llama/") || strings.HasPrefix(model, "deepseek/") || strings.HasPrefix(model, "google/"):
		apiKey = strings.TrimSpace(cfg.Providers.OpenRouter.APIKey)
		apiBase = strings.TrimSpace(cfg.Providers.OpenRouter.APIBase)
		if apiBase == "" {
			apiBase = "https://openrouter.ai/api/v1"
		}
		return apiKey, apiBase, apiKey != ""

	case (strings.Contains(lowerModel, "claude") || strings.HasPrefix(model, "anthropic/")) && (cfg.Providers.Anthropic.APIKey != "" || cfg.Providers.Anthropic.AuthMethod != ""):
		return "", "", false

	case (strings.Contains(lowerModel, "gpt") || strings.HasPrefix(model, "openai/")) && (cfg.Providers.OpenAI.APIKey != "" || cfg.Providers.OpenAI.AuthMethod != ""):
		apiKey = strings.TrimSpace(cfg.Providers.OpenAI.APIKey)
		apiBase = strings.TrimSpace(cfg.Providers.OpenAI.APIBase)
		if apiBase == "" {
			apiBase = "https://api.openai.com/v1"
		}
		return apiKey, apiBase, apiKey != ""

	case (strings.Contains(lowerModel, "gemini") || strings.HasPrefix(model, "google/")) && cfg.Providers.Gemini.APIKey != "":
		return "", "", false

	case (strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zhipu") || strings.Contains(lowerModel, "zai")) && cfg.Providers.Zhipu.APIKey != "":
		apiKey = strings.TrimSpace(cfg.Providers.Zhipu.APIKey)
		apiBase = strings.TrimSpace(cfg.Providers.Zhipu.APIBase)
		if apiBase == "" {
			apiBase = "https://open.bigmodel.cn/api/paas/v4"
		}
		return apiKey, apiBase, apiKey != ""

	case (strings.Contains(lowerModel, "groq") || strings.HasPrefix(model, "groq/")) && cfg.Providers.Groq.APIKey != "":
		apiKey = strings.TrimSpace(cfg.Providers.Groq.APIKey)
		apiBase = strings.TrimSpace(cfg.Providers.Groq.APIBase)
		if apiBase == "" {
			apiBase = "https://api.groq.com/openai/v1"
		}
		return apiKey, apiBase, apiKey != ""

	case (strings.Contains(lowerModel, "glm-5") || strings.HasPrefix(lowerModel, "zai-org/")) && cfg.Providers.Modal.APIKey != "":
		apiKey = strings.TrimSpace(cfg.Providers.Modal.APIKey)
		apiBase = strings.TrimSpace(cfg.Providers.Modal.APIBase)
		if apiBase == "" {
			apiBase = "https://api.us-west-2.modal.direct/v1"
		}
		return apiKey, apiBase, apiKey != ""

	case strings.TrimSpace(cfg.Providers.VLLM.APIBase) != "":
		apiKey = strings.TrimSpace(cfg.Providers.VLLM.APIKey)
		apiBase = strings.TrimSpace(cfg.Providers.VLLM.APIBase)
		return apiKey, apiBase, true

	default:
		if cfg.Providers.OpenRouter.APIKey != "" {
			apiKey = strings.TrimSpace(cfg.Providers.OpenRouter.APIKey)
			apiBase = strings.TrimSpace(cfg.Providers.OpenRouter.APIBase)
			if apiBase == "" {
				apiBase = "https://openrouter.ai/api/v1"
			}
			return apiKey, apiBase, true
		}
		return "", "", false
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	inboundCh := make(chan bus.InboundMessage, 100)
	go func() {
		defer close(inboundCh)
		for al.running.Load() {
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				return
			}

			select {
			case inboundCh <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	pendingBySession := make(map[string]bus.InboundMessage)
	pendingOrder := make([]string, 0)

	var activeDone <-chan processTaskResult
	var activeCancel context.CancelFunc
	activeSessionKey := ""

	startNext := func() {
		if activeDone != nil || len(pendingOrder) == 0 {
			return
		}

		sessionKey := pendingOrder[0]
		pendingOrder = pendingOrder[1:]
		msg := pendingBySession[sessionKey]
		delete(pendingBySession, sessionKey)

		procCtx, cancel := context.WithCancel(ctx)
		done := make(chan processTaskResult, 1)
		activeCancel = cancel
		activeDone = done
		activeSessionKey = sessionKey

		go func(procCtx context.Context, msg bus.InboundMessage, sessionKey string, done chan<- processTaskResult) {
			response, err := al.processMessage(procCtx, msg)
			done <- processTaskResult{
				message:     msg,
				sessionKey:  sessionKey,
				response:    response,
				err:         err,
				interrupted: procCtx.Err() != nil,
			}
		}(procCtx, msg, sessionKey, done)
	}

	for al.running.Load() {
		startNext()

		select {
		case <-ctx.Done():
			if activeCancel != nil {
				activeCancel()
			}
			return nil
		case msg, ok := <-inboundCh:
			if !ok {
				if activeCancel != nil {
					activeCancel()
				}
				return nil
			}

			sessionKey := inboundSessionKey(msg)
			msg.SessionKey = sessionKey

			if shouldInterruptActiveRun(msg) && activeDone != nil && activeSessionKey == sessionKey && activeCancel != nil {
				logger.InfoCF("agent", "Interrupting active run due to newer user message",
					map[string]interface{}{
						"session_key": sessionKey,
						"channel":     msg.Channel,
						"chat_id":     msg.ChatID,
					})
				activeCancel()
			}

			if _, exists := pendingBySession[sessionKey]; !exists {
				pendingOrder = append(pendingOrder, sessionKey)
			}
			pendingBySession[sessionKey] = msg
		case res := <-activeDone:
			activeDone = nil
			activeCancel = nil
			activeSessionKey = ""

			if res.interrupted {
				logger.InfoCF("agent", "Message processing interrupted",
					map[string]interface{}{
						"session_key": res.sessionKey,
						"channel":     res.message.Channel,
						"chat_id":     res.message.ChatID,
					})
				continue
			}

			response := res.response
			if res.err != nil {
				response = fmt.Sprintf("Error processing message: %v", res.err)
			}

			if response != "" {
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: res.message.Channel,
					ChatID:  res.message.ChatID,
					Content: response,
				})
			}
		}
	}

	if activeCancel != nil {
		activeCancel()
	}

	return nil
}

func inboundSessionKey(msg bus.InboundMessage) string {
	if sessionKey := strings.TrimSpace(msg.SessionKey); sessionKey != "" {
		return sessionKey
	}
	return fmt.Sprintf("%s:%s", msg.Channel, msg.ChatID)
}

func shouldInterruptActiveRun(msg bus.InboundMessage) bool {
	return msg.Channel != "system"
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
}

func (al *AgentLoop) nextTraceID() string {
	seq := al.traceSeq.Add(1)
	return fmt.Sprintf("trace-%d", seq)
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
	traceID := ""
	if msg.Metadata != nil {
		traceID = msg.Metadata["trace_id"]
	}
	if traceID == "" {
		traceID = al.nextTraceID()
	}
	ctx = tools.WithTraceID(ctx, traceID)

	// Record the most recent active chat target for cron defaults.
	al.recordLastActiveTarget(msg)

	// Add message preview to log
	preview := utils.Truncate(msg.Content, 80)
	logFields := map[string]interface{}{
		"channel":     msg.Channel,
		"chat_id":     msg.ChatID,
		"sender_id":   msg.SenderID,
		"session_key": msg.SessionKey,
		"trace_id":    traceID,
	}
	if bridgeToGateway := strings.TrimSpace(msg.Metadata["bridge_to_gateway_ms"]); bridgeToGateway != "" {
		logFields["bridge_to_gateway_ms"] = bridgeToGateway
	}
	if sentToBridge := strings.TrimSpace(msg.Metadata["dc_sent_to_bridge_ms"]); sentToBridge != "" {
		logFields["dc_sent_to_bridge_ms"] = sentToBridge
	}
	if transportMillis := strings.TrimSpace(msg.Metadata["dc_transport_ms"]); transportMillis != "" {
		logFields["dc_transport_ms"] = transportMillis
	}

	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, preview),
		logFields)

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg, traceID)
	}

	// Unsafe tool approvals are session-scoped and are granted via an explicit user
	// message token. This lets the agent ask before using unsafe_* tools.
	if al.unsafeGate != nil {
		if approve, revoke, ttl := parseUnsafeApprovalToken(msg.Content); approve {
			effective := al.unsafeGate.Approve(msg.SessionKey, ttl)
			logger.InfoCF("agent", "Unsafe tool approval granted",
				map[string]interface{}{
					"session_key": msg.SessionKey,
					"ttl":         effective.String(),
					"trace_id":    traceID,
				})
		} else if revoke {
			al.unsafeGate.Revoke(msg.SessionKey)
			logger.InfoCF("agent", "Unsafe tool approval revoked",
				map[string]interface{}{
					"session_key": msg.SessionKey,
					"trace_id":    traceID,
				})
		}
	}

	userMessage := msg.Content
	var userMedia []string
	if len(msg.Media) > 0 {
		userMessage, userMedia = al.buildUserMessageWithMediaContext(ctx, msg.Content, msg.Media, traceID)
	}

	// Process as user message
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      msg.SessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		TraceID:         traceID,
		UserMessage:     userMessage,
		UserMedia:       userMedia,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
	})
}

func parseUnsafeApprovalToken(content string) (approve bool, revoke bool, ttl time.Duration) {
	content = strings.TrimSpace(content)
	if content == "" {
		return false, false, 0
	}

	upper := strings.ToUpper(content)
	if strings.HasPrefix(upper, "UNSAFE_OFF") || strings.HasPrefix(upper, "UNSAFE_NO") {
		return false, true, 0
	}

	if !strings.HasPrefix(upper, "UNSAFE_OK") {
		return false, false, 0
	}

	// Optional duration: "UNSAFE_OK 10m".
	parts := strings.Fields(content)
	if len(parts) >= 2 {
		if d, err := time.ParseDuration(parts[1]); err == nil {
			return true, false, d
		}
	}

	return true, false, 0
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage, traceID string) (string, error) {
	// Verify this is a system message
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
			"trace_id":  traceID,
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

	// Heartbeat-spawned subagents should report back to the heartbeat session
	// only. They must not inject system messages into the user's active chat.
	if originChannel == "heartbeat" && strings.HasPrefix(msg.SenderID, "subagent:") {
		event := ""
		if msg.Metadata != nil {
			event = msg.Metadata["subagent_event"]
		}
		internal := fmt.Sprintf("[Internal: %s] %s", msg.SenderID, msg.Content)
		if strings.TrimSpace(event) != "" {
			internal = fmt.Sprintf("[Internal: %s (%s)] %s", msg.SenderID, event, msg.Content)
		}
		al.sessions.AddMessage(sessionKey, "assistant", internal)
		_ = al.sessions.Save(al.sessions.GetOrCreate(sessionKey))
		logger.InfoCF("agent", "Stored heartbeat subagent update (internal)",
			map[string]interface{}{
				"session_key": sessionKey,
				"event":       event,
				"sender_id":   msg.SenderID,
				"trace_id":    traceID,
			})
		return "", nil
	}

	// Subagent internal reports should not be forwarded to the end user.
	// They can be stored as internal notes for later integration.
	if strings.HasPrefix(msg.SenderID, "subagent:") {
		event := ""
		if msg.Metadata != nil {
			event = msg.Metadata["subagent_event"]
		}

		// Progress-like events are internal only: store and return no user response.
		switch event {
		case "progress", "note", "warning", "cancelled":
			internal := fmt.Sprintf("[Internal: %s] %s", msg.SenderID, msg.Content)
			al.sessions.AddMessage(sessionKey, "assistant", internal)
			_ = al.sessions.Save(al.sessions.GetOrCreate(sessionKey))
			logger.InfoCF("agent", "Stored subagent update (internal)",
				map[string]interface{}{
					"session_key": sessionKey,
					"event":       event,
					"sender_id":   msg.SenderID,
					"trace_id":    traceID,
				})
			return "", nil
		}
	}

	// Process as system message with routing back to origin
	_, err := al.runAgentLoop(ctx, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		TraceID:         traceID,
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
	// 1. Build messages
	history := al.sessions.GetHistory(opts.SessionKey)
	summary := al.sessions.GetSummary(opts.SessionKey)
	messages := al.contextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		opts.UserMedia,
		opts.Channel,
		opts.ChatID,
	)

	// 2. Save user message to session
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)
	_ = al.sessions.Save(al.sessions.GetOrCreate(opts.SessionKey))

	// 3. Run LLM iteration loop
	finalContent, iteration, promptTokens, deliveredViaMessageTool, err := al.runLLMIteration(ctx, messages, opts)
	if err != nil {
		return "", err
	}

	// 4. Handle empty response
	finalContent = strings.TrimSpace(finalContent)
	if finalContent == "" || (opts.DefaultResponse != "" && finalContent == opts.DefaultResponse) {
		if deliveredViaMessageTool {
			// A message was already delivered via the message tool. Avoid sending a
			// redundant filler response.
			finalContent = ""
		} else {
			finalContent = opts.DefaultResponse
		}
	}

	// 5. Save final assistant message to session
	if finalContent != "" {
		al.sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
		al.sessions.Save(al.sessions.GetOrCreate(opts.SessionKey))
	}

	// 6. Optional: summarization
	if opts.EnableSummary {
		al.maybeSummarize(opts.SessionKey, promptTokens)
	}

	// 7. Optional: send response via bus
	if opts.SendResponse {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	// 8. Log response
	if finalContent != "" {
		responsePreview := utils.Truncate(finalContent, 120)
		logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
			map[string]interface{}{
				"session_key":  opts.SessionKey,
				"trace_id":     opts.TraceID,
				"iterations":   iteration,
				"final_length": len(finalContent),
			})
	} else {
		logger.InfoCF("agent", "No final response to send",
			map[string]interface{}{
				"session_key":             opts.SessionKey,
				"trace_id":                opts.TraceID,
				"iterations":              iteration,
				"delivered_via_message":   deliveredViaMessageTool,
				"default_response_config": strings.TrimSpace(opts.DefaultResponse) != "",
			})
	}

	return finalContent, nil
}

type tokenUsageTrackingProvider struct {
	inner           providers.LLMProvider
	maxPromptTokens int
}

func (p *tokenUsageTrackingProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	resp, err := p.inner.Chat(ctx, messages, tools, model, options)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Usage != nil && resp.Usage.PromptTokens > p.maxPromptTokens {
		p.maxPromptTokens = resp.Usage.PromptTokens
	}
	return resp, nil
}

func (p *tokenUsageTrackingProvider) GetDefaultModel() string {
	return p.inner.GetDefaultModel()
}

func deliveredMessageToolToTarget(channel, chatID string, toolCalls []providers.ToolCall, results []providers.Message) bool {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel == "" || chatID == "" {
		return false
	}

	target := channel + ":" + chatID
	for i, tc := range toolCalls {
		if !strings.EqualFold(strings.TrimSpace(tc.Name), "message") {
			continue
		}
		if i >= len(results) {
			continue
		}
		content := strings.TrimSpace(results[i].Content)
		if !strings.HasPrefix(content, "Message sent to ") {
			continue
		}
		dest := strings.TrimSpace(strings.TrimPrefix(content, "Message sent to "))
		if strings.EqualFold(dest, target) {
			return true
		}
	}
	return false
}

// runLLMIteration executes the LLM call loop with tool handling.
// Returns the final content, iteration count, and any error.
func (al *AgentLoop) runLLMIteration(ctx context.Context, messages []providers.Message, opts processOptions) (string, int, int, bool, error) {
	chatOptions := al.chatOptions.ToMap()
	trackingProvider := &tokenUsageTrackingProvider{inner: al.provider}
	deliveredViaMessageTool := false

	loopRes, err := llmloop.Run(ctx, llmloop.RunOptions{
		Provider:      trackingProvider,
		Model:         al.model,
		MaxIterations: al.maxIterations,
		LLMTimeout:    al.llmTimeout,
		ChatOptions:   chatOptions,
		MessageBudget: al.messageBudget,
		Messages:      messages,
		BuildToolDefs: func(iteration int, _ []providers.Message) []providers.ToolDefinition {
			return al.tools.GetProviderDefinitions()
		},
		ExecuteTools: func(ctx context.Context, toolCalls []providers.ToolCall, iteration int) []providers.Message {
			results := al.executeToolsConcurrently(ctx, toolCalls, iteration, opts)
			if !deliveredViaMessageTool {
				deliveredViaMessageTool = deliveredMessageToolToTarget(opts.Channel, opts.ChatID, toolCalls, results)
			}
			return results
		},
		Hooks: llmloop.Hooks{
			MessagesBudgeted: func(iteration int, stats providers.MessageBudgetStats) {
				logger.WarnCF("agent", "LLM request payload budget applied",
					map[string]interface{}{
						"trace_id":           opts.TraceID,
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
				logger.DebugCF("agent", "LLM iteration",
					map[string]interface{}{
						"trace_id":  opts.TraceID,
						"iteration": iteration,
						"max":       al.maxIterations,
					})

				systemPromptLen := 0
				if len(currentMessages) > 0 {
					systemPromptLen = len(currentMessages[0].Content)
				}

				logger.DebugCF("agent", "LLM request",
					map[string]interface{}{
						"trace_id":          opts.TraceID,
						"iteration":         iteration,
						"model":             al.model,
						"messages_count":    len(currentMessages),
						"tools_count":       len(toolDefs),
						"max_tokens":        al.chatOptions.MaxTokens,
						"temperature":       al.chatOptions.Temperature,
						"system_prompt_len": systemPromptLen,
					})

				logger.DebugCF("agent", "Full LLM request",
					map[string]interface{}{
						"trace_id":      opts.TraceID,
						"iteration":     iteration,
						"messages_json": formatMessagesForLog(currentMessages),
						"tools_json":    formatToolsForLog(toolDefs),
					})

				logger.InfoCF("agent", "Calling LLM",
					map[string]interface{}{
						"trace_id":       opts.TraceID,
						"iteration":      iteration,
						"model":          al.model,
						"messages_count": len(currentMessages),
						"tools_count":    len(toolDefs),
					})
			},
			LLMCallFailed: func(iteration int, err error) {
				logger.ErrorCF("agent", "LLM call failed",
					map[string]interface{}{
						"trace_id":  opts.TraceID,
						"iteration": iteration,
						"error":     err.Error(),
					})
			},
			ToolCallsRequested: func(iteration int, toolCalls []providers.ToolCall) {
				toolNames := make([]string, 0, len(toolCalls))
				for _, tc := range toolCalls {
					toolNames = append(toolNames, tc.Name)
				}
				logger.InfoCF("agent", "LLM requested tool calls",
					map[string]interface{}{
						"trace_id":  opts.TraceID,
						"tools":     toolNames,
						"count":     len(toolNames),
						"iteration": iteration,
					})
			},
			DirectResponse: func(iteration int, content string) {
				logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
					map[string]interface{}{
						"trace_id":      opts.TraceID,
						"iteration":     iteration,
						"content_chars": len(content),
					})
			},
			AssistantMessage: func(_ int, msg providers.Message) {
				al.sessions.AddFullMessage(opts.SessionKey, msg)
				_ = al.sessions.Save(al.sessions.GetOrCreate(opts.SessionKey))
			},
			ToolResultMessage: func(_ int, msg providers.Message) {
				al.sessions.AddFullMessage(opts.SessionKey, msg)
				_ = al.sessions.Save(al.sessions.GetOrCreate(opts.SessionKey))
			},
		},
	})
	if err != nil {
		return "", loopRes.Iterations, trackingProvider.maxPromptTokens, deliveredViaMessageTool, fmt.Errorf("LLM call failed: %w", err)
	}

	iteration := loopRes.Iterations
	finalContent := loopRes.FinalContent
	exhausted := loopRes.Exhausted
	messages = loopRes.Messages

	// If the loop exhausted all iterations without a direct answer,
	// make one final LLM call with no tools to get a progress summary.
	// The user can then say "continue" to resume.
	if exhausted {
		logger.WarnCF("agent", "Tool iteration limit reached, requesting summary",
			map[string]interface{}{
				"trace_id":   opts.TraceID,
				"iterations": iteration,
				"max":        al.maxIterations,
			})

		messages = append(messages, providers.Message{
			Role:    "user",
			Content: "You've reached your tool call iteration limit. Please summarize what you've accomplished so far and what still needs to be done. The user can tell you to continue.",
		})

		summaryMessages, summaryBudgetStats := providers.ApplyMessageBudget(messages, al.messageBudget)
		if summaryBudgetStats.Changed() {
			logger.WarnCF("agent", "Summary request payload budget applied",
				map[string]interface{}{
					"trace_id":           opts.TraceID,
					"messages_before":    summaryBudgetStats.InputMessages,
					"messages_after":     summaryBudgetStats.OutputMessages,
					"chars_before":       summaryBudgetStats.CharsBefore,
					"chars_after":        summaryBudgetStats.CharsAfter,
					"truncated_messages": summaryBudgetStats.TruncatedMessages,
					"dropped_messages":   summaryBudgetStats.DroppedMessages,
				})
		}

		response, err := providers.ChatWithTimeout(ctx, al.llmTimeout, al.provider, summaryMessages, nil, al.model, al.chatOptions.ToMap())
		if err != nil {
			logger.ErrorCF("agent", "Summary call failed after iteration limit",
				map[string]interface{}{"error": err.Error(), "trace_id": opts.TraceID})
			finalContent = fmt.Sprintf("I reached my tool call limit (%d iterations) before finishing. Ask me to continue and I'll pick up where I left off.", al.maxIterations)
		} else {
			finalContent = response.Content
			if response.Usage != nil && response.Usage.PromptTokens > trackingProvider.maxPromptTokens {
				trackingProvider.maxPromptTokens = response.Usage.PromptTokens
			}
		}
	}

	return finalContent, iteration, trackingProvider.maxPromptTokens, deliveredViaMessageTool, nil
}

func messageBudgetFromDefaults(d config.AgentDefaults) providers.MessageBudget {
	budget := providers.MessageBudget{}
	if d.RequestMaxMessages > 0 {
		budget.MaxMessages = d.RequestMaxMessages
	}
	if d.RequestMaxTotalChars > 0 {
		budget.MaxTotalChars = d.RequestMaxTotalChars
	}
	if d.RequestMaxMessageChars > 0 {
		budget.MaxMessageChars = d.RequestMaxMessageChars
	}
	if d.RequestMaxToolMessageChars > 0 {
		budget.MaxToolMessageChars = d.RequestMaxToolMessageChars
	}
	return budget
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// When contextWindow is configured, compaction triggers at 75% token usage.
// Otherwise, falls back to a message count heuristic.
func (al *AgentLoop) maybeSummarize(sessionKey string, promptTokens int) {
	newHistory := al.sessions.GetHistory(sessionKey)

	var shouldSummarize bool
	if al.contextWindow > 0 {
		tokenEstimate := promptTokens
		if tokenEstimate <= 0 {
			tokenEstimate = al.estimateTokens(newHistory)
		}
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
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
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
		resp, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: mergePrompt}}, nil, al.model, al.compactOptions.ToMap())
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

	response, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, al.model, al.compactOptions.ToMap())
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
