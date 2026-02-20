package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultTimeout   = 45 * time.Second
	defaultMaxImages = 3
	defaultMaxBytes  = 8 * 1024 * 1024
)

type Client struct {
	APIKey       string
	APIBase      string
	Model        string
	Timeout      time.Duration
	MaxImages    int
	MaxImageSize int
	HTTPClient   *http.Client
}

func NewClient(apiKey, apiBase, model string) *Client {
	return &Client{
		APIKey:       strings.TrimSpace(apiKey),
		APIBase:      strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		Model:        strings.TrimSpace(model),
		Timeout:      defaultTimeout,
		MaxImages:    defaultMaxImages,
		MaxImageSize: defaultMaxBytes,
	}
}

func (c *Client) AnalyzeImages(ctx context.Context, prompt string, imagePaths []string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("vision client not configured")
	}
	if c.APIKey == "" {
		return "", fmt.Errorf("vision API key not configured")
	}
	if c.APIBase == "" {
		return "", fmt.Errorf("vision API base not configured")
	}
	if c.Model == "" {
		return "", fmt.Errorf("vision model not configured")
	}
	if len(imagePaths) == 0 {
		return "", fmt.Errorf("at least one image path is required")
	}

	maxImages := c.MaxImages
	if maxImages <= 0 {
		maxImages = defaultMaxImages
	}
	if len(imagePaths) > maxImages {
		imagePaths = imagePaths[:maxImages]
	}

	maxImageSize := c.MaxImageSize
	if maxImageSize <= 0 {
		maxImageSize = defaultMaxBytes
	}

	contentItems := make([]map[string]interface{}, 0, len(imagePaths)+1)
	trimmedPrompt := strings.TrimSpace(prompt)
	if trimmedPrompt == "" {
		trimmedPrompt = "Describe the attached image(s) and transcribe any visible text."
	}
	contentItems = append(contentItems, map[string]interface{}{
		"type": "text",
		"text": trimmedPrompt,
	})

	for _, imagePath := range imagePaths {
		path := strings.TrimSpace(imagePath)
		if path == "" {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read image %q: %w", path, err)
		}
		if len(data) > maxImageSize {
			return "", fmt.Errorf("image %q exceeds max size of %d bytes", path, maxImageSize)
		}

		mimeType := detectImageMime(path, data)
		encoded := base64.StdEncoding.EncodeToString(data)
		dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

		contentItems = append(contentItems, map[string]interface{}{
			"type": "image_url",
			"image_url": map[string]interface{}{
				"url": dataURL,
			},
		})
	}

	if len(contentItems) <= 1 {
		return "", fmt.Errorf("no readable images were provided")
	}

	requestBody := map[string]interface{}{
		"model": c.Model,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": contentItems,
			},
		},
		"temperature": 0.1,
	}

	lowerModel := strings.ToLower(c.Model)
	if strings.Contains(lowerModel, "glm") {
		requestBody["max_completion_tokens"] = 1024
	} else {
		requestBody["max_tokens"] = 1024
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal vision request: %w", err)
	}

	endpoint := c.APIBase + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to build vision request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	httpClient := c.HTTPClient
	if httpClient == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read vision response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vision API error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse vision response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("vision API returned no choices")
	}

	result := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if result == "" {
		return "", fmt.Errorf("vision API returned empty analysis")
	}
	return result, nil
}

func detectImageMime(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	if byExt := mime.TypeByExtension(ext); byExt != "" {
		if semi := strings.Index(byExt, ";"); semi > 0 {
			return byExt[:semi]
		}
		return byExt
	}

	if len(data) > 0 {
		detected := http.DetectContentType(data)
		if strings.HasPrefix(detected, "image/") {
			if semi := strings.Index(detected, ";"); semi > 0 {
				return detected[:semi]
			}
			return detected
		}
	}

	return "image/png"
}
