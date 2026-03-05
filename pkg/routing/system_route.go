package routing

import "strings"

// EncodeSystemRoute builds the standard routing string used by system messages
// to indicate their originating chat.
//
// Format: "<channel>:<chat_id>".
// The chat_id portion may itself contain colons; decoders should split on the
// first colon only.
func EncodeSystemRoute(channel, chatID string) string {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel == "" {
		return chatID
	}
	return channel + ":" + chatID
}

// DecodeSystemRoute parses a system-route string built by EncodeSystemRoute.
//
// It splits on the first colon. If no valid channel prefix is present, ok is
// false and the full route is returned as chatID.
func DecodeSystemRoute(route string) (channel, chatID string, ok bool) {
	route = strings.TrimSpace(route)
	if route == "" {
		return "", "", false
	}
	if idx := strings.Index(route, ":"); idx > 0 {
		remainder := route[idx+1:]
		if strings.TrimSpace(remainder) == "" {
			return "", route, false
		}
		return route[:idx], remainder, true
	}
	return "", route, false
}
