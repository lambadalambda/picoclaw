package tools

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// ToolResult is an optional richer return type for tools.
// Content is always safe to return as plain text.
// Parts may include runtime-only multimodal attachments (e.g., images) that
// certain providers can send inline to multimodal models.
type ToolResult struct {
	Content string
	Parts   []providers.MessagePart
}

// ToolWithResult is an optional extension interface.
// Tools that implement this can return structured attachments alongside text.
type ToolWithResult interface {
	Tool
	ExecuteResult(ctx context.Context, args map[string]interface{}) (ToolResult, error)
}
