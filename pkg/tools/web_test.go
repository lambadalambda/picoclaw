package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchTool_UsesZAIBackendWhenConfigured(t *testing.T) {
	var gotAuth string
	var gotBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/paas/v4/web_search" {
			t.Fatalf("path = %s, want /paas/v4/web_search", r.URL.Path)
		}

		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("failed to parse request body: %v", err)
		}

		_, _ = w.Write([]byte(`{"search_result":[{"title":"ZAI result","link":"https://example.com/a","content":"snippet","media":"Example"}]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchToolConfig{
		Provider:        "zai",
		ZAIAPIKey:       "zai-key",
		ZAIAPIBase:      server.URL,
		ZAISearchEngine: "search-prime",
		MaxResults:      5,
	})
	tool.httpClient = server.Client()

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "latest golang release",
		"count": float64(2),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if gotAuth != "Bearer zai-key" {
		t.Fatalf("Authorization header = %q, want Bearer zai-key", gotAuth)
	}
	if gotBody["search_query"] != "latest golang release" {
		t.Fatalf("search_query = %v, want latest golang release", gotBody["search_query"])
	}
	if gotBody["search_engine"] != "search-prime" {
		t.Fatalf("search_engine = %v, want search-prime", gotBody["search_engine"])
	}
	if !strings.Contains(result, "ZAI result") || !strings.Contains(result, "https://example.com/a") {
		t.Fatalf("unexpected formatted output: %q", result)
	}
}

func TestWebSearchTool_AutoFallsBackToBrave(t *testing.T) {
	var gotToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/res/v1/web/search" {
			t.Fatalf("path = %s, want /res/v1/web/search", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "picoclaw" {
			t.Fatalf("query q = %q, want picoclaw", r.URL.Query().Get("q"))
		}
		gotToken = r.Header.Get("X-Subscription-Token")

		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Brave result","url":"https://example.com/b","description":"desc"}]}}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchToolConfig{
		Provider:    "auto",
		BraveAPIKey: "brave-key",
		MaxResults:  5,
	})
	tool.httpClient = server.Client()
	tool.braveAPIBase = server.URL

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "picoclaw"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if gotToken != "brave-key" {
		t.Fatalf("X-Subscription-Token = %q, want brave-key", gotToken)
	}
	if !strings.Contains(result, "Brave result") {
		t.Fatalf("unexpected formatted output: %q", result)
	}
}

func TestWebSearchTool_AutoWithoutKeysReturnsConfigError(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolConfig{Provider: "auto"})

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "picoclaw"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result, "not configured") {
		t.Fatalf("result = %q, want configuration error", result)
	}
}
