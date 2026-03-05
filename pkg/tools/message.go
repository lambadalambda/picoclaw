package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type SendCallback func(channel, chatID, content string, media []string) error

type MessageTool struct {
	mu                       sync.RWMutex
	sendCallback             SendCallback
	workspaceRoot            string
	restrictMediaToWorkspace bool
	forceContextTarget       bool
}

func NewMessageTool() *MessageTool {
	return &MessageTool{}
}

func (t *MessageTool) Name() string {
	return "message"
}

func (t *MessageTool) Description() string {
	return "Send a message (and optionally files/images) to a user on a chat channel. " +
		"Use this when you want to communicate something or share files."
}

func (t *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The message content to send",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel (telegram, whatsapp, etc.)",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
			"media": map[string]interface{}{
				"type":        "array",
				"description": "Optional: list of file paths to send as attachments (images, documents, etc.)",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		"required": []string{"content"},
	}
}

func (t *MessageTool) SetSendCallback(callback SendCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendCallback = callback
}

// SetWorkspaceRoot configures the root directory used to resolve relative media
// paths. When set, relative paths like "generated/foo.png" are interpreted as
// workspace-relative and will be converted to absolute paths.
func (t *MessageTool) SetWorkspaceRoot(root string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.workspaceRoot = strings.TrimSpace(root)
}

// SetRestrictMediaToWorkspace enforces that media attachment paths must resolve
// within the configured workspace root. When enabled, absolute paths outside
// the workspace are rejected.
func (t *MessageTool) SetRestrictMediaToWorkspace(restrict bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restrictMediaToWorkspace = restrict
}

// SetForceContextTarget forces messages to be delivered to the execution
// context target (injected via ToolRegistry). When enabled, explicit
// channel/chat_id arguments are ignored.
func (t *MessageTool) SetForceContextTarget(force bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.forceContextTarget = force
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	t.mu.RLock()
	callback := t.sendCallback
	workspaceRoot := t.workspaceRoot
	restrictMedia := t.restrictMediaToWorkspace
	forceTarget := t.forceContextTarget
	t.mu.RUnlock()

	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	ctxChannel, ctxChatID := getExecutionContext(args)
	ctxChannel = strings.TrimSpace(ctxChannel)
	ctxChatID = strings.TrimSpace(ctxChatID)

	if forceTarget {
		channel = ctxChannel
		chatID = ctxChatID
	} else {
		if channel == "" {
			channel = ctxChannel
		}
		if chatID == "" {
			chatID = ctxChatID
		}
	}

	if channel == "" || chatID == "" {
		return "Error: No target channel/chat specified", nil
	}

	if callback == nil {
		return "Error: Message sending not configured", nil
	}

	// Extract media paths
	var media []string
	if rawMedia, ok := args["media"]; ok {
		if mediaList, ok := rawMedia.([]interface{}); ok {
			for _, item := range mediaList {
				if path, ok := item.(string); ok {
					media = append(media, path)
				}
			}
		}
	}
	if media == nil {
		media = []string{}
	}

	// Resolve relative media paths against the workspace root (if configured).
	if len(media) > 0 {
		workspaceRoot = strings.TrimSpace(workspaceRoot)
		if restrictMedia && workspaceRoot == "" {
			return "Error: Message tool workspace root not configured", nil
		}
		resolved := make([]string, 0, len(media))
		for _, p := range media {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if workspaceRoot != "" {
				if restrictMedia {
					abs, err := resolvePathWithOptionalRoot(p, workspaceRoot, "workspace")
					if err != nil {
						return fmt.Sprintf("Error: invalid media path %q: %v", p, err), nil
					}
					resolved = append(resolved, abs)
					continue
				}
				if !filepath.IsAbs(p) {
					abs, err := resolvePathWithOptionalRoot(p, workspaceRoot, "workspace")
					if err != nil {
						return fmt.Sprintf("Error: invalid media path %q: %v", p, err), nil
					}
					resolved = append(resolved, abs)
					continue
				}
				resolved = append(resolved, filepath.Clean(p))
				continue
			}
			// No workspace root restriction configured.
			resolved = append(resolved, filepath.Clean(p))
		}
		media = resolved
	}

	if strings.TrimSpace(content) == "" && len(media) == 0 {
		return "Error: message content or media is required", nil
	}

	if err := callback(channel, chatID, content, media); err != nil {
		return fmt.Sprintf("Error sending message: %v", err), nil
	}

	return fmt.Sprintf("Message sent to %s:%s", channel, chatID), nil
}
