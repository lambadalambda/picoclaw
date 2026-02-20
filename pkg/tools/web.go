package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	userAgent               = "Mozilla/5.0 (compatible; picoclaw/1.0)"
	defaultZAISearchAPIBase = "https://api.z.ai/api"
	defaultZAISearchMCPURL  = "https://api.z.ai/api/mcp/web_search_prime/mcp"
)

type WebSearchToolConfig struct {
	BraveAPIKey     string
	MaxResults      int
	Provider        string
	ZAIAPIKey       string
	ZAIAPIBase      string
	ZAIMCPURL       string
	ZAISearchEngine string
}

type WebSearchTool struct {
	braveAPIKey     string
	maxResults      int
	provider        string
	zaiAPIKey       string
	zaiAPIBase      string
	zaiMCPURL       string
	zaiSearchEngine string
	braveAPIBase    string
	httpClient      *http.Client
}

func NewWebSearchTool(cfg WebSearchToolConfig) *WebSearchTool {
	maxResults := cfg.MaxResults
	if maxResults <= 0 || maxResults > 10 {
		maxResults = 5
	}

	zaiSearchEngine := strings.TrimSpace(cfg.ZAISearchEngine)
	if zaiSearchEngine == "" {
		zaiSearchEngine = "search-prime"
	}

	return &WebSearchTool{
		braveAPIKey:     strings.TrimSpace(cfg.BraveAPIKey),
		maxResults:      maxResults,
		provider:        strings.ToLower(strings.TrimSpace(cfg.Provider)),
		zaiAPIKey:       strings.TrimSpace(cfg.ZAIAPIKey),
		zaiAPIBase:      strings.TrimSpace(cfg.ZAIAPIBase),
		zaiMCPURL:       strings.TrimSpace(cfg.ZAIMCPURL),
		zaiSearchEngine: zaiSearchEngine,
		braveAPIBase:    "https://api.search.brave.com",
	}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for current information. Returns titles, URLs, and snippets from search results."
}

func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (1-10)",
				"minimum":     1.0,
				"maximum":     10.0,
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok {
		return "", fmt.Errorf("query is required")
	}

	count := t.maxResults
	if c, ok := args["count"].(float64); ok {
		if int(c) > 0 && int(c) <= 10 {
			count = int(c)
		}
	}

	backend := t.resolveSearchBackend()
	switch backend {
	case "zai":
		if t.zaiAPIKey == "" {
			return "Error: ZAI web search API key not configured", nil
		}
		result, err := t.executeZAISearch(ctx, query, count)
		if err == nil {
			return result, nil
		}

		if t.isAutoProvider() && t.braveAPIKey != "" {
			fallbackResult, fallbackErr := t.executeBraveSearch(ctx, query, count)
			if fallbackErr == nil {
				return fallbackResult, nil
			}
			return "", fmt.Errorf("z.ai web search failed: %w; brave fallback failed: %v", err, fallbackErr)
		}

		return "", err
	case "brave":
		if t.braveAPIKey == "" {
			if t.zaiAPIKey == "" {
				return "Error: web_search is not configured (set BRAVE API key or ZAI web search API key)", nil
			}
			return "Error: BRAVE_API_KEY not configured", nil
		}
		return t.executeBraveSearch(ctx, query, count)
	default:
		return "", fmt.Errorf("unsupported web search provider: %s", backend)
	}
}

func (t *WebSearchTool) isAutoProvider() bool {
	provider := strings.ToLower(strings.TrimSpace(t.provider))
	return provider == "" || provider == "auto"
}

func (t *WebSearchTool) resolveSearchBackend() string {
	provider := strings.ToLower(strings.TrimSpace(t.provider))
	switch provider {
	case "zai", "brave":
		return provider
	default:
		if t.zaiAPIKey != "" {
			return "zai"
		}
		return "brave"
	}
}

func (t *WebSearchTool) executeBraveSearch(ctx context.Context, query string, count int) (string, error) {
	braveAPIBase := strings.TrimRight(strings.TrimSpace(t.braveAPIBase), "/")
	if braveAPIBase == "" {
		braveAPIBase = "https://api.search.brave.com"
	}

	searchURL := fmt.Sprintf("%s/res/v1/web/search?q=%s&count=%d",
		braveAPIBase, url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.braveAPIKey)

	client := t.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var searchResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	results := searchResp.Web.Results
	if len(results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s", query))
	for i, item := range results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
		if item.Description != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Description))
		}
	}

	return strings.Join(lines, "\n"), nil
}

func (t *WebSearchTool) executeZAISearch(ctx context.Context, query string, count int) (string, error) {
	mcpEnabled := strings.TrimSpace(t.zaiMCPURL) != "-"
	mcpErr := fmt.Errorf("disabled")
	if mcpEnabled {
		mcpResult, err := t.executeZAISearchMCP(ctx, query, count)
		if err == nil {
			return mcpResult, nil
		}
		mcpErr = err
	}

	directResult, directErr := t.executeZAISearchAPI(ctx, query, count)
	if directErr == nil {
		return directResult, nil
	}
	if !mcpEnabled {
		return "", directErr
	}

	return "", fmt.Errorf("z.ai search failed (mcp: %v; api: %v)", mcpErr, directErr)
}

type zaiSearchResultItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Content string `json:"content"`
	Media   string `json:"media"`
}

func formatZAISearchResults(query string, count int, items []zaiSearchResultItem) string {
	if len(items) == 0 {
		return fmt.Sprintf("No results for: %s", query)
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s", query))
	for i, item := range items {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.Link))
		if item.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Content))
		}
		if item.Media != "" {
			lines = append(lines, fmt.Sprintf("   Source: %s", item.Media))
		}
	}

	return strings.Join(lines, "\n")
}

func (t *WebSearchTool) executeZAISearchAPI(ctx context.Context, query string, count int) (string, error) {
	apiBase := normalizeZAISearchAPIBase(t.zaiAPIBase)

	reqBody := map[string]interface{}{
		"search_engine": t.zaiSearchEngine,
		"search_query":  query,
		"count":         count,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal z.ai search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/paas/v4/web_search", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create z.ai search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.zaiAPIKey)
	req.Header.Set("Accept", "application/json")

	client := t.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("z.ai search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read z.ai search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("z.ai web search API error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var searchResp struct {
		SearchResult []zaiSearchResultItem `json:"search_result"`
	}

	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("failed to parse z.ai search response: %w", err)
	}

	return formatZAISearchResults(query, count, searchResp.SearchResult), nil
}

func (t *WebSearchTool) executeZAISearchMCP(ctx context.Context, query string, count int) (string, error) {
	mcpURL := strings.TrimSpace(t.zaiMCPURL)
	if mcpURL == "" {
		mcpURL = defaultZAISearchMCPURL
	}

	client := t.httpClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "init-1",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "picoclaw-web-search",
				"version": "1.0",
			},
		},
	}

	_, initHeaders, _, err := t.postZAIMCP(ctx, client, mcpURL, "", initReq)
	if err != nil {
		return "", fmt.Errorf("mcp initialize failed: %w", err)
	}
	sessionID := strings.TrimSpace(initHeaders.Get("Mcp-Session-Id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(initHeaders.Get("mcp-session-id"))
	}
	if sessionID == "" {
		return "", fmt.Errorf("mcp initialize did not return session id")
	}

	_, _, _, _ = t.postZAIMCP(ctx, client, mcpURL, sessionID, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	})

	_, _, callBody, err := t.postZAIMCP(ctx, client, mcpURL, sessionID, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "call-1",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "webSearchPrime",
			"arguments": map[string]interface{}{
				"search_query": query,
				"count":        count,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("mcp tools/call failed: %w", err)
	}

	env, err := parseMCPEnvelope(callBody)
	if err != nil {
		return "", fmt.Errorf("failed to parse mcp response: %w", err)
	}
	if env.Error != nil {
		errPayload, _ := json.Marshal(env.Error)
		return "", fmt.Errorf("mcp error: %s", strings.TrimSpace(string(errPayload)))
	}
	if len(env.Result.Content) == 0 {
		return "", fmt.Errorf("mcp returned empty content")
	}

	text := strings.TrimSpace(env.Result.Content[0].Text)
	if text == "" {
		return "", fmt.Errorf("mcp returned empty text")
	}

	unquoted := ""
	if err := json.Unmarshal([]byte(text), &unquoted); err == nil && strings.TrimSpace(unquoted) != "" {
		text = strings.TrimSpace(unquoted)
	}

	var items []zaiSearchResultItem
	if err := json.Unmarshal([]byte(text), &items); err == nil {
		return formatZAISearchResults(query, count, items), nil
	}

	if strings.Contains(strings.ToLower(text), "error") {
		return "", fmt.Errorf("mcp tool returned error text: %s", text)
	}

	return fmt.Sprintf("Results for: %s\n%s", query, text), nil
}

type mcpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpEnvelope struct {
	Error  interface{} `json:"error"`
	Result struct {
		Content []mcpContentBlock `json:"content"`
	} `json:"result"`
}

func parseMCPEnvelope(body []byte) (*mcpEnvelope, error) {
	data := extractSSEDataPayload(body)
	if len(data) == 0 {
		return nil, fmt.Errorf("no data payload in mcp response")
	}

	var env mcpEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func extractSSEDataPayload(body []byte) []byte {
	lines := strings.Split(string(body), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []byte(strings.Join(parts, "\n"))
}

func (t *WebSearchTool) postZAIMCP(ctx context.Context, client *http.Client, mcpURL, sessionID string, payload map[string]interface{}) (int, http.Header, []byte, error) {
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to marshal mcp payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(requestBody))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to create mcp request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.zaiAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if strings.TrimSpace(sessionID) != "" {
		req.Header.Set("Mcp-Session-Id", strings.TrimSpace(sessionID))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to read mcp response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, resp.Header, body, fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return resp.StatusCode, resp.Header, body, nil
}

func normalizeZAISearchAPIBase(rawBase string) string {
	apiBase := strings.TrimRight(strings.TrimSpace(rawBase), "/")
	if apiBase == "" {
		return defaultZAISearchAPIBase
	}

	lower := strings.ToLower(apiBase)
	for {
		switch {
		case strings.HasSuffix(lower, "/coding/paas/v4"):
			apiBase = apiBase[:len(apiBase)-len("/coding/paas/v4")]
		case strings.HasSuffix(lower, "/paas/v4"):
			apiBase = apiBase[:len(apiBase)-len("/paas/v4")]
		default:
			goto done
		}
		apiBase = strings.TrimRight(apiBase, "/")
		lower = strings.ToLower(apiBase)
	}

done:
	if apiBase == "" {
		return defaultZAISearchAPIBase
	}
	return apiBase
}

type WebFetchTool struct {
	maxChars int
}

func NewWebFetchTool(maxChars int) *WebFetchTool {
	if maxChars <= 0 {
		maxChars = 50000
	}
	return &WebFetchTool{
		maxChars: maxChars,
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch a URL and extract readable content (HTML to text). Use this to get weather info, news, articles, or any web content."
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to fetch",
			},
			"maxChars": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum characters to extract",
				"minimum":     100.0,
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	urlStr, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("url is required")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("only http/https URLs are allowed")
	}

	if parsedURL.Host == "" {
		return "", fmt.Errorf("missing domain in URL")
	}

	maxChars := t.maxChars
	if mc, ok := args["maxChars"].(float64); ok {
		if int(mc) > 100 {
			maxChars = int(mc)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  false,
			TLSHandshakeTimeout: 15 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")

	var text, extractor string

	if strings.Contains(contentType, "application/json") {
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			formatted, _ := json.MarshalIndent(jsonData, "", "  ")
			text = string(formatted)
			extractor = "json"
		} else {
			text = string(body)
			extractor = "raw"
		}
	} else if strings.Contains(contentType, "text/html") || len(body) > 0 &&
		(strings.HasPrefix(string(body), "<!DOCTYPE") || strings.HasPrefix(strings.ToLower(string(body)), "<html")) {
		text = t.extractText(string(body))
		extractor = "text"
	} else {
		text = string(body)
		extractor = "raw"
	}

	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars]
	}

	result := map[string]interface{}{
		"url":       urlStr,
		"status":    resp.StatusCode,
		"extractor": extractor,
		"truncated": truncated,
		"length":    len(text),
		"text":      text,
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	return string(resultJSON), nil
}

func (t *WebFetchTool) extractText(htmlContent string) string {
	re := regexp.MustCompile(`<script[\s\S]*?</script>`)
	result := re.ReplaceAllLiteralString(htmlContent, "")
	re = regexp.MustCompile(`<style[\s\S]*?</style>`)
	result = re.ReplaceAllLiteralString(result, "")
	re = regexp.MustCompile(`<[^>]+>`)
	result = re.ReplaceAllLiteralString(result, "")

	result = strings.TrimSpace(result)

	re = regexp.MustCompile(`\s+`)
	result = re.ReplaceAllLiteralString(result, " ")

	lines := strings.Split(result, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}
