package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolveTokenLimits_ExplicitContextWindow(t *testing.T) {
	outMax, ctx := resolveTokenLimits(config.AgentDefaults{MaxTokens: 8192, ContextWindowTokens: 200000})
	if outMax != 8192 {
		t.Fatalf("outMax = %d, want 8192", outMax)
	}
	if ctx != 200000 {
		t.Fatalf("ctx = %d, want 200000", ctx)
	}
}

func TestResolveTokenLimits_DefaultsToMaxTokensForContextWindow(t *testing.T) {
	outMax, ctx := resolveTokenLimits(config.AgentDefaults{MaxTokens: 8192})
	if outMax != 8192 {
		t.Fatalf("outMax = %d, want 8192", outMax)
	}
	if ctx != 8192 {
		t.Fatalf("ctx = %d, want 8192", ctx)
	}
}

func TestResolveTokenLimits_BackCompatLargeMaxTokensAssumedContextWindow(t *testing.T) {
	outMax, ctx := resolveTokenLimits(config.AgentDefaults{MaxTokens: 200000})
	if outMax != 8192 {
		t.Fatalf("outMax = %d, want 8192", outMax)
	}
	if ctx != 200000 {
		t.Fatalf("ctx = %d, want 200000", ctx)
	}
}

func TestResolveTokenLimits_ZeroValuesUseSafeDefaults(t *testing.T) {
	outMax, ctx := resolveTokenLimits(config.AgentDefaults{})
	if outMax != 8192 {
		t.Fatalf("outMax = %d, want 8192", outMax)
	}
	if ctx != 8192 {
		t.Fatalf("ctx = %d, want 8192", ctx)
	}
}
