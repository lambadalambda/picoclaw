package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/logger"
)

func (al *AgentLoop) recordLastActiveTarget(msg bus.InboundMessage) {
	channel := strings.TrimSpace(msg.Channel)
	chatID := strings.TrimSpace(msg.ChatID)
	if channel == "" || chatID == "" {
		return
	}

	// Exclude internal channels and background sessions.
	if channel == "system" || channel == "cli" {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(msg.SessionKey), "cron-") {
		return
	}

	path := cron.LastTargetPath(al.workspace)
	if err := cron.SaveLastTarget(path, cron.LastTarget{Channel: channel, ChatID: chatID}); err != nil {
		logger.DebugCF("agent", "Failed to record last active target",
			map[string]interface{}{"error": err.Error(), "channel": channel, "chat_id": chatID})
	}
}
