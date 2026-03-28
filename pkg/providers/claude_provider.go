package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/sipeed/picoclaw/pkg/auth"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const anthropicPromptCachingBeta = "prompt-caching-2024-07-31"
const claudeCodeOAuthSystemPrefix = "You are Claude Code, Anthropic's official CLI for Claude."

type ClaudeProvider struct {
	client      *anthropic.Client
	token       string
	tokenSource func() (string, error)
}

func NewClaudeProvider(token string) *ClaudeProvider {
	client := anthropic.NewClient(
		option.WithAuthToken(token),
		option.WithBaseURL("https://api.anthropic.com"),
	)
	return &ClaudeProvider{client: &client, token: token}
}

func NewClaudeProviderWithTokenSource(token string, tokenSource func() (string, error)) *ClaudeProvider {
	p := NewClaudeProvider(token)
	p.tokenSource = tokenSource
	return p
}

func isAnthropicOAuthToken(token string) bool {
	// Anthropic OAuth access tokens use the sk-ant-oat* prefix.
	// These require the oauth beta header to be accepted by the API.
	return strings.Contains(token, "sk-ant-oat")
}

func oauthAnthropicBetaHeader(token string) string {
	if !isAnthropicOAuthToken(token) {
		return ""
	}
	// Required for Anthropic to accept OAuth Bearer auth.
	// Without oauth-2025-04-20, Anthropic returns 401: "OAuth authentication is currently not supported."
	// Keep claude-code-20250219 alongside oauth-2025-04-20 (matches upstream behavior).
	return "claude-code-20250219,oauth-2025-04-20"
}

func (p *ClaudeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	var opts []option.RequestOption

	cacheControl, err := parseAnthropicCacheControl(options)
	if err != nil {
		return nil, err
	}

	tok := strings.TrimSpace(p.token)
	if p.tokenSource != nil {
		refreshed, err := p.tokenSource()
		if err != nil {
			return nil, fmt.Errorf("refreshing token: %w", err)
		}
		tok = strings.TrimSpace(refreshed)
	}
	if tok != "" {
		opts = append(opts, option.WithAuthToken(tok))
		if beta := buildAnthropicBetaHeader(tok, options, cacheControl != nil); beta != "" {
			opts = append(opts, option.WithHeader("anthropic-beta", beta))
		}
	}

	params, err := buildClaudeParams(messages, tools, model, options)
	if err != nil {
		return nil, err
	}

	ensureClaudeCodeOAuthSystemPrefix(&params, tok)

	resp, err := p.client.Messages.New(ctx, params, opts...)
	if err != nil {
		return nil, fmt.Errorf("claude API call: %w", err)
	}

	return parseClaudeResponse(resp), nil
}

func ensureClaudeCodeOAuthSystemPrefix(params *anthropic.MessageNewParams, token string) {
	if params == nil || !isAnthropicOAuthToken(token) {
		return
	}
	if len(params.System) > 0 && strings.TrimSpace(params.System[0].Text) == claudeCodeOAuthSystemPrefix {
		return
	}

	prefixed := make([]anthropic.TextBlockParam, 0, len(params.System)+1)
	prefixed = append(prefixed, anthropic.TextBlockParam{Text: claudeCodeOAuthSystemPrefix})
	prefixed = append(prefixed, params.System...)
	params.System = prefixed
}

func (p *ClaudeProvider) GetDefaultModel() string {
	return "claude-sonnet-4-5-20250929"
}

func buildClaudeParams(messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (anthropic.MessageNewParams, error) {
	var system []anthropic.TextBlockParam
	var anthropicMessages []anthropic.MessageParam
	cacheControl, err := parseAnthropicCacheControl(options)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}

	beforeCount := len(messages)
	messages, dropped := SanitizeToolTranscript(messages)
	if dropped > 0 {
		logger.WarnCF("provider", "Dropped invalid tool transcript messages before Claude request",
			map[string]interface{}{
				"messages_before": beforeCount,
				"messages_after":  len(messages),
				"dropped":         dropped,
			})
	}

	toolResultIsError := func(content string) bool {
		content = strings.TrimSpace(content)
		if content == "" {
			return false
		}
		lower := strings.ToLower(content)
		return strings.HasPrefix(lower, "error:")
	}
	buildToolResultBlock := func(msg Message) anthropic.ContentBlockParamUnion {
		if msg.ToolCallID == "" {
			return anthropic.NewToolResultBlock("", msg.Content, toolResultIsError(msg.Content))
		}

		if len(msg.Parts) == 0 {
			return anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, toolResultIsError(msg.Content))
		}

		content := make([]anthropic.ToolResultBlockParamContentUnion, 0, len(msg.Parts)+1)
		if strings.TrimSpace(msg.Content) != "" {
			content = append(content, anthropic.ToolResultBlockParamContentUnion{OfText: &anthropic.TextBlockParam{Text: msg.Content}})
		}
		for _, part := range msg.Parts {
			imageData, err := inlineImageDataFromPart(part)
			if err != nil {
				logger.WarnCF("provider", "Skipping inline image part for Claude tool output", map[string]interface{}{
					"path":  strings.TrimSpace(part.Path),
					"error": err.Error(),
				})
				continue
			}

			mediaType, ok := anthropicMediaTypeForImage(imageData.MediaType)
			if !ok {
				logger.WarnCF("provider", "Skipping unsupported inline image media type for Claude tool output", map[string]interface{}{
					"path":       imageData.Path,
					"media_type": imageData.MediaType,
				})
				continue
			}

			source := anthropic.ImageBlockParamSourceUnion{OfBase64: &anthropic.Base64ImageSourceParam{
				Data:      imageData.Base64Data,
				MediaType: mediaType,
			}}
			img := anthropic.ImageBlockParam{Source: source}
			content = append(content, anthropic.ToolResultBlockParamContentUnion{OfImage: &img})
		}

		if len(content) == 0 {
			return anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, toolResultIsError(msg.Content))
		}

		toolRes := anthropic.ToolResultBlockParam{ToolUseID: msg.ToolCallID, Content: content}
		if toolResultIsError(msg.Content) {
			toolRes.IsError = anthropic.Bool(true)
		}
		return anthropic.ContentBlockParamUnion{OfToolResult: &toolRes}
	}

	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		role := strings.ToLower(strings.TrimSpace(msg.Role))

		switch role {
		case "system":
			tb := anthropic.TextBlockParam{Text: msg.Content}
			system = append(system, tb)
		case "user":
			if msg.ToolCallID != "" {
				anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(buildToolResultBlock(msg)))
			} else {
				if len(msg.Parts) > 0 {
					blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Parts)+1)
					if strings.TrimSpace(msg.Content) != "" {
						blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
					}

					for _, part := range msg.Parts {
						imageData, err := inlineImageDataFromPart(part)
						if err != nil {
							logger.WarnCF("provider", "Skipping inline image part for Claude request", map[string]interface{}{
								"path":  strings.TrimSpace(part.Path),
								"error": err.Error(),
							})
							continue
						}

						mediaType, ok := anthropicMediaTypeForImage(imageData.MediaType)
						if !ok {
							logger.WarnCF("provider", "Skipping unsupported inline image media type for Claude", map[string]interface{}{
								"path":       imageData.Path,
								"media_type": imageData.MediaType,
							})
							continue
						}

						blocks = append(blocks, anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
							Data:      imageData.Base64Data,
							MediaType: mediaType,
						}))
					}

					if len(blocks) == 0 {
						blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
					}

					anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(blocks...))
				} else {
					textBlock := anthropic.NewTextBlock(msg.Content)
					anthropicMessages = append(anthropicMessages,
						anthropic.NewUserMessage(textBlock),
					)
				}
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if msg.Content != "" {
					textBlock := anthropic.NewTextBlock(msg.Content)
					blocks = append(blocks, textBlock)
				}
				for _, tc := range msg.ToolCalls {
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Arguments, tc.Name))
				}
				anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(blocks...))
			} else {
				textBlock := anthropic.NewTextBlock(msg.Content)
				anthropicMessages = append(anthropicMessages,
					anthropic.NewAssistantMessage(textBlock),
				)
			}
		case "tool":
			blocks := []anthropic.ContentBlockParamUnion{buildToolResultBlock(msg)}
			for i+1 < len(messages) {
				next := messages[i+1]
				if strings.ToLower(strings.TrimSpace(next.Role)) != "tool" {
					break
				}
				i++
				blocks = append(blocks, buildToolResultBlock(next))
			}
			anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(blocks...))
		}
	}

	maxTokens := int64(4096)
	if mt, ok := options["max_tokens"].(int); ok {
		maxTokens = int64(mt)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		Messages:  anthropicMessages,
		MaxTokens: maxTokens,
	}

	if len(system) > 0 {
		params.System = system
	}

	if temp, ok := options["temperature"].(float64); ok {
		params.Temperature = anthropic.Float(temp)
	}

	if len(tools) > 0 {
		params.Tools = translateToolsForClaude(tools)
	}

	if cacheControl != nil {
		extra := map[string]interface{}{
			"type": string(cacheControl.Type),
		}
		if cacheControl.TTL != "" {
			extra["ttl"] = string(cacheControl.TTL)
		}
		params.SetExtraFields(map[string]interface{}{
			"cache_control": extra,
		})
	}

	return params, nil
}

func parseAnthropicCacheControl(options map[string]interface{}) (*anthropic.CacheControlEphemeralParam, error) {
	if len(options) == 0 {
		return nil, nil
	}

	enabled := false
	if rawEnabled, ok := options["anthropic_cache"]; ok {
		v, ok := rawEnabled.(bool)
		if !ok {
			return nil, fmt.Errorf("anthropic_cache must be a bool")
		}
		enabled = v
	}

	var ttl string
	if rawTTL, ok := options["anthropic_cache_ttl"]; ok {
		v, ok := rawTTL.(string)
		if !ok {
			return nil, fmt.Errorf("anthropic_cache_ttl must be a string")
		}
		ttl = strings.TrimSpace(v)
		if ttl != "" {
			enabled = true
		}
	}

	if !enabled {
		return nil, nil
	}

	cacheControl := anthropic.NewCacheControlEphemeralParam()
	switch ttl {
	case "", "5m":
		if ttl == "5m" {
			cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL5m
		}
	case "1h":
		cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL1h
	default:
		return nil, fmt.Errorf("unsupported anthropic_cache_ttl %q (expected \"5m\" or \"1h\")", ttl)
	}

	return &cacheControl, nil
}

func buildAnthropicBetaHeader(token string, options map[string]interface{}, cacheEnabled bool) string {
	betaValues := []string{oauthAnthropicBetaHeader(token)}
	if cacheEnabled {
		betaValues = append(betaValues, anthropicPromptCachingBeta)
	}

	if rawBeta, ok := options["anthropic_beta"]; ok {
		if beta, ok := rawBeta.(string); ok {
			betaValues = append(betaValues, beta)
		}
	}

	return mergeAnthropicBetaHeaders(betaValues...)
}

func mergeAnthropicBetaHeaders(values ...string) string {
	seen := make(map[string]struct{})
	merged := make([]string, 0, len(values))

	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			merged = append(merged, part)
		}
	}

	return strings.Join(merged, ",")
}

func anthropicMediaTypeForImage(mediaType string) (anthropic.Base64ImageSourceMediaType, bool) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg":
		return anthropic.Base64ImageSourceMediaTypeImageJPEG, true
	case "image/png":
		return anthropic.Base64ImageSourceMediaTypeImagePNG, true
	case "image/gif":
		return anthropic.Base64ImageSourceMediaTypeImageGIF, true
	case "image/webp":
		return anthropic.Base64ImageSourceMediaTypeImageWebP, true
	default:
		return "", false
	}
}

func translateToolsForClaude(tools []ToolDefinition) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := anthropic.ToolParam{
			Name: t.Function.Name,
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: t.Function.Parameters["properties"],
			},
		}
		if desc := t.Function.Description; desc != "" {
			tool.Description = anthropic.String(desc)
		}
		if required := parseToolRequiredFields(t.Function.Parameters["required"]); len(required) > 0 {
			tool.InputSchema.Required = required
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return result
}

func parseToolRequiredFields(raw interface{}) []string {
	switch req := raw.(type) {
	case []string:
		out := make([]string, 0, len(req))
		for _, v := range req {
			name := strings.TrimSpace(v)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(req))
		for _, v := range req {
			name, ok := v.(string)
			if !ok {
				continue
			}
			name = strings.TrimSpace(name)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func parseClaudeResponse(resp *anthropic.Message) *LLMResponse {
	var content string
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			tb := block.AsText()
			content += tb.Text
		case "tool_use":
			tu := block.AsToolUse()
			var args map[string]interface{}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				args = map[string]interface{}{"raw": string(tu.Input)}
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:          tu.ID,
				Name:        tu.Name,
				Description: normalizeToolCallDescription(toolCallDescriptionFromArgs(args)),
				Arguments:   args,
			})
		}
	}

	finishReason := "stop"
	switch resp.StopReason {
	case anthropic.StopReasonToolUse:
		finishReason = "tool_calls"
	case anthropic.StopReasonMaxTokens:
		finishReason = "length"
	case anthropic.StopReasonEndTurn:
		finishReason = "stop"
	}

	logClaudeCacheUsage(resp)

	promptTokens := effectiveClaudePromptTokens(resp.Usage)
	inputTokens := int(resp.Usage.InputTokens)
	cacheReadInputTokens := int(resp.Usage.CacheReadInputTokens)
	cacheCreationInputTokens := int(resp.Usage.CacheCreationInputTokens)
	cacheCreationEphemeral5mInputTokens := int(resp.Usage.CacheCreation.Ephemeral5mInputTokens)
	cacheCreationEphemeral1hInputTokens := int(resp.Usage.CacheCreation.Ephemeral1hInputTokens)
	if cacheCreationInputTokens <= 0 {
		cacheCreationInputTokens = cacheCreationEphemeral5mInputTokens + cacheCreationEphemeral1hInputTokens
	}
	completionTokens := int(resp.Usage.OutputTokens)
	totalTokens := promptTokens + completionTokens

	return &LLMResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: &UsageInfo{
			Provider:                            "anthropic",
			PromptTokens:                        promptTokens,
			CompletionTokens:                    completionTokens,
			TotalTokens:                         totalTokens,
			InputTokens:                         inputTokens,
			OutputTokens:                        completionTokens,
			CacheReadInputTokens:                cacheReadInputTokens,
			CacheCreationInputTokens:            cacheCreationInputTokens,
			CacheCreationEphemeral5mInputTokens: cacheCreationEphemeral5mInputTokens,
			CacheCreationEphemeral1hInputTokens: cacheCreationEphemeral1hInputTokens,
		},
	}
}

// effectiveClaudePromptTokens returns an estimate of the true prompt size for
// Anthropic requests.
//
// When prompt caching is enabled, Anthropic reports cached portions separately
// (cache_read_input_tokens / cache_creation_input_tokens). For agent compaction
// and payload safety we want the combined value.
func effectiveClaudePromptTokens(usage anthropic.Usage) int {
	total := usage.InputTokens
	if usage.CacheReadInputTokens > 0 {
		total += usage.CacheReadInputTokens
	}

	creation := usage.CacheCreationInputTokens
	if creation <= 0 {
		creation = usage.CacheCreation.Ephemeral5mInputTokens + usage.CacheCreation.Ephemeral1hInputTokens
	}
	if creation > 0 {
		total += creation
	}

	if total < 0 {
		return 0
	}
	return int(total)
}

func logClaudeCacheUsage(resp *anthropic.Message) {
	if resp == nil {
		return
	}

	hasCacheInfo := resp.Usage.JSON.CacheCreation.Valid() ||
		resp.Usage.JSON.CacheCreationInputTokens.Valid() ||
		resp.Usage.JSON.CacheReadInputTokens.Valid() ||
		resp.Usage.CacheCreationInputTokens > 0 ||
		resp.Usage.CacheReadInputTokens > 0 ||
		resp.Usage.CacheCreation.Ephemeral5mInputTokens > 0 ||
		resp.Usage.CacheCreation.Ephemeral1hInputTokens > 0
	if !hasCacheInfo {
		return
	}

	fields := map[string]interface{}{
		"provider":                          "anthropic",
		"model":                             string(resp.Model),
		"usage.cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
		"usage.cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
		"usage.cache_creation.ephemeral_5m_input_tokens": resp.Usage.CacheCreation.Ephemeral5mInputTokens,
		"usage.cache_creation.ephemeral_1h_input_tokens": resp.Usage.CacheCreation.Ephemeral1hInputTokens,
	}
	if resp.Usage.InputTokens > 0 {
		fields["usage.input_tokens"] = resp.Usage.InputTokens
	}
	if ratio, ok := anthropicCacheHitRatio(resp.Usage.InputTokens, resp.Usage.CacheReadInputTokens); ok {
		fields["usage.cache_hit_ratio"] = ratio
	}

	logger.InfoCF("provider", "LLM cache usage reported", fields)
}

func anthropicCacheHitRatio(inputTokens, cacheReadInputTokens int64) (float64, bool) {
	totalInput := inputTokens + cacheReadInputTokens
	if totalInput <= 0 {
		return 0, false
	}
	return roundTo(float64(cacheReadInputTokens)/float64(totalInput), 4), true
}

func createClaudeTokenSource() func() (string, error) {
	var mu sync.Mutex

	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()

		cred, err := auth.GetCredential("anthropic")
		if err != nil {
			return "", fmt.Errorf("loading auth credentials: %w", err)
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for anthropic. Run: picoclaw auth login --provider anthropic")
		}

		if strings.EqualFold(cred.AuthMethod, "oauth") && cred.NeedsRefresh() {
			if strings.TrimSpace(cred.RefreshToken) == "" {
				if cred.IsExpired() {
					return "", fmt.Errorf("anthropic oauth token expired and no refresh token is available; run auth login again")
				}
				return cred.AccessToken, nil
			}

			refreshed, err := auth.RefreshAnthropicAccessToken(cred)
			if err != nil {
				return "", fmt.Errorf("refreshing token: %w", err)
			}
			if err := auth.SetCredential("anthropic", refreshed); err != nil {
				return "", fmt.Errorf("saving refreshed token: %w", err)
			}
			return refreshed.AccessToken, nil
		}

		return cred.AccessToken, nil
	}
}
