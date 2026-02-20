package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type imageAnalyzer interface {
	AnalyzeImages(ctx context.Context, prompt string, imagePaths []string) (string, error)
}

func (al *AgentLoop) buildUserMessageWithMediaContext(ctx context.Context, content string, media []string, traceID string) string {
	base := strings.TrimSpace(content)
	if base == "" {
		base = "[user sent attachments]"
	}

	attachments := make([]string, 0, len(media))
	imagePaths := make([]string, 0, len(media))
	for _, raw := range media {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		attachments = append(attachments, path)
		if isImagePath(path) {
			imagePaths = append(imagePaths, path)
		}
	}

	if len(attachments) == 0 {
		return base
	}

	parts := []string{base, "[Attached files]"}
	for _, path := range attachments {
		parts = append(parts, "- "+path)
	}

	if len(imagePaths) == 0 {
		return strings.Join(parts, "\n")
	}

	if al.modelCapabilities.SupportsVision && al.modelCapabilities.SupportsInlineVision {
		return strings.Join(parts, "\n")
	}

	if al.visionAnalyzer == nil {
		parts = append(parts, "", "[Automatic image analysis unavailable: no vision fallback is configured.]")
		return strings.Join(parts, "\n")
	}

	prompt := buildVisionPrompt(content)
	analysis, err := al.visionAnalyzer.AnalyzeImages(ctx, prompt, imagePaths)
	if err != nil {
		logger.WarnCF("agent", "Automatic image analysis failed", map[string]interface{}{
			"trace_id": traceID,
			"images":   len(imagePaths),
			"error":    err.Error(),
		})
		parts = append(parts, "", fmt.Sprintf("[Automatic image analysis failed: %s]", utils.Truncate(err.Error(), 200)))
		return strings.Join(parts, "\n")
	}

	parts = append(parts, "", "[Automatic image analysis]", strings.TrimSpace(analysis))
	return strings.Join(parts, "\n")
}

func buildVisionPrompt(userContent string) string {
	base := "Describe the attached image(s) for a coding assistant. Focus on visible text, errors, code, UI states, and anything relevant to answering the user's request."
	trimmed := strings.TrimSpace(userContent)
	if trimmed == "" {
		return base
	}
	return base + "\n\nUser message: " + trimmed
}

func isImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".heic", ".heif":
		return true
	default:
		return false
	}
}
