package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	defaultImageInspectMaxImages       = 3
	defaultImageInspectMaxBytes        = 8 * 1024 * 1024
	defaultImageInspectDownloadTimeout = 20 * time.Second
	imageInspectRedirectLimit          = 4
)

type ImageAnalyzer interface {
	AnalyzeImages(ctx context.Context, prompt string, imagePaths []string) (string, error)
}

type ImageInspectTool struct {
	workspace         string
	primaryAnalyzer   ImageAnalyzer
	primaryLabel      string
	fallbackAnalyzer  ImageAnalyzer
	fallbackLabel     string
	maxImages         int
	maxBytes          int64
	downloadTimeout   time.Duration
	allowPrivateHosts bool
	httpClient        *http.Client
}

type imageInspectSource struct {
	Input string
	Kind  string
	Path  string
	MIME  string
	Bytes int
}

type namedImageAnalyzer struct {
	name     string
	analyzer ImageAnalyzer
}

func NewImageInspectTool(workspace string, primaryAnalyzer ImageAnalyzer, primaryLabel string, fallbackAnalyzer ImageAnalyzer, fallbackLabel string) *ImageInspectTool {
	return &ImageInspectTool{
		workspace:        workspace,
		primaryAnalyzer:  primaryAnalyzer,
		primaryLabel:     strings.TrimSpace(primaryLabel),
		fallbackAnalyzer: fallbackAnalyzer,
		fallbackLabel:    strings.TrimSpace(fallbackLabel),
		maxImages:        defaultImageInspectMaxImages,
		maxBytes:         defaultImageInspectMaxBytes,
		downloadTimeout:  defaultImageInspectDownloadTimeout,
	}
}

func (t *ImageInspectTool) Name() string {
	return "image_inspect"
}

func (t *ImageInspectTool) Description() string {
	return "Inspect image URLs or local image files and return a detailed visual analysis"
}

func (t *ImageInspectTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sources": map[string]interface{}{
				"type":        "array",
				"description": "Image sources to inspect (local file paths and/or http/https URLs)",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"question": map[string]interface{}{
				"type":        "string",
				"description": "Optional focus prompt for the analysis (e.g., OCR text, UI errors, layout issues)",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Analyzer selection mode: auto (default), primary, or fallback",
				"enum":        []string{"auto", "primary", "fallback"},
			},
			"max_images": map[string]interface{}{
				"type":        "integer",
				"description": "Optional max number of images to inspect from sources (default 3)",
			},
		},
		"required": []string{"sources"},
	}
}

func (t *ImageInspectTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	sources, err := t.parseSources(args)
	if err != nil {
		return "", err
	}

	mode := "auto"
	if rawMode, ok := args["mode"].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(rawMode))
	}
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "primary" && mode != "fallback" {
		return "", fmt.Errorf("mode must be one of: auto, primary, fallback")
	}

	maxImages := t.maxImages
	if maxImages <= 0 {
		maxImages = defaultImageInspectMaxImages
	}
	if rawMaxImages, exists := args["max_images"]; exists && rawMaxImages != nil {
		parsedMaxImages, parseErr := parseOptionalIntArg(args, "max_images", maxImages)
		if parseErr != nil {
			return "", parseErr
		}
		if parsedMaxImages <= 0 {
			return "", fmt.Errorf("max_images must be >= 1")
		}
		maxImages = parsedMaxImages
	}

	if len(sources) > maxImages {
		sources = sources[:maxImages]
	}

	prepared, warnings, cleanup, err := t.prepareSources(ctx, sources)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if len(prepared) == 0 {
		if len(warnings) == 0 {
			return "", fmt.Errorf("no valid image sources found")
		}
		return "", fmt.Errorf("no valid image sources found: %s", strings.Join(warnings, "; "))
	}

	prompt := t.buildPrompt(args)
	imagePaths := make([]string, 0, len(prepared))
	for _, item := range prepared {
		imagePaths = append(imagePaths, item.Path)
	}

	analysis, backendName, backendErr := t.runAnalysis(ctx, mode, prompt, imagePaths)
	if backendErr != nil {
		return "", backendErr
	}

	if strings.TrimSpace(analysis) == "" {
		return "", fmt.Errorf("image analysis returned empty output")
	}

	return formatImageInspectResult(backendName, prepared, analysis, warnings), nil
}

func (t *ImageInspectTool) parseSources(args map[string]interface{}) ([]string, error) {
	rawSources, ok := args["sources"]
	if !ok {
		return nil, fmt.Errorf("sources is required")
	}

	var out []string
	switch v := rawSources.(type) {
	case []interface{}:
		for _, item := range v {
			source, ok := item.(string)
			if !ok {
				continue
			}
			source = strings.TrimSpace(source)
			if source != "" {
				out = append(out, source)
			}
		}
	case []string:
		for _, item := range v {
			source := strings.TrimSpace(item)
			if source != "" {
				out = append(out, source)
			}
		}
	default:
		return nil, fmt.Errorf("sources must be an array of strings")
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("at least one source is required")
	}

	return out, nil
}

func (t *ImageInspectTool) buildPrompt(args map[string]interface{}) string {
	if q, ok := args["question"].(string); ok {
		q = strings.TrimSpace(q)
		if q != "" {
			return "Analyze the provided image(s) and answer this request: " + q
		}
	}

	return "Analyze the provided image(s). Describe visible content, transcribe important text, and highlight details useful for debugging, coding, or UI analysis."
}

func (t *ImageInspectTool) availableAnalyzers(mode string) ([]namedImageAnalyzer, error) {
	primary := namedImageAnalyzer{name: t.primaryLabel, analyzer: t.primaryAnalyzer}
	if strings.TrimSpace(primary.name) == "" {
		primary.name = "primary"
	}

	fallback := namedImageAnalyzer{name: t.fallbackLabel, analyzer: t.fallbackAnalyzer}
	if strings.TrimSpace(fallback.name) == "" {
		fallback.name = "fallback"
	}

	switch mode {
	case "primary":
		if primary.analyzer == nil {
			return nil, fmt.Errorf("primary image analyzer is not configured")
		}
		return []namedImageAnalyzer{primary}, nil
	case "fallback":
		if fallback.analyzer == nil {
			return nil, fmt.Errorf("fallback image analyzer is not configured")
		}
		return []namedImageAnalyzer{fallback}, nil
	default:
		analyzers := make([]namedImageAnalyzer, 0, 2)
		if primary.analyzer != nil {
			analyzers = append(analyzers, primary)
		}
		if fallback.analyzer != nil {
			if len(analyzers) == 0 || fallback.name != analyzers[0].name {
				analyzers = append(analyzers, fallback)
			}
		}
		if len(analyzers) == 0 {
			return nil, fmt.Errorf("no image analyzer is configured")
		}
		return analyzers, nil
	}
}

func (t *ImageInspectTool) runAnalysis(ctx context.Context, mode, prompt string, imagePaths []string) (analysis string, backend string, err error) {
	analyzers, err := t.availableAnalyzers(mode)
	if err != nil {
		return "", "", err
	}

	var firstErr error
	for index, candidate := range analyzers {
		result, runErr := candidate.analyzer.AnalyzeImages(ctx, prompt, imagePaths)
		if runErr == nil {
			return strings.TrimSpace(result), candidate.name, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s analyzer failed: %w", candidate.name, runErr)
		}
		if mode != "auto" || index == len(analyzers)-1 {
			break
		}
	}

	if firstErr == nil {
		firstErr = fmt.Errorf("image analysis failed")
	}
	return "", "", firstErr
}

func (t *ImageInspectTool) prepareSources(ctx context.Context, sources []string) (prepared []imageInspectSource, warnings []string, cleanup func(), err error) {
	prepared = make([]imageInspectSource, 0, len(sources))
	warnings = make([]string, 0)
	downloadedPaths := make([]string, 0)

	cleanup = func() {
		for _, downloaded := range downloadedPaths {
			_ = os.Remove(downloaded)
		}
	}

	for _, raw := range sources {
		source := strings.TrimSpace(raw)
		if source == "" {
			continue
		}

		var entry imageInspectSource
		var prepErr error
		if isHTTPURL(source) {
			entry, prepErr = t.prepareURLSource(ctx, source)
			if prepErr == nil {
				downloadedPaths = append(downloadedPaths, entry.Path)
			}
		} else {
			entry, prepErr = t.prepareLocalSource(source)
		}

		if prepErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s (%v)", source, prepErr))
			continue
		}

		prepared = append(prepared, entry)
	}

	return prepared, warnings, cleanup, nil
}

func (t *ImageInspectTool) prepareLocalSource(source string) (imageInspectSource, error) {
	resolved := source
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(t.workspace, resolved)
	}

	absPath, err := filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return imageInspectSource{}, fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return imageInspectSource{}, fmt.Errorf("stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return imageInspectSource{}, fmt.Errorf("not a regular file")
	}
	if info.Size() > t.maxBytes {
		return imageInspectSource{}, fmt.Errorf("file exceeds max size (%d bytes)", t.maxBytes)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return imageInspectSource{}, fmt.Errorf("read file: %w", err)
	}
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return imageInspectSource{}, fmt.Errorf("file is not an image (detected %s)", mimeType)
	}

	return imageInspectSource{Input: source, Kind: "path", Path: absPath, MIME: mimeType, Bytes: len(data)}, nil
}

func (t *ImageInspectTool) prepareURLSource(ctx context.Context, source string) (imageInspectSource, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return imageInspectSource{}, fmt.Errorf("invalid URL: %w", err)
	}

	finalURL, data, mimeType, err := t.downloadImageURL(ctx, parsed)
	if err != nil {
		return imageInspectSource{}, err
	}

	tempDir := filepath.Join(os.TempDir(), "picoclaw_image_inspect")
	if mkErr := os.MkdirAll(tempDir, 0700); mkErr != nil {
		return imageInspectSource{}, fmt.Errorf("create temp dir: %w", mkErr)
	}

	baseName := path.Base(finalURL.Path)
	if baseName == "." || baseName == "/" || strings.TrimSpace(baseName) == "" {
		baseName = "image"
	}
	baseName = utils.SanitizeFilename(baseName)
	ext := extensionForImageMime(mimeType)
	pattern := strings.TrimSuffix(baseName, filepath.Ext(baseName)) + "-*" + ext
	if strings.TrimSpace(pattern) == "-*"+ext {
		pattern = "image-*" + ext
	}

	file, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		return imageInspectSource{}, fmt.Errorf("create temp file: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return imageInspectSource{}, fmt.Errorf("write temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return imageInspectSource{}, fmt.Errorf("close temp file: %w", err)
	}

	return imageInspectSource{Input: source, Kind: "url", Path: path, MIME: mimeType, Bytes: len(data)}, nil
}

func (t *ImageInspectTool) downloadImageURL(ctx context.Context, parsed *url.URL) (*url.URL, []byte, string, error) {
	client := t.httpClient
	if client == nil {
		client = &http.Client{Timeout: t.downloadTimeout}
	}
	manualClient := *client
	manualClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	current := parsed
	for redirects := 0; redirects <= imageInspectRedirectLimit; redirects++ {
		if err := t.validateURL(ctx, current); err != nil {
			return nil, nil, "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, nil, "", fmt.Errorf("build request: %w", err)
		}

		resp, err := manualClient.Do(req)
		if err != nil {
			return nil, nil, "", fmt.Errorf("download request failed: %w", err)
		}

		if isRedirectStatus(resp.StatusCode) {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			_ = resp.Body.Close()
			if location == "" {
				return nil, nil, "", fmt.Errorf("redirect response missing location header")
			}
			nextURL, err := current.Parse(location)
			if err != nil {
				return nil, nil, "", fmt.Errorf("invalid redirect target: %w", err)
			}
			current = nextURL
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, nil, "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
		}

		if contentLength := resp.ContentLength; contentLength > 0 && contentLength > t.maxBytes {
			_ = resp.Body.Close()
			return nil, nil, "", fmt.Errorf("download too large (%d bytes)", contentLength)
		}

		limited := io.LimitReader(resp.Body, t.maxBytes+1)
		data, readErr := io.ReadAll(limited)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, nil, "", fmt.Errorf("read download body: %w", readErr)
		}
		if int64(len(data)) > t.maxBytes {
			return nil, nil, "", fmt.Errorf("download exceeded max size (%d bytes)", t.maxBytes)
		}

		mimeType := http.DetectContentType(data)
		if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
			return nil, nil, "", fmt.Errorf("URL did not return image content (detected %s)", mimeType)
		}

		return current, data, mimeType, nil
	}

	return nil, nil, "", fmt.Errorf("too many redirects")
}

func (t *ImageInspectTool) validateURL(ctx context.Context, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("invalid URL")
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}

	if u.User != nil {
		return fmt.Errorf("URLs with userinfo are not allowed")
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("URL host is required")
	}

	if t.allowPrivateHosts {
		return nil
	}

	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("URL host resolves to a private or local network address")
		}
		return nil
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host has no IP addresses")
	}

	for _, ipAddr := range ips {
		if isBlockedIP(ipAddr.IP) {
			return fmt.Errorf("URL host resolves to a private or local network address")
		}
	}

	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func extensionForImageMime(mimeType string) string {
	m := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	default:
		return ".img"
	}
}

func formatImageInspectResult(backendName string, sources []imageInspectSource, analysis string, warnings []string) string {
	lines := []string{
		fmt.Sprintf("Image analysis complete (backend: %s)", backendName),
		fmt.Sprintf("Images analyzed: %d", len(sources)),
	}

	for index, source := range sources {
		lines = append(lines,
			fmt.Sprintf("%d. %s", index+1, source.Input),
			fmt.Sprintf("   kind=%s mime=%s bytes=%d", source.Kind, source.MIME, source.Bytes),
		)
	}

	lines = append(lines, "", "Analysis:", strings.TrimSpace(analysis))

	if len(warnings) > 0 {
		lines = append(lines, "", "Warnings:")
		for _, warning := range warnings {
			lines = append(lines, "- "+warning)
		}
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	return scheme == "http" || scheme == "https"
}
