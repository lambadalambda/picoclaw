package routing

import "strings"

// IsHeartbeatSessionKey reports whether the session key refers to a heartbeat
// context.
//
// Current conventions:
// - "heartbeat"
// - "heartbeat:<channel>:<chat_id>" (the remainder may include additional colons)
func IsHeartbeatSessionKey(sessionKey string) bool {
	sk := strings.ToLower(strings.TrimSpace(sessionKey))
	return sk == "heartbeat" || strings.HasPrefix(sk, "heartbeat:")
}

// IsCronSessionKey reports whether the session key refers to a cron/background
// context.
//
// Current conventions:
// - "cron"
// - "cron:<...>"
// - "cron-<...>"
func IsCronSessionKey(sessionKey string) bool {
	sk := strings.ToLower(strings.TrimSpace(sessionKey))
	return sk == "cron" || strings.HasPrefix(sk, "cron-") || strings.HasPrefix(sk, "cron:")
}

// IsBackgroundSessionKey reports whether the session key refers to a
// non-interactive/background context (cron or heartbeat).
func IsBackgroundSessionKey(sessionKey string) bool {
	if strings.TrimSpace(sessionKey) == "" {
		return false
	}
	return IsHeartbeatSessionKey(sessionKey) || IsCronSessionKey(sessionKey)
}
