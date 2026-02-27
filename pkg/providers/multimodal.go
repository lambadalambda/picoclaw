package providers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxInlineImageBytes = 8 * 1024 * 1024

const (
	inlineResizedImageMaxBytes     = 900 * 1024
	inlineResizedImageMaxDimension = 1024
	inlineResizedImageMinDimension = 256
)

var inlineResizedJPEGQualities = []int{85, 75, 65, 55}

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

	if len(data) > inlineResizedImageMaxBytes {
		resized, resizedType, didResize, resizeErr := maybeShrinkInlineImage(path, mediaType, data)
		if resizeErr != nil {
			return inlineImageData{}, resizeErr
		}
		if didResize {
			logger.DebugCF("provider", "Shrunk inline image for multimodal transport", map[string]interface{}{
				"path":          path,
				"media_type":    mediaType,
				"resized_type":  resizedType,
				"bytes_before":  len(data),
				"bytes_after":   len(resized),
				"target_bytes":  inlineResizedImageMaxBytes,
				"max_dimension": inlineResizedImageMaxDimension,
			})
			data = resized
			mediaType = resizedType
		}
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return inlineImageData{
		Path:       path,
		MediaType:  mediaType,
		Base64Data: encoded,
		DataURL:    fmt.Sprintf("data:%s;base64,%s", mediaType, encoded),
	}, nil
}

func maybeShrinkInlineImage(path string, mediaType string, data []byte) ([]byte, string, bool, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if len(data) <= inlineResizedImageMaxBytes {
		return data, mediaType, false, nil
	}

	switch mediaType {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		// ok
	default:
		// Unknown image format; best-effort keep. If it's too large, refuse to
		// inline to avoid request-size failures.
		return nil, "", false, fmt.Errorf("image %q is too large to inline (%d bytes) and cannot be resized (media_type=%q)", path, len(data), mediaType)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", false, fmt.Errorf("decode image %q for resize: %w", path, err)
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil, "", false, fmt.Errorf("invalid image dimensions for %q", path)
	}

	startDim := inlineResizedImageMaxDimension
	if max := maxInt(w, h); max < startDim {
		startDim = max
	}
	if startDim < inlineResizedImageMinDimension {
		startDim = inlineResizedImageMinDimension
	}

	bestBytes := []byte(nil)
	bestSize := int(^uint(0) >> 1)
	bestDim := 0
	bestQuality := 0

	dim := startDim
	for dim >= inlineResizedImageMinDimension {
		scaled := scaleToMaxDimension(img, dim)
		rgba := flattenToOpaqueRGBA(scaled)

		for _, quality := range inlineResizedJPEGQualities {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: quality}); err != nil {
				continue
			}
			if buf.Len() < bestSize {
				bestSize = buf.Len()
				bestBytes = append([]byte(nil), buf.Bytes()...)
				bestDim = dim
				bestQuality = quality
			}
			if buf.Len() <= inlineResizedImageMaxBytes {
				_ = bestDim
				_ = bestQuality
				return buf.Bytes(), "image/jpeg", true, nil
			}
		}

		dim = int(float64(dim) * 0.85)
	}

	if len(bestBytes) > 0 && len(bestBytes) <= inlineResizedImageMaxBytes {
		return bestBytes, "image/jpeg", true, nil
	}
	if len(bestBytes) > 0 {
		return nil, "", false, fmt.Errorf("image %q is too large to inline after resizing (best=%d bytes, target=%d bytes)", path, len(bestBytes), inlineResizedImageMaxBytes)
	}
	return nil, "", false, fmt.Errorf("unable to shrink image %q for inline transport", path)
}

func scaleToMaxDimension(img image.Image, maxDim int) image.Image {
	if img == nil {
		return img
	}
	if maxDim <= 0 {
		return img
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return img
	}
	if maxInt(w, h) <= maxDim {
		return img
	}

	newW, newH := w, h
	if w >= h {
		newW = maxDim
		newH = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		newH = maxDim
		newW = int(float64(w) * float64(maxDim) / float64(h))
	}
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

func flattenToOpaqueRGBA(img image.Image) *image.RGBA {
	if img == nil {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Over)
	return rgba
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
