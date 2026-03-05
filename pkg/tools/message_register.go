package tools

import (
	"errors"

	"github.com/sipeed/picoclaw/pkg/bus"
)

type MessageToolOptions struct {
	// ForceContextTarget ignores explicit channel/chat_id arguments and forces
	// delivery to the execution context target injected by the tool registry.
	ForceContextTarget bool

	// RestrictMediaToWorkspace enforces that media attachment paths resolve within
	// the configured workspace root.
	RestrictMediaToWorkspace bool
}

// RegisterMessageTool creates and registers a configured message tool.
//
// The caller can control routing and media restrictions via MessageToolOptions.
func RegisterMessageTool(registry *ToolRegistry, msgBus *bus.MessageBus, workspace string, opts MessageToolOptions) *MessageTool {
	tool := NewMessageTool()
	tool.SetWorkspaceRoot(workspace)
	tool.SetForceContextTarget(opts.ForceContextTarget)
	tool.SetRestrictMediaToWorkspace(opts.RestrictMediaToWorkspace)
	tool.SetSendCallback(func(channel, chatID, content string, media []string) error {
		if msgBus == nil {
			return errors.New("message bus not configured")
		}
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
			Media:   media,
		})
		return nil
	})

	if registry != nil {
		registry.Register(tool)
	}
	return tool
}
