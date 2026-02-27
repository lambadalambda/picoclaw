package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// validResponse returns a minimal valid OpenAI-format chat completion response.
func validResponse(content string) string {
	return fmt.Sprintf(`{
		"choices": [{
			"message": {"content": %q, "tool_calls": []},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`, content)
}

// emptyChoicesResponse returns a response with no choices (upstream error pattern).
func emptyChoicesResponse() string {
	return `{
		"choices": [],
		"usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	}`
}

// errorFinishResponse returns a response with finish_reason "error" and empty content.
func errorFinishResponse() string {
	return `{
		"choices": [{
			"message": {"content": "", "tool_calls": []},
			"finish_reason": "error"
		}],
		"usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	}`
}

// errorFinishResponseWithContent returns a response with finish_reason "error" but non-empty content.
func errorFinishResponseWithContent(content string) string {
	return fmt.Sprintf(`{
		"choices": [{
			"message": {"content": %q, "tool_calls": []},
			"finish_reason": "error"
		}],
		"usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	}`, content)
}

func newTestMessages() []Message {
	return []Message{{Role: "user", Content: "hello"}}
}

func newTestOptions() map[string]interface{} {
	return map[string]interface{}{"max_tokens": 100}
}

func TestExtractCacheUsageFieldsFromMap_FindsNestedCacheFields(t *testing.T) {
	var payload struct {
		Usage map[string]interface{} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(`{
		"usage": {
			"prompt_tokens": 1200,
			"prompt_tokens_details": {"cached_tokens": 800},
			"cache_creation_input_tokens": 10
		}
	}`), &payload); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	fields := extractCacheUsageFieldsFromMap(payload.Usage)

	if got, ok := fields["usage.prompt_tokens_details.cached_tokens"].(float64); !ok || got != 800 {
		t.Fatalf("cached_tokens = %#v, want 800", fields["usage.prompt_tokens_details.cached_tokens"])
	}
	if got, ok := fields["usage.cache_creation_input_tokens"].(float64); !ok || got != 10 {
		t.Fatalf("cache_creation_input_tokens = %#v, want 10", fields["usage.cache_creation_input_tokens"])
	}
	if _, ok := fields["usage.prompt_tokens"]; ok {
		t.Fatalf("unexpected non-cache field captured: usage.prompt_tokens=%#v", fields["usage.prompt_tokens"])
	}
}

func TestExtractCacheUsageFieldsFromMap_NoCacheFields(t *testing.T) {
	usage := map[string]interface{}{
		"prompt_tokens":     100,
		"completion_tokens": 30,
		"total_tokens":      130,
	}
	fields := extractCacheUsageFieldsFromMap(usage)
	if len(fields) != 0 {
		t.Fatalf("expected no cache fields, got %+v", fields)
	}
}

func TestPromptAndCachedTokensFromUsageMap_OpenAIStyle(t *testing.T) {
	usage := map[string]interface{}{
		"prompt_tokens": 1200.0,
		"prompt_tokens_details": map[string]interface{}{
			"cached_tokens": 800.0,
		},
	}
	cacheFields := extractCacheUsageFieldsFromMap(usage)

	promptTokens, ok := promptTokensFromUsageMap(usage)
	if !ok || promptTokens != 1200 {
		t.Fatalf("promptTokens = %v, %v; want 1200, true", promptTokens, ok)
	}
	cachedTokens, ok := cachedTokensFromUsageMap(usage, cacheFields)
	if !ok || cachedTokens != 800 {
		t.Fatalf("cachedTokens = %v, %v; want 800, true", cachedTokens, ok)
	}
	ratio := roundTo(cachedTokens/promptTokens, 4)
	if ratio != 0.6667 {
		t.Fatalf("ratio = %v, want 0.6667", ratio)
	}
}

func TestPromptAndCachedTokensFromUsageMap_AnthropicStyle(t *testing.T) {
	usage := map[string]interface{}{
		"input_tokens":            1000.0,
		"cache_read_input_tokens": 700.0,
	}
	cacheFields := extractCacheUsageFieldsFromMap(usage)

	promptTokens, ok := promptTokensFromUsageMap(usage)
	if !ok || promptTokens != 1000 {
		t.Fatalf("promptTokens = %v, %v; want 1000, true", promptTokens, ok)
	}
	cachedTokens, ok := cachedTokensFromUsageMap(usage, cacheFields)
	if !ok || cachedTokens != 700 {
		t.Fatalf("cachedTokens = %v, %v; want 700, true", cachedTokens, ok)
	}
}

// newTestProvider creates an HTTPProvider with near-zero backoff for fast tests.
func newTestProvider(apiKey, apiBase string) *HTTPProvider {
	p := NewHTTPProvider(apiKey, apiBase)
	p.retryBaseWait = 1 * time.Millisecond
	p.retryMaxWait = 10 * time.Millisecond
	p.retryJitter = 0
	return p
}

// TestChat_NoRetryOnSuccess verifies that a successful response is returned immediately
// without any retries.
func TestChat_NoRetryOnSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("hi there"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "hi there" {
		t.Fatalf("expected content 'hi there', got: %q", resp.Content)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call, got: %d", calls.Load())
	}
}

// TestChat_RetryOnEmptyChoices verifies that when the provider returns 0 choices,
// the request is retried up to the max, and succeeds if a later attempt returns content.
func TestChat_RetryOnEmptyChoices(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			fmt.Fprint(w, emptyChoicesResponse())
		} else {
			fmt.Fprint(w, validResponse("recovered"))
		}
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "recovered" {
		t.Fatalf("expected content 'recovered', got: %q", resp.Content)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got: %d", calls.Load())
	}
}

// TestChat_RetryOnErrorFinishReason verifies that a response with finish_reason "error"
// and empty content triggers retries.
func TestChat_RetryOnErrorFinishReason(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 2 {
			fmt.Fprint(w, errorFinishResponse())
		} else {
			fmt.Fprint(w, validResponse("ok now"))
		}
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "ok now" {
		t.Fatalf("expected content 'ok now', got: %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got: %d", calls.Load())
	}
}

// TestChat_RetryOnErrorFinishReason_WithContent verifies that finish_reason "error"
// triggers retries even if the message content is non-empty.
func TestChat_RetryOnErrorFinishReason_WithContent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 2 {
			fmt.Fprint(w, errorFinishResponseWithContent("partial"))
		} else {
			fmt.Fprint(w, validResponse("ok now"))
		}
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "ok now" {
		t.Fatalf("expected content 'ok now', got: %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got: %d", calls.Load())
	}
}

func TestNewHTTPProvider_DefaultTimeoutIsSet(t *testing.T) {
	p := NewHTTPProvider("test-key", "https://example.com")
	if p.httpClient == nil {
		t.Fatal("expected httpClient to be non-nil")
	}
	if p.httpClient.Timeout <= 0 {
		t.Fatalf("expected non-zero default http client timeout, got: %s", p.httpClient.Timeout)
	}
}

// TestChat_RetryOnHTTP500 verifies that HTTP 5xx errors trigger retries.
func TestChat_RetryOnHTTP500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error": "internal server error"}`)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, validResponse("after 500"))
		}
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "after 500" {
		t.Fatalf("expected content 'after 500', got: %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got: %d", calls.Load())
	}
}

// TestChat_RetryOnHTTP429 verifies that rate-limit (429) responses trigger retries.
func TestChat_RetryOnHTTP429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error": "rate limited"}`)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, validResponse("after rate limit"))
		}
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "after rate limit" {
		t.Fatalf("expected content 'after rate limit', got: %q", resp.Content)
	}
}

// TestChat_RespectsRetryAfterHeaderSeconds verifies that 429 Retry-After
// headers are respected when scheduling retries.
func TestChat_RespectsRetryAfterHeaderSeconds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error": "rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("after retry-after"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	p.retryMaxWait = 2 * time.Second
	start := time.Now()
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "after retry-after" {
		t.Fatalf("expected content 'after retry-after', got: %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got: %d", calls.Load())
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("expected retry to wait for Retry-After (~1s), elapsed=%v", elapsed)
	}
}

// TestChat_RetryAfterHeaderRespectsRetryMaxWait verifies Retry-After doesn't
// exceed configured retryMaxWait, preventing unbounded sleeps.
func TestChat_RetryAfterHeaderRespectsRetryMaxWait(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error": "rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("after capped retry"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	p.retryMaxWait = 20 * time.Millisecond
	start := time.Now()
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "after capped retry" {
		t.Fatalf("expected content 'after capped retry', got: %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got: %d", calls.Load())
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("expected retry to wait close to retryMaxWait, elapsed=%v", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected capped wait, but retry slept too long: %v", elapsed)
	}
}

// TestChat_RetryOnTransientHTTP401UserNotFound verifies that OpenRouter-style
// transient 401 "User not found" responses are retried.
func TestChat_RetryOnTransientHTTP401UserNotFound(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"User not found.","code":401}}`)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, validResponse("after transient 401"))
		}
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "after transient 401" {
		t.Fatalf("expected content 'after transient 401', got: %q", resp.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got: %d", calls.Load())
	}
}

// TestChat_NoRetryOnHTTP401Other verifies that regular unauthorized responses
// are still treated as non-retryable client errors.
func TestChat_NoRetryOnHTTP401Other(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key","code":401}}`)
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	_, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call (no retry), got: %d", calls.Load())
	}
}

// TestChat_NoRetryOnHTTP400 verifies that client errors (4xx, not 429) are NOT retried.
func TestChat_NoRetryOnHTTP400(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error": "bad request"}`)
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	_, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err == nil {
		t.Fatal("expected error for 400, got nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call (no retry), got: %d", calls.Load())
	}
}

// TestChat_ExhaustedRetriesReturnsLastResponse verifies that if all retries are exhausted
// (empty choices every time), an error is returned.
func TestChat_ExhaustedRetriesReturnsError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, emptyChoicesResponse())
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	_, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	// Should have tried original + retries (expect 6 total: 1 original + 5 retries)
	if calls.Load() != 6 {
		t.Fatalf("expected 6 attempts, got: %d", calls.Load())
	}
}

// TestChat_RetriesRespectContextCancellation verifies that retries stop if
// the context is cancelled.
func TestChat_RetriesRespectContextCancellation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, emptyChoicesResponse())
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	p := newTestProvider("test-key", srv.URL)
	_, err := p.Chat(ctx, newTestMessages(), nil, "test-model", newTestOptions())
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestChat_NewlinePaddedResponse verifies that responses with leading/trailing
// whitespace padding (seen from Friendli via OpenRouter) are handled correctly.
func TestChat_NewlinePaddedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simulate Friendli-style padding: many newlines before JSON
		fmt.Fprint(w, "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n"+validResponse("padded but fine")+"\n\n\n")
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	resp, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.Content != "padded but fine" {
		t.Fatalf("expected content 'padded but fine', got: %q", resp.Content)
	}
}

// TestChat_ProviderRoutingIncludedInRequest verifies that when routing is set,
// it appears as the "provider" object in the request body sent to the API.
func TestChat_ProviderRoutingIncludedInRequest(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("ok"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	p.SetRouting(map[string]interface{}{
		"ignore": []string{"Friendli"},
		"order":  []string{"Together", "DeepInfra"},
	})

	_, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	providerObj, ok := capturedBody["provider"]
	if !ok {
		t.Fatal("expected 'provider' field in request body, not found")
	}
	providerMap, ok := providerObj.(map[string]interface{})
	if !ok {
		t.Fatalf("expected provider to be object, got: %T", providerObj)
	}

	ignoreList, ok := providerMap["ignore"]
	if !ok {
		t.Fatal("expected 'ignore' in provider object")
	}
	items := ignoreList.([]interface{})
	if len(items) != 1 || items[0] != "Friendli" {
		t.Fatalf("expected ignore=[Friendli], got: %v", ignoreList)
	}
}

// TestChat_ProviderRoutingOmittedWhenEmpty verifies that when no routing is set,
// no "provider" field appears in the request body.
func TestChat_ProviderRoutingOmittedWhenEmpty(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("ok"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	// No SetRouting call

	_, err := p.Chat(context.Background(), newTestMessages(), nil, "test-model", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if _, ok := capturedBody["provider"]; ok {
		t.Fatal("expected no 'provider' field in request body when routing is not set")
	}
}

func TestChat_CanonicalizesLegacyAssistantToolCallsInRequest(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("ok"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	messages := []Message{
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{
					ID:   "call_1",
					Name: "exec",
					Arguments: map[string]interface{}{
						"command": "pwd",
					},
				},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "/tmp"},
		{Role: "user", Content: "next"},
	}

	_, err := p.Chat(context.Background(), messages, nil, "glm-5", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	rawMessages, ok := capturedBody["messages"].([]interface{})
	if !ok || len(rawMessages) < 2 {
		t.Fatalf("unexpected messages payload: %#v", capturedBody["messages"])
	}

	assistant, ok := rawMessages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant payload has unexpected type: %T", rawMessages[1])
	}

	rawToolCalls, ok := assistant["tool_calls"].([]interface{})
	if !ok || len(rawToolCalls) != 1 {
		t.Fatalf("assistant.tool_calls has unexpected shape: %#v", assistant["tool_calls"])
	}

	toolCall, ok := rawToolCalls[0].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_call has unexpected type: %T", rawToolCalls[0])
	}

	if got, _ := toolCall["type"].(string); got != "function" {
		t.Fatalf("tool_call.type = %q, want function", got)
	}
	if _, exists := toolCall["name"]; exists {
		t.Fatalf("tool_call should not include top-level name: %#v", toolCall)
	}
	if _, exists := toolCall["arguments"]; exists {
		t.Fatalf("tool_call should not include top-level arguments: %#v", toolCall)
	}

	fn, ok := toolCall["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_call.function has unexpected shape: %#v", toolCall["function"])
	}

	if got, _ := fn["name"].(string); got != "exec" {
		t.Fatalf("function.name = %q, want exec", got)
	}
	fnArgs, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("function.arguments should be string, got: %T", fn["arguments"])
	}
	if !strings.Contains(fnArgs, `"command":"pwd"`) {
		t.Fatalf("function.arguments = %q, want JSON containing command=pwd", fnArgs)
	}
}

func TestChat_EncodesInlineImagePartsForUserMessage(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("ok"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	messages := []Message{
		{
			Role:    "user",
			Content: "Describe this image",
			Parts: []MessagePart{
				{Type: MessagePartTypeImage, Path: imagePath},
			},
		},
	}

	_, err := p.Chat(context.Background(), messages, nil, "gpt-4o", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	rawMessages, ok := capturedBody["messages"].([]interface{})
	if !ok || len(rawMessages) != 1 {
		t.Fatalf("unexpected messages payload: %#v", capturedBody["messages"])
	}

	user, ok := rawMessages[0].(map[string]interface{})
	if !ok {
		t.Fatalf("user payload has unexpected type: %T", rawMessages[0])
	}
	content, ok := user["content"].([]interface{})
	if !ok {
		t.Fatalf("user.content should be array for multimodal input, got: %T", user["content"])
	}
	if len(content) != 2 {
		t.Fatalf("len(user.content) = %d, want 2", len(content))
	}

	textPart, ok := content[0].(map[string]interface{})
	if !ok || textPart["type"] != "text" || textPart["text"] != "Describe this image" {
		t.Fatalf("unexpected text part: %#v", content[0])
	}

	imagePart, ok := content[1].(map[string]interface{})
	if !ok || imagePart["type"] != "image_url" {
		t.Fatalf("unexpected image part: %#v", content[1])
	}
	imageURL, ok := imagePart["image_url"].(map[string]interface{})
	if !ok {
		t.Fatalf("image_url shape invalid: %#v", imagePart["image_url"])
	}
	urlValue, _ := imageURL["url"].(string)
	if !strings.HasPrefix(urlValue, "data:image/png;base64,") {
		t.Fatalf("image_url.url = %q, want data URL prefix", urlValue)
	}
}

func TestChat_ToolResultWithImagePartsAddsSyntheticUserMessage(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, validResponse("ok"))
	}))
	defer srv.Close()

	p := newTestProvider("test-key", srv.URL)
	messages := []Message{
		{Role: "user", Content: "hi"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "image_inspect", Arguments: map[string]interface{}{"sources": []interface{}{"x"}}},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content:    "Image(s) attached for inline inspection.",
			Parts:      []MessagePart{{Type: MessagePartTypeImage, Path: imagePath}},
		},
	}

	_, err := p.Chat(context.Background(), messages, nil, "gpt-4o", newTestOptions())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	rawMessages, ok := capturedBody["messages"].([]interface{})
	if !ok || len(rawMessages) != 4 {
		t.Fatalf("expected 4 wire messages (tool + synthetic user), got: %#v", capturedBody["messages"])
	}

	toolMsg, ok := rawMessages[2].(map[string]interface{})
	if !ok {
		t.Fatalf("tool payload has unexpected type: %T", rawMessages[2])
	}
	if toolMsg["role"] != "tool" {
		t.Fatalf("wire role[2] = %#v, want tool", toolMsg["role"])
	}

	synthetic, ok := rawMessages[3].(map[string]interface{})
	if !ok {
		t.Fatalf("synthetic user payload has unexpected type: %T", rawMessages[3])
	}
	if synthetic["role"] != "user" {
		t.Fatalf("wire role[3] = %#v, want user", synthetic["role"])
	}
	content, ok := synthetic["content"].([]interface{})
	if !ok || len(content) < 2 {
		t.Fatalf("synthetic user content should be multimodal array, got: %#v", synthetic["content"])
	}
	imagePart, ok := content[len(content)-1].(map[string]interface{})
	if !ok || imagePart["type"] != "image_url" {
		t.Fatalf("unexpected synthetic image part: %#v", content[len(content)-1])
	}
}

func TestParseRetryAfterHeader_DeltaSeconds(t *testing.T) {
	d, ok := parseRetryAfterHeader("3")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d != 3*time.Second {
		t.Fatalf("duration = %v, want 3s", d)
	}
}

func TestParseRetryAfterHeader_HTTPDate(t *testing.T) {
	header := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	d, ok := parseRetryAfterHeader(header)
	if !ok {
		t.Fatal("expected ok=true for HTTP-date")
	}
	if d <= 0 {
		t.Fatalf("expected positive duration, got %v", d)
	}
	if d > 3*time.Second {
		t.Fatalf("expected duration close to 2s, got %v", d)
	}
}

func TestParseRetryAfterHeader_Invalid(t *testing.T) {
	if d, ok := parseRetryAfterHeader("not-a-date"); ok {
		t.Fatalf("expected ok=false for invalid header, got duration %v", d)
	}
}

func TestComputeRetryWait_AppliesJitterWhenNoRetryAfter(t *testing.T) {
	p := newTestProvider("test-key", "https://example.com")
	p.retryBaseWait = 100 * time.Millisecond
	p.retryMaxWait = 5 * time.Second
	p.retryJitter = 0.5
	p.randFloat = func() float64 { return 1.0 } // max positive jitter

	wait := p.computeRetryWait(1, 0, false)
	if wait < 149*time.Millisecond || wait > 151*time.Millisecond {
		t.Fatalf("wait = %v, want about 150ms", wait)
	}
}

func TestComputeRetryWait_DoesNotJitterRetryAfterHint(t *testing.T) {
	p := newTestProvider("test-key", "https://example.com")
	p.retryBaseWait = 100 * time.Millisecond
	p.retryMaxWait = 5 * time.Second
	p.retryJitter = 0.9
	p.randFloat = func() float64 { return 0.0 } // would reduce wait if jitter were applied

	wait := p.computeRetryWait(1, 400*time.Millisecond, true)
	if wait != 400*time.Millisecond {
		t.Fatalf("wait = %v, want 400ms", wait)
	}
}
