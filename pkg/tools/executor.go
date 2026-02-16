package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type ExecuteToolCallsOptions struct {
	Channel     string
	ChatID      string
	Timeout     time.Duration
	MaxParallel int // <=0 means unlimited within this batch

	LogComponent string // default: "tool"
	Iteration    int

	OnToolComplete func(completed, total, index int, call providers.ToolCall, result providers.Message)
}

// ExecuteToolCalls executes a batch of tool calls with optional per-tool timeout
// and bounded parallelism. Results are returned in original call order.
func (r *ToolRegistry) ExecuteToolCalls(
	ctx context.Context,
	toolCalls []providers.ToolCall,
	opts ExecuteToolCallsOptions,
) []providers.Message {
	n := len(toolCalls)
	if n == 0 {
		return nil
	}

	component := opts.LogComponent
	if component == "" {
		component = "tool"
	}

	parallelLimit := n
	if opts.MaxParallel > 0 && opts.MaxParallel < parallelLimit {
		parallelLimit = opts.MaxParallel
	}

	results := make([]providers.Message, n)
	sem := make(chan struct{}, parallelLimit)
	doneCh := make(chan int, n)

	var wg sync.WaitGroup
	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, tc providers.ToolCall) {
			acquired := false
			defer func() {
				if acquired {
					<-sem
				}
				if rec := recover(); rec != nil {
					result := fmt.Sprintf("Error: tool %s panicked: %v", tc.Name, rec)
					logger.ErrorCF(component, "Recovered panic in tool execution",
						map[string]interface{}{
							"tool":      tc.Name,
							"iteration": opts.Iteration,
							"panic":     fmt.Sprintf("%v", rec),
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

			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF(component, fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"tool":      tc.Name,
					"iteration": opts.Iteration,
				})

			toolCtx := ctx
			cancel := func() {}
			if opts.Timeout > 0 {
				toolCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
			}
			result, err := r.ExecuteWithContext(toolCtx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID)
			cancel()
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			results[idx] = providers.ToolResultMessage(tc.ID, result)
		}(i, tc)
	}

	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		completed := 0
		for range n {
			idx := <-doneCh
			completed++
			if opts.OnToolComplete != nil {
				opts.OnToolComplete(completed, n, idx, toolCalls[idx], results[idx])
			}
		}
	}()

	wg.Wait()
	<-progressDone

	return results
}
