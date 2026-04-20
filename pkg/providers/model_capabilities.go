package providers

import "strings"

type ModelCapabilities struct {
	SupportsVision       bool
	SupportsInlineVision bool
}

func ModelCapabilitiesFor(model string) ModelCapabilities {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return ModelCapabilities{}
	}

	switch {
	case strings.Contains(normalized, "glm-5v"):
		return ModelCapabilities{SupportsVision: true, SupportsInlineVision: true}
	case strings.Contains(normalized, "glm-5"):
		return ModelCapabilities{SupportsVision: false, SupportsInlineVision: false}
	case strings.Contains(normalized, "glm-4.6v"):
		return ModelCapabilities{SupportsVision: true, SupportsInlineVision: false}
	case strings.Contains(normalized, "gpt-4o"):
		return ModelCapabilities{SupportsVision: true, SupportsInlineVision: true}
	case strings.Contains(normalized, "claude"):
		return ModelCapabilities{SupportsVision: true, SupportsInlineVision: true}
	case strings.Contains(normalized, "gemini"):
		return ModelCapabilities{SupportsVision: true, SupportsInlineVision: false}
	default:
		return ModelCapabilities{SupportsVision: false, SupportsInlineVision: false}
	}
}
