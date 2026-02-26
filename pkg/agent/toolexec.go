package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

var sensitivePatterns = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile(`(?i)(authorization\s*:\s*)(bearer\s+|basic\s+|token\s+)?[\w\-._~+/]+=*`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(api[_-]?key|apikey|access[_-]?key|secret[_-]?key|auth[_-]?token|bearer|token)\s*[=:]\s*["']?[\w\-._~+/]{8,}["']?`), "${1}=[REDACTED]"},
	{regexp.MustCompile(`(?i)["']?(api[_-]?key|apikey|access[_-]?key|secret[_-]?key|auth[_-]?token|token|secret|password|passwd)["']?\s*=\s*["']?[\w\-._~+/]{8,}["']?`), "${1}=[REDACTED]"},
	{regexp.MustCompile(`(?i)(signature|sig|x-goog-signature|x-amz-signature|awsaccesskeyid)\s*=\s*[\w\-._~+/]+`), "${1}=[REDACTED]"},
	{regexp.MustCompile(`(?i)(bearer\s+)[\w\-._~+/]{20,}`), "${1}[REDACTED]"},
}

func redactSensitive(s string) string {
	for _, sp := range sensitivePatterns {
		s = sp.pattern.ReplaceAllString(s, sp.replace)
	}
	return s
}

var toolsToEcho = map[string]bool{
	"exec":          true,
	"edit_file":     true,
	"write_file":    true,
	"read_file":     true,
	"list_dir":      true,
	"web_search":    true,
	"web_fetch":     true,
	"spawn":         true,
	"memory_store":  true,
	"memory_search": true,
	"compact":       true,
}

func formatToolCallSummary(tc providers.ToolCall) string {
	description := redactSensitive(extractToolCallDescription(tc))
	keyParam := extractKeyParam(tc.Name, tc.Arguments)
	keyParam = redactSensitive(keyParam)

	if description != "" && keyParam != "" {
		return fmt.Sprintf("%s - %s (%s)", tc.Name, description, keyParam)
	}
	if description != "" {
		return fmt.Sprintf("%s - %s", tc.Name, description)
	}
	if keyParam != "" {
		return fmt.Sprintf("%s %s", tc.Name, keyParam)
	}
	return tc.Name
}

func extractToolCallDescription(tc providers.ToolCall) string {
	description := strings.TrimSpace(tc.Description)
	if description != "" {
		return description
	}

	raw, ok := tc.Arguments["description"]
	if !ok {
		return ""
	}
	argDescription, ok := raw.(string)
	if !ok {
		return ""
	}
	argDescription = strings.TrimSpace(argDescription)
	if argDescription == "" {
		return ""
	}
	if len(argDescription) > 80 {
		return argDescription[:77] + "..."
	}
	return argDescription
}

func extractKeyParam(toolName string, args map[string]interface{}) string {
	switch toolName {
	case "exec":
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > 60 {
				return cmd[:57] + "..."
			}
			return cmd
		}
	case "edit_file", "read_file", "write_file", "list_dir":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "web_search":
		if query, ok := args["query"].(string); ok {
			if len(query) > 50 {
				return fmt.Sprintf("%q", query[:47]+"...")
			}
			return fmt.Sprintf("%q", query)
		}
	case "web_fetch":
		if url, ok := args["url"].(string); ok {
			if len(url) > 60 {
				return url[:57] + "..."
			}
			return url
		}
	case "spawn":
		if task, ok := args["task"].(string); ok {
			if len(task) > 50 {
				return task[:47] + "..."
			}
			return task
		}
	case "memory_store", "memory_search":
		if content, ok := args["content"].(string); ok {
			if len(content) > 50 {
				return content[:47] + "..."
			}
			return content
		}
		if query, ok := args["query"].(string); ok {
			if len(query) > 50 {
				return query[:47] + "..."
			}
			return query
		}
	case "compact":
		if mode, ok := args["mode"].(string); ok {
			return mode
		}
	}
	return ""
}

func (al *AgentLoop) maybeEchoToolCalls(toolCalls []providers.ToolCall, channel, chatID string) {
	if !al.echoToolCalls || channel == "system" {
		return
	}

	var summaries []string
	for _, tc := range toolCalls {
		if toolsToEcho[tc.Name] {
			summaries = append(summaries, formatToolCallSummary(tc))
		}
	}

	if len(summaries) == 0 {
		return
	}

	content := "🔧 " + strings.Join(summaries, "\n🔧 ")
	al.bus.PublishOutbound(bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: content,
	})
}

func (al *AgentLoop) executeToolsConcurrently(
	ctx context.Context,
	toolCalls []providers.ToolCall,
	iteration int,
	opts processOptions,
) []providers.Message {
	if len(toolCalls) == 0 {
		return nil
	}

	// Provide session context to tools (notably spawn) so they can route
	// background work appropriately (e.g., heartbeat-spawned subagents).
	if strings.TrimSpace(opts.SessionKey) != "" {
		for i := range toolCalls {
			if toolCalls[i].Arguments == nil {
				toolCalls[i].Arguments = map[string]interface{}{}
			}
			if _, exists := toolCalls[i].Arguments["__context_session_key"]; !exists {
				toolCalls[i].Arguments["__context_session_key"] = opts.SessionKey
			}
		}
	}

	al.maybeEchoToolCalls(toolCalls, opts.Channel, opts.ChatID)

	results := al.tools.ExecuteToolCalls(ctx, toolCalls, tools.ExecuteToolCallsOptions{
		Channel:      opts.Channel,
		ChatID:       opts.ChatID,
		SessionKey:   opts.SessionKey,
		TraceID:      opts.TraceID,
		Timeout:      al.toolTimeout,
		MaxParallel:  al.maxParallelTools,
		LogComponent: "agent",
		Iteration:    iteration,
		OnToolComplete: func(completed, total, index int, call providers.ToolCall, _ providers.Message) {
			logger.DebugCF("agent", fmt.Sprintf("Tool completed: %s (%d/%d)", call.Name, completed, total),
				map[string]interface{}{
					"tool":      call.Name,
					"completed": completed,
					"total":     total,
					"trace_id":  opts.TraceID,
				})
		},
	})

	// If the message tool sent user-facing output to a different session
	// (e.g., heartbeat/cron background sessions), mirror that content into the
	// target chat session history so the main agent can understand follow-ups.
	al.mirrorMessageToolSends(toolCalls, results, opts)

	return results
}

func (al *AgentLoop) mirrorMessageToolSends(toolCalls []providers.ToolCall, results []providers.Message, opts processOptions) {
	if al == nil || al.sessions == nil {
		return
	}
	if len(toolCalls) == 0 || len(results) == 0 {
		return
	}

	for i, tc := range toolCalls {
		if strings.ToLower(strings.TrimSpace(tc.Name)) != "message" {
			continue
		}
		if i >= len(results) {
			continue
		}
		if !messageToolResultLooksSuccessful(results[i]) {
			continue
		}

		args := tc.Arguments
		content := firstStringArg(args, "content", "text", "message")
		channel := firstStringArg(args, "channel")
		chatID := firstStringArg(args, "chat_id", "chatId", "chatID", "target", "target_id", "targetId")
		if channel == "" {
			channel = strings.TrimSpace(opts.Channel)
		}
		if chatID == "" {
			chatID = strings.TrimSpace(opts.ChatID)
		}
		if channel == "" || chatID == "" {
			continue
		}

		targetSessionKey := fmt.Sprintf("%s:%s", channel, chatID)
		if strings.TrimSpace(targetSessionKey) == "" {
			continue
		}
		if strings.TrimSpace(opts.SessionKey) == strings.TrimSpace(targetSessionKey) {
			continue
		}

		media := stringSliceArg(args, "media")
		mirrored := formatMirroredMessageContent(content, media)
		al.sessions.AddMessage(targetSessionKey, "assistant", mirrored)
		_ = al.sessions.Save(al.sessions.GetOrCreate(targetSessionKey))

		logger.DebugCF("agent", "Mirrored outbound message tool send to session history",
			map[string]interface{}{
				"from_session": opts.SessionKey,
				"to_session":   targetSessionKey,
				"channel":      channel,
				"chat_id":      chatID,
				"trace_id":     opts.TraceID,
			})
	}
}

func messageToolResultLooksSuccessful(msg providers.Message) bool {
	if msg.Role != "tool" {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(msg.Content), "Message sent to ")
}

func firstStringArg(args map[string]interface{}, keys ...string) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		// Fast path: direct lookup.
		if raw, ok := args[key]; ok {
			if s, ok := raw.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
		// Slow path: case-insensitive lookup.
		for k, raw := range args {
			if !strings.EqualFold(k, key) {
				continue
			}
			if s, ok := raw.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func stringSliceArg(args map[string]interface{}, key string) []string {
	if len(args) == 0 || strings.TrimSpace(key) == "" {
		return nil
	}

	raw, ok := args[key]
	if !ok {
		// Case-insensitive lookup
		for k, v := range args {
			if strings.EqualFold(k, key) {
				raw = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		return []string{s}
	default:
		return nil
	}
}

func formatMirroredMessageContent(content string, media []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(sent a message)"
	}
	if len(media) == 0 {
		return content
	}
	if len(media) == 1 {
		return content + "\n\n[Media: " + media[0] + "]"
	}
	return content + "\n\n[Media: " + strings.Join(media, ", ") + "]"
}
