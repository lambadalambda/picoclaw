package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/heartbeat"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/routing"
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

	// Exclude background sessions (cron and heartbeat) - they shouldn't count as user activity
	if routing.IsBackgroundSessionKey(msg.SessionKey) {
		return
	}

	// Record last target for cron jobs
	path := cron.LastTargetPath(al.workspace)
	if err := cron.SaveLastTarget(path, cron.LastTarget{Channel: channel, ChatID: chatID}); err != nil {
		logger.DebugCF("agent", "Failed to record last active target",
			map[string]interface{}{"error": err.Error(), "channel": channel, "chat_id": chatID})
	}

	// Record user activity for heartbeat awareness
	activityPath := heartbeat.ActivityPath(al.workspace)
	if err := heartbeat.SaveActivity(activityPath, heartbeat.Activity{Channel: channel, ChatID: chatID}); err != nil {
		logger.DebugCF("agent", "Failed to record user activity",
			map[string]interface{}{"error": err.Error(), "channel": channel, "chat_id": chatID})
	}
}
