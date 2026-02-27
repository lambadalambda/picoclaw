package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolvePrimaryVisionAnalyzer_TextOnlyModel_SkipsPrimary(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "glm-5"

	// Ensure a provider would otherwise be resolvable.
	cfg.Providers.Modal.APIKey = "test-key"
	cfg.Providers.Modal.APIBase = "https://example.com"

	analyzer, model := resolvePrimaryVisionAnalyzer(cfg)
	if analyzer != nil {
		t.Fatalf("expected nil analyzer for text-only model")
	}
	if model != "" {
		t.Fatalf("expected empty model label for text-only model, got %q", model)
	}
}

func TestResolvePrimaryVisionAnalyzer_VisionModel_ReturnsClient(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "glm-4.6v"
	cfg.Providers.Zhipu.APIKey = "test-key"
	cfg.Providers.Zhipu.APIBase = "https://open.bigmodel.cn/api/paas/v4"

	analyzer, model := resolvePrimaryVisionAnalyzer(cfg)
	if analyzer == nil {
		t.Fatalf("expected analyzer to be configured")
	}
	if model != "glm-4.6v" {
		t.Fatalf("model = %q, want %q", model, "glm-4.6v")
	}
	if analyzer.Model != "glm-4.6v" {
		t.Fatalf("client model = %q, want %q", analyzer.Model, "glm-4.6v")
	}
}
