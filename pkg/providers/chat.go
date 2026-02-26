package providers

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const chatTimeoutRetryAttempts = 2

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
	attempts := 1
	if timeout > 0 {
		attempts = chatTimeoutRetryAttempts
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		callCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, timeout)
		}

		resp, err := provider.Chat(callCtx, messages, tools, model, options)
		cancel()
		if err == nil {
			return resp, nil
		}

		if attempt == attempts || ctx.Err() != nil || !isTimeoutRetryableError(err) {
			return nil, err
		}

		logger.WarnCF("provider", "Retrying timed-out LLM call",
			map[string]interface{}{
				"attempt":      attempt + 1,
				"max_attempts": attempts,
				"timeout":      timeout.String(),
				"error":        err.Error(),
			})
	}

	return nil, context.DeadlineExceeded
}

func isTimeoutRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded")
}
