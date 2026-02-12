package providers

import (
	"context"
	"fmt"
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
