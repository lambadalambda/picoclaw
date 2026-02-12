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
	"net/http"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/auth"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	defaultMaxRetries    = 5                 // up to 5 retries (6 attempts total)
	defaultRetryBaseWait = 1 * time.Second   // base wait before first retry
	defaultRetryMaxWait  = 60 * time.Second  // cap on backoff duration
)

type HTTPProvider struct {
	apiKey        string
	apiBase       string
	httpClient    *http.Client
	maxRetries    int
	retryBaseWait time.Duration
	retryMaxWait  time.Duration
}

func NewHTTPProvider(apiKey, apiBase string) *HTTPProvider {
	return &HTTPProvider{
		apiKey:        apiKey,
		apiBase:       apiBase,
		maxRetries:    defaultMaxRetries,
		retryBaseWait: defaultRetryBaseWait,
		retryMaxWait:  defaultRetryMaxWait,
		httpClient: &http.Client{
			Timeout: 0,
		},
	}
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

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			wait := p.retryBaseWait * time.Duration(1<<(attempt-1)) // exponential: 1s, 2s, 4s, 8s, 16s
			if wait > p.retryMaxWait {
				wait = p.retryMaxWait
			}
			logger.WarnCF("provider", fmt.Sprintf("Retrying LLM request (attempt %d/%d)", attempt+1, p.maxRetries+1),
				map[string]interface{}{
					"wait":       wait.String(),
					"last_error": fmt.Sprintf("%v", lastErr),
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
			// Context cancellation is not retryable
			if ctx.Err() != nil {
				return nil, fmt.Errorf("failed to send request: %w", err)
			}
			continue
		}

		statusCode, body, err := p.readResponse(resp)
		if err != nil {
			lastErr = err
			continue
		}

		// Non-OK status: retry on 5xx and 429, fail immediately on other 4xx
		if statusCode != http.StatusOK {
			lastErr = fmt.Errorf("API error (HTTP %d): %s", statusCode, utils.Truncate(string(body), 500))
			if statusCode == http.StatusTooManyRequests || statusCode >= 500 {
				continue // retryable
			}
			return nil, lastErr // non-retryable client error
		}

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
			continue
		}

		// Check for empty/error responses that warrant a retry
		if p.shouldRetry(llmResp) {
			lastErr = fmt.Errorf("empty or error response from LLM (finish_reason=%s)", llmResp.FinishReason)
			continue
		}

		return llmResp, nil
	}

	return nil, fmt.Errorf("LLM request failed after %d attempts: %w", p.maxRetries+1, lastErr)
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
func (p *HTTPProvider) readResponse(resp *http.Response) (int, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// shouldRetry returns true if the LLM response is empty/broken and worth retrying.
func (p *HTTPProvider) shouldRetry(resp *LLMResponse) bool {
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

		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
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

	lowerModel := strings.ToLower(model)

	switch {
	case strings.HasPrefix(model, "openrouter/") || strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "openai/") || strings.HasPrefix(model, "meta-llama/") || strings.HasPrefix(model, "deepseek/") || strings.HasPrefix(model, "google/"):
		apiKey = cfg.Providers.OpenRouter.APIKey
		if cfg.Providers.OpenRouter.APIBase != "" {
			apiBase = cfg.Providers.OpenRouter.APIBase
		} else {
			apiBase = "https://openrouter.ai/api/v1"
		}

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

	return NewHTTPProvider(apiKey, apiBase), nil
}