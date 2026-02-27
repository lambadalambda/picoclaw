package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type imageAnalyzer interface {
	AnalyzeImages(ctx context.Context, prompt string, imagePaths []string) (string, error)
}

func (al *AgentLoop) buildUserMessageWithMediaContext(ctx context.Context, content string, media []string, traceID string) (string, []string) {
	base := strings.TrimSpace(content)
	if base == "" {
		base = "[user sent attachments]"
	}

	seen := make(map[string]struct{}, len(media))
	attachments := make([]string, 0, len(media))
	imagePaths := make([]string, 0, len(media))
	for _, raw := range media {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		attachments = append(attachments, path)
		if isImagePath(path) {
			imagePaths = append(imagePaths, path)
		}
	}

	if len(attachments) == 0 {
		return base, nil
	}

	parts := []string{base, "[Attached files]"}
	for _, path := range attachments {
		parts = append(parts, "- "+path)
	}

	if len(imagePaths) == 0 {
		return strings.Join(parts, "\n"), nil
	}

	inlineSupported := al.modelCapabilities.SupportsVision && al.modelCapabilities.SupportsInlineVision
	if inlineSupported && al.provider != nil {
		inlineSupported = providers.SupportsInlineVisionTransport(al.provider, al.model)
	}

	inlineImagePaths := make([]string, 0, len(imagePaths))
	analyzeImagePaths := imagePaths
	if inlineSupported {
		analyzeImagePaths = make([]string, 0, len(imagePaths))
		for _, path := range imagePaths {
			if isInlineTransportImage(path) {
				if err := providers.ValidateInlineImagePath(path); err != nil {
					logger.WarnCF("agent", "Inline image transport unavailable for attachment; using analysis fallback", map[string]interface{}{
						"trace_id": traceID,
						"path":     path,
						"error":    err.Error(),
					})
					analyzeImagePaths = append(analyzeImagePaths, path)
					continue
				}
				inlineImagePaths = append(inlineImagePaths, path)
				continue
			}
			analyzeImagePaths = append(analyzeImagePaths, path)
		}

		if len(analyzeImagePaths) == 0 && len(inlineImagePaths) > 0 {
			return strings.Join(parts, "\n"), inlineImagePaths
		}
	}

	if len(analyzeImagePaths) == 0 {
		return strings.Join(parts, "\n"), inlineImagePaths
	}

	if al.visionAnalyzer == nil {
		parts = append(parts, "", "[Automatic image analysis unavailable: no vision fallback is configured.]")
		return strings.Join(parts, "\n"), inlineImagePaths
	}

	prompt := buildVisionPrompt(content)
	analysis, err := al.visionAnalyzer.AnalyzeImages(ctx, prompt, analyzeImagePaths)
	if err != nil {
		logger.WarnCF("agent", "Automatic image analysis failed", map[string]interface{}{
			"trace_id": traceID,
			"images":   len(analyzeImagePaths),
			"error":    err.Error(),
		})
		parts = append(parts, "", fmt.Sprintf("[Automatic image analysis failed: %s]", utils.Truncate(err.Error(), 200)))
		return strings.Join(parts, "\n"), inlineImagePaths
	}

	parts = append(parts, "", "[Automatic image analysis]", strings.TrimSpace(analysis))
	return strings.Join(parts, "\n"), inlineImagePaths
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
		return strings.HasPrefix(sniffFileContentType(path), "image/")
	}
}

func isInlineTransportImage(path string) bool {
	if providers.SupportsInlineImagePath(path) {
		return true
	}
	// For files without a helpful extension (e.g. DeltaChat paste blobs), sniff
	// the content type and allow common inline formats.
	switch sniffFileContentType(path) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func sniffFileContentType(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return ""
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n <= 0 {
		return ""
	}

	mimeType := http.DetectContentType(buf[:n])
	if semi := strings.Index(mimeType, ";"); semi > 0 {
		mimeType = mimeType[:semi]
	}
	return strings.ToLower(strings.TrimSpace(mimeType))
}
