package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// executeToolsConcurrently runs all tool calls in parallel, collects results
// in call order, and sends per-tool progress to the bus. A statusNotifier
// provides periodic "still working" pings as a fallback for very long tools.
func (al *AgentLoop) executeToolsConcurrently(
	ctx context.Context,
	toolCalls []providers.ToolCall,
	iteration int,
	opts processOptions,
) []providers.Message {
	n := len(toolCalls)

	// Pre-allocate result slots so goroutines write to independent indices.
	results := make([]providers.Message, n)

	// Start status notifier for long-running tool calls (skip for system channel)
	var notifier *statusNotifier
	sendProgress := opts.Channel != "system"
	if al.statusDelay > 0 && sendProgress {
		names := make([]string, n)
		for i, tc := range toolCalls {
			names[i] = tc.Name
		}
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
			defer wg.Done()

			// Log tool call
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"tool":      tc.Name,
					"iteration": iteration,
				})

			result, err := al.tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			results[idx] = providers.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			}

			doneCh <- idx
		}(i, tc)
	}

	// Progress reporter: drain doneCh until all tools complete.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		for range n {
			idx := <-doneCh
			completed++
			if sendProgress && n > 1 {
				name := toolCalls[idx].Name
				msg := fmt.Sprintf("%s done (%d/%d)", name, completed, n)
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: msg,
				})
			}
		}
	}()

	wg.Wait()
	<-progressDone // wait for progress reporter to finish publishing

	if notifier != nil {
		notifier.stop()
	}

	return results
}
