package llmloop

import (
	"context"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type Hooks struct {
	BeforeLLMCall      func(iteration int, messages []providers.Message, toolDefs []providers.ToolDefinition)
	MessagesBudgeted   func(iteration int, stats providers.MessageBudgetStats)
	LLMCallFailed      func(iteration int, err error)
	ToolCallsRequested func(iteration int, toolCalls []providers.ToolCall)
	DirectResponse     func(iteration int, content string)
	AssistantMessage   func(iteration int, msg providers.Message)
	ToolResultMessage  func(iteration int, msg providers.Message)
}

type RunOptions struct {
	Provider      providers.LLMProvider
	Model         string
	MaxIterations int
	LLMTimeout    time.Duration
	ChatOptions   map[string]interface{}
	MessageBudget providers.MessageBudget
	Messages      []providers.Message

	BuildToolDefs func(iteration int, messages []providers.Message) []providers.ToolDefinition
	ExecuteTools  func(ctx context.Context, toolCalls []providers.ToolCall, iteration int) []providers.Message

	Hooks Hooks
}

type RunResult struct {
	Messages     []providers.Message
	FinalContent string
	Iterations   int
	Exhausted    bool
}

// Run executes a standard LLM/tool-call iteration loop.
// It returns the final content when the model stops requesting tools.
// If max iterations are reached while still requesting tools, Exhausted is true.
func Run(ctx context.Context, opts RunOptions) (RunResult, error) {
	result := RunResult{
		Messages:  append([]providers.Message(nil), opts.Messages...),
		Exhausted: true,
	}

	messagesHaveParts := func(messages []providers.Message) bool {
		for _, msg := range messages {
			if len(msg.Parts) > 0 {
				return true
			}
		}
		return false
	}

	stripParts := func(messages []providers.Message) []providers.Message {
		out := make([]providers.Message, len(messages))
		copy(out, messages)
		for i := range out {
			out[i].Parts = nil
		}
		return out
	}

	isLikelyPolicyRefusal := func(err error) bool {
		if err == nil {
			return false
		}
		msg := strings.ToLower(err.Error())
		patterns := []string{
			"content policy",
			"content_policy",
			"policy violation",
			"safety",
			"unsafe",
			"moderation",
			"nsfw",
			"nudity",
			"sexual",
			"explicit",
			"prohibited",
			"disallowed",
			"blocked",
			"rejected",
			"violat",
		}
		for _, pattern := range patterns {
			if strings.Contains(msg, pattern) {
				return true
			}
		}
		return false
	}

	if opts.MaxIterations <= 0 {
		return result, nil
	}

	for iteration := 1; iteration <= opts.MaxIterations; iteration++ {
		result.Iterations = iteration
		requestMessages := result.Messages
		if opts.MessageBudget.Enabled() {
			budgeted, stats := providers.ApplyMessageBudget(result.Messages, opts.MessageBudget)
			requestMessages = budgeted
			if opts.Hooks.MessagesBudgeted != nil && stats.Changed() {
				opts.Hooks.MessagesBudgeted(iteration, stats)
			}
		}

		var toolDefs []providers.ToolDefinition
		if opts.BuildToolDefs != nil {
			toolDefs = opts.BuildToolDefs(iteration, requestMessages)
		}

		if opts.Hooks.BeforeLLMCall != nil {
			opts.Hooks.BeforeLLMCall(iteration, requestMessages, toolDefs)
		}

		resp, err := providers.ChatWithTimeout(
			ctx,
			opts.LLMTimeout,
			opts.Provider,
			requestMessages,
			toolDefs,
			opts.Model,
			opts.ChatOptions,
		)
		if err != nil {
			if messagesHaveParts(requestMessages) && isLikelyPolicyRefusal(err) {
				retryMessages := stripParts(requestMessages)
				retryMessages = append(retryMessages, providers.Message{
					Role:    "system",
					Content: "NOTE: The previous request included image(s), but the provider refused to process them. Retrying without images. Do not guess what is in the image; proceed using text only and ask the user for a description if needed.",
				})
				resp, err = providers.ChatWithTimeout(
					ctx,
					opts.LLMTimeout,
					opts.Provider,
					retryMessages,
					toolDefs,
					opts.Model,
					opts.ChatOptions,
				)
			}

			if err != nil {
				if opts.Hooks.LLMCallFailed != nil {
					opts.Hooks.LLMCallFailed(iteration, err)
				}
				return result, err
			}
		}

		// Detect truncated tool calls (response cut off at max_tokens limit)
		if resp.FinishReason == "length" && len(resp.ToolCalls) > 0 {
			truncatedMsg := providers.AssistantMessageFromResponse(resp)
			result.Messages = append(result.Messages, truncatedMsg)
			if opts.Hooks.AssistantMessage != nil {
				opts.Hooks.AssistantMessage(iteration, truncatedMsg)
			}

			errorMsg := providers.Message{
				Role:    "tool",
				Content: "Error: Your response was truncated at the output token limit. The tool call(s) were incomplete and cannot be executed. Try one of these approaches:\n1. Break the operation into smaller parts (e.g., write smaller files, make multiple calls)\n2. For write_file with large content, consider using exec with heredoc instead\n3. Request a higher output token limit if available",
			}
			if len(resp.ToolCalls) > 0 {
				errorMsg.ToolCallID = resp.ToolCalls[0].ID
			}
			result.Messages = append(result.Messages, errorMsg)
			if opts.Hooks.ToolResultMessage != nil {
				opts.Hooks.ToolResultMessage(iteration, errorMsg)
			}
			continue
		}

		if len(resp.ToolCalls) == 0 {
			result.FinalContent = resp.Content
			result.Exhausted = false
			if opts.Hooks.DirectResponse != nil {
				opts.Hooks.DirectResponse(iteration, result.FinalContent)
			}
			return result, nil
		}

		if opts.Hooks.ToolCallsRequested != nil {
			opts.Hooks.ToolCallsRequested(iteration, resp.ToolCalls)
		}

		assistantMsg := providers.AssistantMessageFromResponse(resp)
		result.Messages = append(result.Messages, assistantMsg)
		if opts.Hooks.AssistantMessage != nil {
			opts.Hooks.AssistantMessage(iteration, assistantMsg)
		}

		var toolResults []providers.Message
		if opts.ExecuteTools != nil {
			toolResults = opts.ExecuteTools(ctx, resp.ToolCalls, iteration)
		}
		for _, tr := range toolResults {
			result.Messages = append(result.Messages, tr)
			if opts.Hooks.ToolResultMessage != nil {
				opts.Hooks.ToolResultMessage(iteration, tr)
			}
		}
	}

	return result, nil
}
