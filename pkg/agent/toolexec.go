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

	al.maybeEchoToolCalls(toolCalls, opts.Channel, opts.ChatID)

	results := al.tools.ExecuteToolCalls(ctx, toolCalls, tools.ExecuteToolCallsOptions{
		Channel:      opts.Channel,
		ChatID:       opts.ChatID,
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

	return results
}
