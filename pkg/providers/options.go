package providers

// ChatOptions defines common request options for provider Chat calls.
// It keeps max token/temperature tuning in one typed place and can be
// converted to the generic options map expected by providers.
type ChatOptions struct {
	MaxTokens   int
	Temperature float64
}

// ToMap converts ChatOptions to provider request options.
func (o ChatOptions) ToMap() map[string]interface{} {
	opts := map[string]interface{}{
		"temperature": o.Temperature,
	}
	if o.MaxTokens > 0 {
		opts["max_tokens"] = o.MaxTokens
	}
	return opts
}
