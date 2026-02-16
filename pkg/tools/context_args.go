package tools

const (
	execContextChannelKey = "__context_channel"
	execContextChatIDKey  = "__context_chat_id"
	execContextTraceIDKey = "__context_trace_id"
)

func withExecutionContext(args map[string]interface{}, channel, chatID, traceID string) map[string]interface{} {
	if channel == "" && chatID == "" && traceID == "" {
		return args
	}

	copyArgs := make(map[string]interface{}, len(args)+3)
	for k, v := range args {
		copyArgs[k] = v
	}
	if channel != "" {
		copyArgs[execContextChannelKey] = channel
	}
	if chatID != "" {
		copyArgs[execContextChatIDKey] = chatID
	}
	if traceID != "" {
		copyArgs[execContextTraceIDKey] = traceID
	}

	return copyArgs
}

func getExecutionContext(args map[string]interface{}) (string, string) {
	channel, _ := args[execContextChannelKey].(string)
	chatID, _ := args[execContextChatIDKey].(string)
	return channel, chatID
}

func getExecutionTraceID(args map[string]interface{}) string {
	traceID, _ := args[execContextTraceIDKey].(string)
	return traceID
}
