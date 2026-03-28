package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

type fallbackCandidate struct {
	model    string
	provider LLMProvider
}

type fallbackProvider struct {
	primaryModel string
	candidates   []fallbackCandidate
}

func newFallbackProvider(primaryModel string, candidates []fallbackCandidate) *fallbackProvider {
	return &fallbackProvider{
		primaryModel: primaryModel,
		candidates:   append([]fallbackCandidate(nil), candidates...),
	}
}

func (p *fallbackProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if len(p.candidates) == 0 {
		return nil, fmt.Errorf("no providers configured for fallback")
	}

	order := p.orderedCandidates(model)
	attemptErrors := make([]string, 0, len(order))

	for idx, candidate := range order {
		resp, err := candidate.provider.Chat(ctx, messages, tools, candidate.model, options)
		if err == nil {
			if idx > 0 {
				logger.WarnCF("provider", "Fallback model used after primary failure",
					map[string]interface{}{
						"requested_model": model,
						"selected_model":  candidate.model,
						"attempt":         idx + 1,
					})
			}
			return resp, nil
		}

		attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", candidate.model, err))
		if !isModelFallbackEligibleError(err) {
			return nil, err
		}

		if idx+1 < len(order) {
			nextModel := order[idx+1].model
			logger.WarnCF("provider", "Model unavailable, trying fallback",
				map[string]interface{}{
					"requested_model": model,
					"failed_model":    candidate.model,
					"next_model":      nextModel,
					"error":           err.Error(),
				})
		}
	}

	return nil, fmt.Errorf("all fallback models failed: %s", strings.Join(attemptErrors, " | "))
}

func (p *fallbackProvider) GetDefaultModel() string {
	if p.primaryModel != "" {
		return p.primaryModel
	}
	if len(p.candidates) > 0 {
		return p.candidates[0].model
	}
	return ""
}

func (p *fallbackProvider) orderedCandidates(requestedModel string) []fallbackCandidate {
	if len(p.candidates) <= 1 {
		return append([]fallbackCandidate(nil), p.candidates...)
	}

	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return append([]fallbackCandidate(nil), p.candidates...)
	}

	requestedIndex := -1
	for i, candidate := range p.candidates {
		if strings.EqualFold(candidate.model, requestedModel) {
			requestedIndex = i
			break
		}
	}
	if requestedIndex <= 0 {
		return append([]fallbackCandidate(nil), p.candidates...)
	}

	ordered := make([]fallbackCandidate, 0, len(p.candidates))
	ordered = append(ordered, p.candidates[requestedIndex])
	for i, candidate := range p.candidates {
		if i == requestedIndex {
			continue
		}
		ordered = append(ordered, candidate)
	}
	return ordered
}

func isModelFallbackEligibleError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if isOpaqueAnthropicBadRequest(msg) {
		return true
	}
	patterns := []string{
		"model not found",
		"unknown model",
		"does not exist",
		"unavailable",
		"temporarily unavailable",
		"service unavailable",
		"overloaded",
		"overload",
		"rate limit",
		"too many requests",
		"insufficient_quota",
		"quota exceeded",
		"internal server error",
		"http 500",
		"http 429",
		" 500 ",
		"http 502",
		"http 503",
		"http 504",
		"http 529",
		" 429 ",
		" 502 ",
		" 503 ",
		" 504 ",
		" 529 ",
	}
	for _, pattern := range patterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

func isOpaqueAnthropicBadRequest(msg string) bool {
	if !strings.Contains(msg, "api.anthropic.com/v1/messages") || !strings.Contains(msg, "400 bad request") {
		return false
	}

	if start := strings.Index(msg, "{"); start >= 0 {
		var payload struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(msg[start:]), &payload); err == nil {
			return strings.EqualFold(strings.TrimSpace(payload.Error.Type), "invalid_request_error") &&
				strings.EqualFold(strings.TrimSpace(payload.Error.Message), "error")
		}
	}

	return strings.Contains(msg, "invalid_request_error") &&
		(strings.Contains(msg, `"message":"error"`) || strings.Contains(msg, `"message": "error"`))
}

func normalizeFallbackModels(primary string, fallbackModels []string) []string {
	primary = strings.TrimSpace(primary)
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(fallbackModels))

	if primary != "" {
		seen[strings.ToLower(primary)] = struct{}{}
	}

	for _, m := range fallbackModels {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		key := strings.ToLower(m)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, m)
	}

	return normalized
}
