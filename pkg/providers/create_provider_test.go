package providers

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestCreateProvider_UsesModalForGLM5(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "zai-org/GLM-5-FP8"
	cfg.Providers.Modal.APIKey = "modal-key"
	cfg.Providers.Modal.APIBase = ""

	p, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	hp, ok := p.(*HTTPProvider)
	if !ok {
		t.Fatalf("expected HTTPProvider, got %T", p)
	}
	if hp.apiKey != "modal-key" {
		t.Fatalf("apiKey = %q, want %q", hp.apiKey, "modal-key")
	}
	if hp.apiBase != "https://api.us-west-2.modal.direct/v1" {
		t.Fatalf("apiBase = %q, want modal default", hp.apiBase)
	}
}

func TestCreateProvider_UsesModalCustomAPIBase(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "glm-5"
	cfg.Providers.Modal.APIKey = "modal-key"
	cfg.Providers.Modal.APIBase = "https://custom.modal.example/v1"

	p, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	hp, ok := p.(*HTTPProvider)
	if !ok {
		t.Fatalf("expected HTTPProvider, got %T", p)
	}
	if hp.apiBase != "https://custom.modal.example/v1" {
		t.Fatalf("apiBase = %q, want custom modal base", hp.apiBase)
	}
}

func TestCreateProvider_WithFallbackModelsBuildsFallbackProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "claude-opus-4-6"
	cfg.Agents.Defaults.FallbackModels = []string{"glm-5"}
	cfg.Providers.Anthropic.APIKey = "anthropic-key"
	cfg.Providers.Modal.APIKey = "modal-key"

	p, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	fp, ok := p.(*fallbackProvider)
	if !ok {
		t.Fatalf("expected fallbackProvider, got %T", p)
	}
	if fp.primaryModel != "claude-opus-4-6" {
		t.Fatalf("primaryModel = %q, want claude-opus-4-6", fp.primaryModel)
	}
	if len(fp.candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(fp.candidates))
	}
	if fp.candidates[0].model != "claude-opus-4-6" {
		t.Fatalf("candidates[0].model = %q, want claude-opus-4-6", fp.candidates[0].model)
	}
	if fp.candidates[1].model != "glm-5" {
		t.Fatalf("candidates[1].model = %q, want glm-5", fp.candidates[1].model)
	}
}

func TestCreateProvider_WithInvalidFallbackModelKeepsPrimaryProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "claude-opus-4-6"
	cfg.Agents.Defaults.FallbackModels = []string{"glm-5"}
	cfg.Providers.Anthropic.APIKey = "anthropic-key"
	// modal key intentionally missing, glm-5 fallback cannot be created

	p, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := p.(*fallbackProvider); ok {
		t.Fatalf("expected primary provider only when fallbacks are invalid, got fallbackProvider")
	}
}
