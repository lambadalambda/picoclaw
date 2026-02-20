package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolveZAISearchCredentials_PrefersExplicitToolConfig(t *testing.T) {
	webCfg := config.WebSearchConfig{
		ZAIAPIKey:  "tool-zai-key",
		ZAIAPIBase: "https://custom.z.ai/api",
	}
	providersCfg := config.ProvidersConfig{
		Zhipu: config.ProviderConfig{APIKey: "zhipu-key", APIBase: "https://zhipu-base.example"},
		Modal: config.ProviderConfig{APIKey: "modal-key", APIBase: "https://modal.example"},
	}

	key, base := resolveZAISearchCredentials(webCfg, providersCfg)
	if key != "tool-zai-key" {
		t.Fatalf("key = %q, want tool-zai-key", key)
	}
	if base != "https://custom.z.ai/api" {
		t.Fatalf("base = %q, want explicit tool base", base)
	}
}

func TestResolveZAISearchCredentials_FallbackOrder(t *testing.T) {
	webCfg := config.WebSearchConfig{}

	providersCfg := config.ProvidersConfig{
		Zhipu: config.ProviderConfig{APIKey: "zhipu-key", APIBase: "https://zhipu-base.example"},
		Modal: config.ProviderConfig{APIKey: "modal-key", APIBase: "https://modal.example"},
	}

	key, base := resolveZAISearchCredentials(webCfg, providersCfg)
	if key != "zhipu-key" {
		t.Fatalf("key = %q, want zhipu-key", key)
	}
	if base != "https://zhipu-base.example" {
		t.Fatalf("base = %q, want zhipu base", base)
	}
}

func TestResolveZAISearchCredentials_UsesModalKeyWhenZhipuMissing(t *testing.T) {
	webCfg := config.WebSearchConfig{}

	providersCfg := config.ProvidersConfig{
		Modal: config.ProviderConfig{APIKey: "modal-key", APIBase: "https://modal.example"},
	}

	key, base := resolveZAISearchCredentials(webCfg, providersCfg)
	if key != "modal-key" {
		t.Fatalf("key = %q, want modal-key", key)
	}
	if base != "" {
		t.Fatalf("base = %q, want empty (tool default should apply)", base)
	}
}
