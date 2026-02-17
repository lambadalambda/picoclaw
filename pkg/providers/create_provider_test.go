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
