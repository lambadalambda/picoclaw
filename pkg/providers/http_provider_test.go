package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
