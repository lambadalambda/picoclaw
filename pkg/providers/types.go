package providers

import "context"

type ToolCall struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type,omitempty"`
	Function    *FunctionCall          `json:"function,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Arguments   map[string]interface{} `json:"arguments,omitempty"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type LLMResponse struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason"`
	Usage        *UsageInfo `json:"usage,omitempty"`
}

type UsageInfo struct {
	Provider                            string `json:"provider,omitempty"`
	PromptTokens                        int    `json:"prompt_tokens"`
	CompletionTokens                    int    `json:"completion_tokens"`
	TotalTokens                         int    `json:"total_tokens"`
	InputTokens                         int    `json:"input_tokens,omitempty"`
	OutputTokens                        int    `json:"output_tokens,omitempty"`
	CacheReadInputTokens                int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens            int    `json:"cache_creation_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int    `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int    `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CachedPromptTokens                  int    `json:"cached_prompt_tokens,omitempty"`
}

type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	Parts      []MessagePart `json:"-"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type MessagePartType string

const (
	MessagePartTypeImage MessagePartType = "image"
)

type MessagePart struct {
	Type      MessagePartType `json:"type,omitempty"`
	Path      string          `json:"path,omitempty"`
	MediaType string          `json:"media_type,omitempty"`
}

type LLMProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error)
	GetDefaultModel() string
}

// AssistantMessageFromResponse builds a Message suitable for appending to the
// conversation history from an LLM response that contains tool calls.
// The returned message has Role "assistant" and carries the response's tool
// calls in the canonical OpenAI wire format (Type + Function populated).
func AssistantMessageFromResponse(resp *LLMResponse) Message {
	if resp == nil {
		return Message{Role: "assistant"}
	}

	return Message{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: canonicalizeToolCalls(resp.ToolCalls),
	}
}

// ToolResultMessage builds a "tool" role message for a single tool call result.
func ToolResultMessage(toolCallID, result string) Message {
	return Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	}
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}
