package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
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
	n := len(toolCalls)
	if n == 0 {
		return nil
	}

	// Pre-allocate result slots so goroutines write to independent indices.
	results := make([]providers.Message, n)

	parallelLimit := n
	if al.maxParallelTools > 0 && al.maxParallelTools < parallelLimit {
		parallelLimit = al.maxParallelTools
	}
	sem := make(chan struct{}, parallelLimit)

	// Start status notifier for long-running tool calls (skip for system channel)
	var notifier *statusNotifier
	if al.statusDelay > 0 && opts.Channel != "system" {
		notifier = newStatusNotifier(al.bus, opts.Channel, opts.ChatID, al.statusDelay)
		notifier.start(fmt.Sprintf("%d tools", n))
	}

	var wg sync.WaitGroup
	var completed int32 // accessed only from the progress goroutine below when n>1

	// For multi-tool batches, report progress as each tool finishes.
	doneCh := make(chan int, n) // signals index of completed tool

	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, tc providers.ToolCall) {
			acquired := false
			defer func() {
				if acquired {
					<-sem
				}
				if r := recover(); r != nil {
					result := fmt.Sprintf("Error: tool %s panicked: %v", tc.Name, r)
					logger.ErrorCF("agent", "Recovered panic in tool execution",
						map[string]interface{}{
							"tool":      tc.Name,
							"iteration": iteration,
							"panic":     fmt.Sprintf("%v", r),
						})
					results[idx] = providers.ToolResultMessage(tc.ID, result)
				}
				doneCh <- idx
				wg.Done()
			}()

			select {
			case sem <- struct{}{}:
				acquired = true
			case <-ctx.Done():
				results[idx] = providers.ToolResultMessage(tc.ID, fmt.Sprintf("Error: %v", ctx.Err()))
				return
			}

			// Log tool call
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"tool":      tc.Name,
					"iteration": iteration,
				})

			toolCtx := ctx
			cancel := func() {}
			if al.toolTimeout > 0 {
				toolCtx, cancel = context.WithTimeout(ctx, al.toolTimeout)
			}
			result, err := al.tools.ExecuteWithContext(toolCtx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID)
			cancel()
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			results[idx] = providers.ToolResultMessage(tc.ID, result)
		}(i, tc)
	}

	// Progress reporter: drain doneCh until all tools complete.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		for range n {
			idx := <-doneCh
			completed++
			name := toolCalls[idx].Name
			logger.DebugCF("agent", fmt.Sprintf("Tool completed: %s (%d/%d)", name, completed, n),
				map[string]interface{}{
					"tool":      name,
					"completed": completed,
					"total":     n,
				})
		}
	}()

	wg.Wait()
	<-progressDone // wait for progress reporter to finish publishing

	if notifier != nil {
		notifier.stop()
	}

	return results
}
