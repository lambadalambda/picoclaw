package tools

const (
	execContextChannelKey = "__context_channel"
	execContextChatIDKey  = "__context_chat_id"
)

func withExecutionContext(args map[string]interface{}, channel, chatID string) map[string]interface{} {
	if channel == "" && chatID == "" {
		return args
	}

	copyArgs := make(map[string]interface{}, len(args)+2)
	for k, v := range args {
		copyArgs[k] = v
	}
	if channel != "" {
		copyArgs[execContextChannelKey] = channel
	}
	if chatID != "" {
		copyArgs[execContextChatIDKey] = chatID
	}

	return copyArgs
}

func getExecutionContext(args map[string]interface{}) (string, string) {
	channel, _ := args[execContextChannelKey].(string)
	chatID, _ := args[execContextChatIDKey].(string)
	return channel, chatID
}
