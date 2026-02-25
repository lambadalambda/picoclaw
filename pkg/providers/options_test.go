package providers

import "testing"

func TestChatOptions_ToMap(t *testing.T) {
	opts := ChatOptions{MaxTokens: 1024, Temperature: 0.3}.ToMap()

	if got, ok := opts["max_tokens"].(int); !ok || got != 1024 {
		t.Fatalf("max_tokens = %#v, want 1024", opts["max_tokens"])
	}
	if got, ok := opts["temperature"].(float64); !ok || got != 0.3 {
		t.Fatalf("temperature = %#v, want 0.3", opts["temperature"])
	}
}

func TestChatOptions_ToMap_OmitsNonPositiveMaxTokens(t *testing.T) {
	opts := ChatOptions{MaxTokens: 0, Temperature: 0.7}.ToMap()

	if _, ok := opts["max_tokens"]; ok {
		t.Fatal("expected max_tokens to be omitted when <= 0")
	}
	if got, ok := opts["temperature"].(float64); !ok || got != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", opts["temperature"])
	}
}

func TestChatOptions_ToMap_IncludesAnthropicCacheFlags(t *testing.T) {
	opts := ChatOptions{
		MaxTokens:         512,
		Temperature:       0.2,
		AnthropicCache:    true,
		AnthropicCacheTTL: "1h",
	}.ToMap()

	if got, ok := opts["anthropic_cache"].(bool); !ok || !got {
		t.Fatalf("anthropic_cache = %#v, want true", opts["anthropic_cache"])
	}
	if got, ok := opts["anthropic_cache_ttl"].(string); !ok || got != "1h" {
		t.Fatalf("anthropic_cache_ttl = %#v, want 1h", opts["anthropic_cache_ttl"])
	}
}

func TestChatOptions_ToMap_OmitsEmptyAnthropicCacheTTL(t *testing.T) {
	opts := ChatOptions{Temperature: 0.7, AnthropicCacheTTL: "   "}.ToMap()

	if _, ok := opts["anthropic_cache_ttl"]; ok {
		t.Fatal("expected anthropic_cache_ttl to be omitted when empty")
	}
}
