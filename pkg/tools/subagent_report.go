package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
)

// SubagentReportTool lets a subagent send internal updates to the main agent.
// It publishes a system inbound message that is routed back to the origin chat.
//
// IMPORTANT: This tool does NOT message the end user directly.
type SubagentReportTool struct {
	bus           *bus.MessageBus
	taskID        string
	label         string
	originChannel string
	originChatID  string
}

func NewSubagentReportTool(b *bus.MessageBus, taskID, label, originChannel, originChatID string) *SubagentReportTool {
	return &SubagentReportTool{
		bus:           b,
		taskID:        taskID,
		label:         label,
		originChannel: originChannel,
		originChatID:  originChatID,
	}
}

func (t *SubagentReportTool) Name() string {
	return "subagent_report"
}

func (t *SubagentReportTool) Description() string {
	return "Report progress or intermediate results back to the main agent (internal only). This does NOT message the user."
}

func (t *SubagentReportTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The update to send to the main agent",
			},
			"event": map[string]interface{}{
				"type":        "string",
				"description": "Event type: progress, note, warning, error, complete",
				"enum":        []string{"progress", "note", "warning", "error", "complete"},
			},
			"artifacts": map[string]interface{}{
				"type":        "array",
				"description": "Optional file paths produced by the subagent (images, outputs, etc.)",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		"required": []string{"content"},
	}
}

func (t *SubagentReportTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	event, _ := args["event"].(string)
	if event == "" {
		event = "progress"
	}

	var artifacts []string
	if raw, ok := args["artifacts"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok && s != "" {
					artifacts = append(artifacts, s)
				}
			}
		}
	}

	msgContent := content
	if len(artifacts) > 0 {
		var sb strings.Builder
		sb.WriteString(content)
		sb.WriteString("\n\nArtifacts:\n")
		for _, p := range artifacts {
			sb.WriteString("- ")
			sb.WriteString(p)
			sb.WriteString("\n")
		}
		msgContent = strings.TrimSpace(sb.String())
	}

	if t.bus != nil {
		md := map[string]string{
			"subagent_event":   event,
			"subagent_task_id": t.taskID,
		}
		if t.label != "" {
			md["subagent_label"] = t.label
		}
		chatID := fmt.Sprintf("%s:%s", t.originChannel, t.originChatID)
		t.bus.PublishInbound(bus.InboundMessage{
			Channel:  "system",
			SenderID: fmt.Sprintf("subagent:%s", t.taskID),
			ChatID:   chatID,
			Content:  msgContent,
			Metadata: md,
		})
	}

	return "Reported to main agent", nil
}
