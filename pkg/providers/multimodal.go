package providers

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxInlineImageBytes = 8 * 1024 * 1024

type inlineImageData struct {
	Path       string
	MediaType  string
	Base64Data string
	DataURL    string
}

func SupportsInlineVisionTransport(provider LLMProvider, model string) bool {
	caps := ModelCapabilitiesFor(model)
	if !caps.SupportsVision || !caps.SupportsInlineVision {
		return false
	}
	return providerSupportsInlineVision(provider, model)
}

func providerSupportsInlineVision(provider LLMProvider, model string) bool {
	if provider == nil {
		return false
	}

	switch p := provider.(type) {
	case *HTTPProvider, *ClaudeProvider, *CodexProvider:
		return true
	case *fallbackProvider:
		ordered := p.orderedCandidates(model)
		if len(ordered) == 0 {
			ordered = append([]fallbackCandidate(nil), p.candidates...)
		}

		requested := strings.ToLower(strings.TrimSpace(model))
		if requested != "" {
			for _, candidate := range ordered {
				if strings.EqualFold(strings.TrimSpace(candidate.model), model) {
					return providerSupportsInlineVision(candidate.provider, candidate.model)
				}
			}
		}

		for _, candidate := range ordered {
			if providerSupportsInlineVision(candidate.provider, candidate.model) {
				return true
			}
		}
	}

	return false
}

func SupportsInlineImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func ValidateInlineImagePath(path string) error {
	_, err := loadInlineImageData(path, "")
	return err
}

func inlineImageDataFromPart(part MessagePart) (inlineImageData, error) {
	if part.Type != "" && part.Type != MessagePartTypeImage {
		return inlineImageData{}, fmt.Errorf("unsupported message part type %q", part.Type)
	}

	path := strings.TrimSpace(part.Path)
	if path == "" {
		return inlineImageData{}, fmt.Errorf("image part path is empty")
	}

	return loadInlineImageData(path, part.MediaType)
}

func loadInlineImageData(path string, mediaTypeHint string) (inlineImageData, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return inlineImageData{}, fmt.Errorf("image path is empty")
	}

	st, err := os.Stat(path)
	if err != nil {
		return inlineImageData{}, fmt.Errorf("stat image %q: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return inlineImageData{}, fmt.Errorf("image %q is not a regular file", path)
	}
	if st.Size() > maxInlineImageBytes {
		return inlineImageData{}, fmt.Errorf("image %q exceeds inline max size of %d bytes", path, maxInlineImageBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return inlineImageData{}, fmt.Errorf("read image %q: %w", path, err)
	}
	if len(data) == 0 {
		return inlineImageData{}, fmt.Errorf("image %q is empty", path)
	}

	mediaType := detectInlineImageMediaType(path, data, mediaTypeHint)
	if !strings.HasPrefix(mediaType, "image/") {
		return inlineImageData{}, fmt.Errorf("file %q is not a supported image", path)
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return inlineImageData{
		Path:       path,
		MediaType:  mediaType,
		Base64Data: encoded,
		DataURL:    fmt.Sprintf("data:%s;base64,%s", mediaType, encoded),
	}, nil
}

func detectInlineImageMediaType(path string, data []byte, mediaTypeHint string) string {
	hint := strings.TrimSpace(strings.ToLower(mediaTypeHint))
	if strings.HasPrefix(hint, "image/") {
		return hint
	}

	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); byExt != "" {
		if semi := strings.Index(byExt, ";"); semi > 0 {
			byExt = byExt[:semi]
		}
		if strings.HasPrefix(byExt, "image/") {
			return byExt
		}
	}

	if len(data) > 0 {
		detected := http.DetectContentType(data)
		if semi := strings.Index(detected, ";"); semi > 0 {
			detected = detected[:semi]
		}
		if strings.HasPrefix(detected, "image/") {
			return detected
		}
	}

	return ""
}
