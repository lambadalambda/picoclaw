package agent

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// executeToolsConcurrently runs all tool calls in parallel, collects results
// in call order, and logs per-tool progress. A statusNotifier provides
// periodic "still working" pings for long-running tool batches.
func (al *AgentLoop) executeToolsConcurrently(
	ctx context.Context,
	toolCalls []providers.ToolCall,
	iteration int,
	opts processOptions,
) []providers.Message {
	if len(toolCalls) == 0 {
		return nil
	}

	// Start status notifier for long-running tool calls (skip for system channel)
	var notifier *statusNotifier
	if al.statusDelay > 0 && opts.Channel != "system" {
		notifier = newStatusNotifier(al.bus, opts.Channel, opts.ChatID, al.statusDelay)
		notifier.start(fmt.Sprintf("%d tools", len(toolCalls)))
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
