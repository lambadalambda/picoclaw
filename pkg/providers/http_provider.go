// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sipeed/picoclaw/pkg/auth"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	defaultMaxRetries    = 5                // up to 5 retries (6 attempts total)
	defaultRetryBaseWait = 1 * time.Second  // base wait before first retry
	defaultRetryMaxWait  = 60 * time.Second // cap on backoff duration
	defaultRetryJitter   = 0.2              // +/-20% jitter for non-Retry-After waits
	defaultHTTPTimeout   = 2 * time.Minute  // safety net; ctx controls cancellation per call
)

type HTTPProvider struct {
	apiKey        string
	apiBase       string
	httpClient    *http.Client
	maxRetries    int
	retryBaseWait time.Duration
	retryMaxWait  time.Duration
	retryJitter   float64
	randFloat     func() float64
	routing       map[string]interface{}
}

func NewHTTPProvider(apiKey, apiBase string) *HTTPProvider {
	return &HTTPProvider{
		apiKey:        apiKey,
		apiBase:       apiBase,
		maxRetries:    defaultMaxRetries,
		retryBaseWait: defaultRetryBaseWait,
		retryMaxWait:  defaultRetryMaxWait,
		retryJitter:   defaultRetryJitter,
		randFloat:     rand.Float64,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// SetRouting sets the provider routing preferences (OpenRouter-specific).
// The map is passed as the "provider" object in the request body.
func (p *HTTPProvider) SetRouting(routing map[string]interface{}) {
	p.routing = routing
}

func (p *HTTPProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.apiBase == "" {
		return nil, fmt.Errorf("API base not configured")
	}

	requestBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}

	if len(tools) > 0 {
		requestBody["tools"] = tools
		requestBody["tool_choice"] = "auto"
	}

	if maxTokens, ok := options["max_tokens"].(int); ok {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "o1") {
			requestBody["max_completion_tokens"] = maxTokens
		} else {
			requestBody["max_tokens"] = maxTokens
		}
	}

	if temperature, ok := options["temperature"].(float64); ok {
		requestBody["temperature"] = temperature
	}

	if len(p.routing) > 0 {
		requestBody["provider"] = p.routing
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var lastErr error
	var retryAfterHint time.Duration
	var hasRetryAfterHint bool
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			retryAfterLog := ""
			if hasRetryAfterHint {
				retryAfterLog = retryAfterHint.String()
			}
			wait := p.computeRetryWait(attempt, retryAfterHint, hasRetryAfterHint)
			hasRetryAfterHint = false

			logger.WarnCF("provider", fmt.Sprintf("Retrying LLM request (attempt %d/%d)", attempt+1, p.maxRetries+1),
				map[string]interface{}{
					"wait":        wait.String(),
					"retry_after": retryAfterLog,
					"last_error":  fmt.Sprintf("%v", lastErr),
				})

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry wait: %w", ctx.Err())
			case <-time.After(wait):
			}
		}

		resp, err := p.doRequest(ctx, jsonData)
		if err != nil {
			lastErr = err
			hasRetryAfterHint = false
			// Context cancellation is not retryable
			if ctx.Err() != nil {
				return nil, fmt.Errorf("failed to send request: %w", err)
			}
			continue
		}

		retryAfter, hasRetryAfter := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
		statusCode, body, err := p.readResponse(resp)
		if err != nil {
			lastErr = err
			hasRetryAfterHint = false
			continue
		}

		// Non-OK status: retry on retryable HTTP errors, fail immediately otherwise.
		if statusCode != http.StatusOK {
			lastErr = fmt.Errorf("API error (HTTP %d): %s", statusCode, utils.Truncate(string(body), 500))
			if isRetryableHTTPError(statusCode, body) {
				retryAfterHint = retryAfter
				hasRetryAfterHint = hasRetryAfter
				continue // retryable
			}
			return nil, lastErr // non-retryable client error
		}
		hasRetryAfterHint = false

		// Log raw response body at debug level for troubleshooting
		logger.DebugCF("provider", "Raw LLM response",
			map[string]interface{}{
				"status":     statusCode,
				"body_bytes": len(body),
				"body":       utils.Truncate(string(body), 2000),
			})

		llmResp, err := p.parseResponse(body)
		if err != nil {
			lastErr = err
			hasRetryAfterHint = false
			continue
		}

		// Check for empty/error responses that warrant a retry
		if p.shouldRetry(llmResp) {
			lastErr = fmt.Errorf("empty or error response from LLM (finish_reason=%s)", llmResp.FinishReason)
			hasRetryAfterHint = false
			continue
		}

		return llmResp, nil
	}

	return nil, fmt.Errorf("LLM request failed after %d attempts: %w", p.maxRetries+1, lastErr)
}

func (p *HTTPProvider) computeRetryWait(attempt int, retryAfterHint time.Duration, hasRetryAfterHint bool) time.Duration {
	wait := p.retryBaseWait * time.Duration(1<<(attempt-1)) // exponential: 1s, 2s, 4s, 8s, 16s
	if wait > p.retryMaxWait {
		wait = p.retryMaxWait
	}

	if !hasRetryAfterHint && p.retryJitter > 0 {
		rf := p.randFloat
		if rf == nil {
			rf = rand.Float64
		}
		factor := 1 + (rf()*2-1)*p.retryJitter
		if factor < 0 {
			factor = 0
		}
		wait = time.Duration(float64(wait) * factor)
		if wait <= 0 {
			wait = time.Millisecond
		}
		if wait > p.retryMaxWait {
			wait = p.retryMaxWait
		}
	}

	if hasRetryAfterHint {
		retryAfter := retryAfterHint
		if retryAfter < 0 {
			retryAfter = 0
		}
		if retryAfter > p.retryMaxWait {
			retryAfter = p.retryMaxWait
		}
		if retryAfter > wait {
			wait = retryAfter
		}
	}

	return wait
}

func isRetryableHTTPError(statusCode int, body []byte) bool {
	if statusCode == http.StatusTooManyRequests || statusCode >= 500 {
		return true
	}

	// OpenRouter sometimes transiently returns HTTP 401 with
	// "User not found." even for valid credentials. Treat it as retryable.
	if statusCode == http.StatusUnauthorized {
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			if strings.Contains(strings.ToLower(payload.Error.Message), "user not found") {
				return true
			}
		}
		// Fallback for non-standard payload shapes.
		if strings.Contains(strings.ToLower(string(body)), "user not found") {
			return true
		}
	}

	return false
}

func parseRetryAfterHeader(header string) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}

	// Delta-seconds form.
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0, true
		}
		return time.Duration(secs) * time.Second, true
	}

	// HTTP-date form.
	if t, err := http.ParseTime(header); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}

	return 0, false
}

// doRequest sends the HTTP request and returns the raw response.
func (p *HTTPProvider) doRequest(ctx context.Context, jsonData []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	return p.httpClient.Do(req)
}

// readResponse reads the body and closes it, returning status code and body bytes.
// Leading/trailing whitespace is trimmed because some upstream providers (e.g. Friendli
// via OpenRouter) pad responses with newlines.
func (p *HTTPProvider) readResponse(resp *http.Response) (int, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response: %w", err)
	}
	body = bytes.TrimFunc(body, unicode.IsSpace)
	return resp.StatusCode, body, nil
}

// shouldRetry returns true if the LLM response is empty/broken and worth retrying.
func (p *HTTPProvider) shouldRetry(resp *LLMResponse) bool {
	// Some providers return finish_reason="error" even with partial content.
	// Treat this as retryable.
	if strings.EqualFold(resp.FinishReason, "error") {
		return true
	}

	// No content and no tool calls = useless response
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		return true
	}
	return false
}

func (p *HTTPProvider) parseResponse(body []byte) (*LLMResponse, error) {
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *UsageInfo `json:"usage"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		logger.WarnCF("provider", "LLM returned 0 choices",
			map[string]interface{}{
				"body_preview": utils.Truncate(string(body), 500),
			})
		return &LLMResponse{
			Content:      "",
			FinishReason: "stop",
		}, nil
	}

	choice := apiResponse.Choices[0]

	if choice.Message.Content == "" && len(choice.Message.ToolCalls) == 0 {
		logger.WarnCF("provider", "LLM returned empty content with no tool calls",
			map[string]interface{}{
				"finish_reason": choice.FinishReason,
				"body_preview":  utils.Truncate(string(body), 500),
			})
	}

	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		arguments := make(map[string]interface{})
		name := ""

		// Handle OpenAI format with nested function object
		if tc.Type == "function" && tc.Function != nil {
			name = tc.Function.Name
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
					arguments["raw"] = tc.Function.Arguments
				}
			}
		} else if tc.Function != nil {
			// Legacy format without type field
			name = tc.Function.Name
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
					arguments["raw"] = tc.Function.Arguments
				}
			}
		}

		rawArgs := ""
		if tc.Function != nil {
			rawArgs = tc.Function.Arguments
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: &FunctionCall{
				Name:      name,
				Arguments: rawArgs,
			},
			Name:      name,
			Arguments: arguments,
		})
	}

	return &LLMResponse{
		Content:      choice.Message.Content,
		ToolCalls:    toolCalls,
		FinishReason: choice.FinishReason,
		Usage:        apiResponse.Usage,
	}, nil
}

func (p *HTTPProvider) GetDefaultModel() string {
	return ""
}

func createClaudeAuthProvider() (LLMProvider, error) {
	cred, err := auth.GetCredential("anthropic")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for anthropic. Run: picoclaw auth login --provider anthropic")
	}
	return NewClaudeProviderWithTokenSource(cred.AccessToken, createClaudeTokenSource()), nil
}

func createCodexAuthProvider() (LLMProvider, error) {
	cred, err := auth.GetCredential("openai")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for openai. Run: picoclaw auth login --provider openai")
	}
	return NewCodexProviderWithTokenSource(cred.AccessToken, cred.AccountID, createCodexTokenSource()), nil
}

func CreateProvider(cfg *config.Config) (LLMProvider, error) {
	model := cfg.Agents.Defaults.Model

	var apiKey, apiBase string
	var routing map[string]interface{}

	lowerModel := strings.ToLower(model)

	switch {
	case strings.HasPrefix(model, "openrouter/") || strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "openai/") || strings.HasPrefix(model, "meta-llama/") || strings.HasPrefix(model, "deepseek/") || strings.HasPrefix(model, "google/"):
		apiKey = cfg.Providers.OpenRouter.APIKey
		if cfg.Providers.OpenRouter.APIBase != "" {
			apiBase = cfg.Providers.OpenRouter.APIBase
		} else {
			apiBase = "https://openrouter.ai/api/v1"
		}
		routing = cfg.Providers.OpenRouter.Routing

	case (strings.Contains(lowerModel, "claude") || strings.HasPrefix(model, "anthropic/")) && (cfg.Providers.Anthropic.APIKey != "" || cfg.Providers.Anthropic.AuthMethod != ""):
		if cfg.Providers.Anthropic.AuthMethod == "oauth" || cfg.Providers.Anthropic.AuthMethod == "token" {
			return createClaudeAuthProvider()
		}
		apiKey = cfg.Providers.Anthropic.APIKey
		apiBase = cfg.Providers.Anthropic.APIBase
		if apiBase == "" {
			apiBase = "https://api.anthropic.com/v1"
		}

	case (strings.Contains(lowerModel, "gpt") || strings.HasPrefix(model, "openai/")) && (cfg.Providers.OpenAI.APIKey != "" || cfg.Providers.OpenAI.AuthMethod != ""):
		if cfg.Providers.OpenAI.AuthMethod == "oauth" || cfg.Providers.OpenAI.AuthMethod == "token" {
			return createCodexAuthProvider()
		}
		apiKey = cfg.Providers.OpenAI.APIKey
		apiBase = cfg.Providers.OpenAI.APIBase
		if apiBase == "" {
			apiBase = "https://api.openai.com/v1"
		}

	case (strings.Contains(lowerModel, "gemini") || strings.HasPrefix(model, "google/")) && cfg.Providers.Gemini.APIKey != "":
		apiKey = cfg.Providers.Gemini.APIKey
		apiBase = cfg.Providers.Gemini.APIBase
		if apiBase == "" {
			apiBase = "https://generativelanguage.googleapis.com/v1beta"
		}

	case (strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zhipu") || strings.Contains(lowerModel, "zai")) && cfg.Providers.Zhipu.APIKey != "":
		apiKey = cfg.Providers.Zhipu.APIKey
		apiBase = cfg.Providers.Zhipu.APIBase
		if apiBase == "" {
			apiBase = "https://open.bigmodel.cn/api/paas/v4"
		}

	case (strings.Contains(lowerModel, "groq") || strings.HasPrefix(model, "groq/")) && cfg.Providers.Groq.APIKey != "":
		apiKey = cfg.Providers.Groq.APIKey
		apiBase = cfg.Providers.Groq.APIBase
		if apiBase == "" {
			apiBase = "https://api.groq.com/openai/v1"
		}

	case (strings.Contains(lowerModel, "glm-5") || strings.HasPrefix(lowerModel, "zai-org/")) && cfg.Providers.Modal.APIKey != "":
		apiKey = cfg.Providers.Modal.APIKey
		apiBase = cfg.Providers.Modal.APIBase
		if apiBase == "" {
			apiBase = "https://api.us-west-2.modal.direct/v1"
		}

	case cfg.Providers.VLLM.APIBase != "":
		apiKey = cfg.Providers.VLLM.APIKey
		apiBase = cfg.Providers.VLLM.APIBase

	default:
		if cfg.Providers.OpenRouter.APIKey != "" {
			apiKey = cfg.Providers.OpenRouter.APIKey
			if cfg.Providers.OpenRouter.APIBase != "" {
				apiBase = cfg.Providers.OpenRouter.APIBase
			} else {
				apiBase = "https://openrouter.ai/api/v1"
			}
			routing = cfg.Providers.OpenRouter.Routing
		} else {
			return nil, fmt.Errorf("no API key configured for model: %s", model)
		}
	}

	if apiKey == "" && !strings.HasPrefix(model, "bedrock/") {
		return nil, fmt.Errorf("no API key configured for provider (model: %s)", model)
	}

	if apiBase == "" {
		return nil, fmt.Errorf("no API base configured for provider (model: %s)", model)
	}

	p := NewHTTPProvider(apiKey, apiBase)
	if len(routing) > 0 {
		p.SetRouting(routing)
	}
	return p, nil
}
