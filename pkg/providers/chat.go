package providers

import (
	"context"
	"time"
)

// ChatWithTimeout wraps provider.Chat with an optional per-call timeout.
// timeout <= 0 means no additional timeout is applied.
func ChatWithTimeout(
	ctx context.Context,
	timeout time.Duration,
	provider LLMProvider,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]interface{},
) (*LLMResponse, error) {
	callCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	return provider.Chat(callCtx, messages, tools, model, options)
}
