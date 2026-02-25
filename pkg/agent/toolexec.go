package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

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
	keyParam := extractKeyParam(tc.Name, tc.Arguments)
	if keyParam != "" {
		return fmt.Sprintf("%s %s", tc.Name, keyParam)
	}
	return tc.Name
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
		if desc, ok := args["description"].(string); ok {
			if len(desc) > 50 {
				return desc[:47] + "..."
			}
			return desc
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

	var notifier *statusNotifier
	if al.statusDelay > 0 && opts.Channel != "system" {
		notifier = newStatusNotifier(al.bus, opts.Channel, opts.ChatID, al.statusDelay)
		notifier.start(fmt.Sprintf("%d tools", len(toolCalls)))
		notifier.setProgress(0, len(toolCalls))
	}

	results := al.tools.ExecuteToolCalls(ctx, toolCalls, tools.ExecuteToolCallsOptions{
		Channel:      opts.Channel,
		ChatID:       opts.ChatID,
		TraceID:      opts.TraceID,
		Timeout:      al.toolTimeout,
		MaxParallel:  al.maxParallelTools,
		LogComponent: "agent",
		Iteration:    iteration,
		OnToolComplete: func(completed, total, index int, call providers.ToolCall, _ providers.Message) {
			if notifier != nil {
				notifier.setProgress(completed, total)
			}
			logger.DebugCF("agent", fmt.Sprintf("Tool completed: %s (%d/%d)", call.Name, completed, total),
				map[string]interface{}{
					"tool":      call.Name,
					"completed": completed,
					"total":     total,
					"trace_id":  opts.TraceID,
				})
		},
	})

	if notifier != nil {
		notifier.stop()
	}

	return results
}
