package providers

import "strings"

// ChatOptions defines common request options for provider Chat calls.
// It keeps max token/temperature tuning in one typed place and can be
// converted to the generic options map expected by providers.
type ChatOptions struct {
	MaxTokens         int
	Temperature       float64
	AnthropicCache    bool
	AnthropicCacheTTL string
}

// ToMap converts ChatOptions to provider request options.
func (o ChatOptions) ToMap() map[string]interface{} {
	opts := map[string]interface{}{
		"temperature": o.Temperature,
	}
	if o.MaxTokens > 0 {
		opts["max_tokens"] = o.MaxTokens
	}
	if o.AnthropicCache {
		opts["anthropic_cache"] = true
	}
	if ttl := strings.TrimSpace(o.AnthropicCacheTTL); ttl != "" {
		opts["anthropic_cache_ttl"] = ttl
	}
	return opts
}
