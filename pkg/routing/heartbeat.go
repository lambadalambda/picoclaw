package routing

import "strings"

// HeartbeatTargetRouteFromSessionKey extracts the target route portion from a
// scoped heartbeat session key.
//
// Examples:
// - "heartbeat"                    -> ok=false
// - "heartbeat:telegram:chat1"      -> route="telegram:chat1", ok=true
// - "HEARTBEAT:TeLeGrAm:chat:1"     -> route="TeLeGrAm:chat:1", ok=true
//
// It is intentionally tolerant of case in the "heartbeat" prefix and preserves
// the remainder as-is.
func HeartbeatTargetRouteFromSessionKey(sessionKey string) (route string, ok bool) {
	sk := strings.TrimSpace(sessionKey)
	if sk == "" {
		return "", false
	}

	lower := strings.ToLower(sk)
	if !strings.HasPrefix(lower, "heartbeat:") {
		return "", false
	}

	idx := strings.Index(sk, ":")
	if idx < 0 || idx+1 >= len(sk) {
		return "", false
	}

	route = sk[idx+1:]
	if strings.TrimSpace(route) == "" {
		return "", false
	}
	return route, true
}

// EncodeHeartbeatSessionKey builds a scoped heartbeat session key for a
// specific target chat.
//
// Format: "heartbeat:<channel>:<chat_id>".
func EncodeHeartbeatSessionKey(channel, chatID string) string {
	route := strings.TrimSpace(EncodeSystemRoute(channel, chatID))
	if route == "" {
		return "heartbeat"
	}
	return "heartbeat:" + route
}
