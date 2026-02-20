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
		ZAIMCPURL:       "-",
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

func TestWebSearchTool_AutoFallsBackToBraveWhenZAIRequestFails(t *testing.T) {
	zaiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer zaiServer.Close()

	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/web/search" {
			t.Fatalf("path = %s, want /res/v1/web/search", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Brave fallback","url":"https://example.com/fallback","description":"ok"}]}}`))
	}))
	defer braveServer.Close()

	tool := NewWebSearchTool(WebSearchToolConfig{
		Provider:    "auto",
		BraveAPIKey: "brave-key",
		ZAIAPIKey:   "zai-key",
		ZAIAPIBase:  zaiServer.URL,
		ZAIMCPURL:   "-",
		MaxResults:  5,
	})
	tool.braveAPIBase = braveServer.URL
	tool.httpClient = &http.Client{}

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "picoclaw"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result, "Brave fallback") {
		t.Fatalf("expected Brave fallback result, got: %q", result)
	}
}

func TestWebSearchTool_UsesZAIMCPWhenAvailable(t *testing.T) {
	const sessionID = "session-123"
	var sawToolCall bool
	var gotSearchQuery string
	var gotLocation string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Fatalf("path = %s, want /mcp", r.URL.Path)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to parse request body: %v", err)
		}

		method, _ := payload["method"].(string)
		w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")

		switch method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", sessionID)
			_, _ = w.Write([]byte("id:1\nevent:message\ndata:{\"jsonrpc\":\"2.0\",\"id\":\"init-1\",\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{\"tools\":{}},\"serverInfo\":{\"name\":\"mcp-web-search-prime\",\"version\":\"0.0.1\"}}}\n\n"))
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != sessionID {
				t.Fatalf("missing/invalid session id on initialized notification")
			}
			_, _ = w.Write([]byte("id:2\nevent:message\ndata:{\"jsonrpc\":\"2.0\",\"result\":{}}\n\n"))
		case "tools/call":
			sawToolCall = true
			if r.Header.Get("Mcp-Session-Id") != sessionID {
				t.Fatalf("missing/invalid session id on tools/call")
			}
			params, _ := payload["params"].(map[string]interface{})
			args, _ := params["arguments"].(map[string]interface{})
			gotSearchQuery, _ = args["search_query"].(string)
			gotLocation, _ = args["location"].(string)
			_, _ = w.Write([]byte("id:3\nevent:message\ndata:{\"jsonrpc\":\"2.0\",\"id\":\"call-1\",\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"[{\\\"title\\\":\\\"MCP Result\\\",\\\"link\\\":\\\"https://example.com/mcp\\\",\\\"content\\\":\\\"snippet\\\",\\\"media\\\":\\\"Example\\\"}]\"}]}}\n\n"))
		default:
			t.Fatalf("unexpected method: %s", method)
		}
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchToolConfig{
		Provider:    "zai",
		ZAIAPIKey:   "zai-key",
		ZAIMCPURL:   server.URL + "/mcp",
		ZAIAPIBase:  "https://invalid.local",
		ZAILocation: "us",
		MaxResults:  5,
	})
	tool.httpClient = server.Client()

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "latest golang release"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !sawToolCall {
		t.Fatal("expected MCP tools/call to be invoked")
	}
	if gotSearchQuery != "latest golang release" {
		t.Fatalf("search_query = %q, want latest golang release", gotSearchQuery)
	}
	if gotLocation != "us" {
		t.Fatalf("location = %q, want us", gotLocation)
	}
	if !strings.Contains(result, "MCP Result") {
		t.Fatalf("unexpected formatted output: %q", result)
	}
}

func TestNormalizeZAILocation(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "us", want: "us"},
		{in: "US", want: "us"},
		{in: "cn", want: "cn"},
		{in: "  cn  ", want: "cn"},
		{in: "eu", want: ""},
		{in: "", want: ""},
	}

	for _, tc := range tests {
		got := normalizeZAILocation(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeZAILocation(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWebSearchTool_NormalizesZAIAPIBaseFromCodingPath(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"search_result":[{"title":"ZAI result","link":"https://example.com","content":"ok"}]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchToolConfig{
		Provider:   "zai",
		ZAIAPIKey:  "zai-key",
		ZAIAPIBase: server.URL + "/coding/paas/v4",
		ZAIMCPURL:  "-",
	})
	tool.httpClient = server.Client()

	_, err := tool.Execute(context.Background(), map[string]interface{}{"query": "picoclaw"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if gotPath != "/paas/v4/web_search" {
		t.Fatalf("request path = %q, want /paas/v4/web_search", gotPath)
	}
}

func TestNormalizeZAISearchAPIBase(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: defaultZAISearchAPIBase},
		{name: "already root", in: "https://api.z.ai/api", want: "https://api.z.ai/api"},
		{name: "zhipu base", in: "https://open.bigmodel.cn/api/paas/v4", want: "https://open.bigmodel.cn/api"},
		{name: "coding base", in: "https://api.z.ai/api/coding/paas/v4", want: "https://api.z.ai/api"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeZAISearchAPIBase(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeZAISearchAPIBase(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
