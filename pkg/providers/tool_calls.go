package providers

import (
	"encoding/json"
	"strings"
)

func canonicalizeMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}

	out := make([]Message, len(messages))
	copy(out, messages)

	for i := range out {
		if len(out[i].ToolCalls) == 0 {
			continue
		}
		out[i].ToolCalls = canonicalizeToolCalls(out[i].ToolCalls)
	}

	return out
}

func canonicalizeToolCalls(toolCalls []ToolCall) []ToolCall {
	if len(toolCalls) == 0 {
		return toolCalls
	}

	out := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		out[i] = canonicalizeToolCall(tc)
	}

	return out
}

func canonicalizeToolCall(tc ToolCall) ToolCall {
	normalized := tc

	name := strings.TrimSpace(tc.Name)
	if name == "" && tc.Function != nil {
		name = strings.TrimSpace(tc.Function.Name)
	}

	rawArgs := ""
	if tc.Function != nil {
		rawArgs = strings.TrimSpace(tc.Function.Arguments)
	}

	arguments := tc.Arguments
	if len(arguments) == 0 && rawArgs != "" {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(rawArgs), &parsed); err == nil {
			arguments = parsed
		}
	}
	if arguments == nil {
		arguments = map[string]interface{}{}
	}

	if rawArgs == "" {
		encoded, err := json.Marshal(arguments)
		if err == nil {
			rawArgs = string(encoded)
		} else {
			rawArgs = "{}"
		}
	}

	normalized.Type = "function"
	normalized.Name = name
	normalized.Arguments = arguments
	normalized.Function = &FunctionCall{
		Name:      name,
		Arguments: rawArgs,
	}

	return normalized
}
