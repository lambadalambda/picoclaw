package vision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClientAnalyzeImages_SendsOpenAIStyleVisionRequest(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "sample.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var gotAuth string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("failed to parse request body: %v", err)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Detected a screenshot with a stack trace."}}]}`))
	}))
	defer srv.Close()

	client := NewClient("test-key", srv.URL, "glm-4.6v")
	client.Timeout = 2 * time.Second

	result, err := client.AnalyzeImages(context.Background(), "Explain this image", []string{imagePath})
	if err != nil {
		t.Fatalf("AnalyzeImages returned error: %v", err)
	}
	if !strings.Contains(result, "stack trace") {
		t.Fatalf("unexpected analysis result: %q", result)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization header = %q, want Bearer token", gotAuth)
	}

	model, _ := gotBody["model"].(string)
	if model != "glm-4.6v" {
		t.Fatalf("model = %q, want glm-4.6v", model)
	}

	messages, ok := gotBody["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("messages shape invalid: %#v", gotBody["messages"])
	}
	msg, ok := messages[0].(map[string]interface{})
	if !ok {
		t.Fatalf("message item shape invalid: %#v", messages[0])
	}
	content, ok := msg["content"].([]interface{})
	if !ok || len(content) < 2 {
		t.Fatalf("message content invalid: %#v", msg["content"])
	}

	second, ok := content[1].(map[string]interface{})
	if !ok {
		t.Fatalf("content item shape invalid: %#v", content[1])
	}
	if second["type"] != "image_url" {
		t.Fatalf("content[1].type = %v, want image_url", second["type"])
	}
	imageURL, ok := second["image_url"].(map[string]interface{})
	if !ok {
		t.Fatalf("image_url shape invalid: %#v", second["image_url"])
	}
	urlValue, _ := imageURL["url"].(string)
	if !strings.HasPrefix(urlValue, "data:image/png;base64,") {
		t.Fatalf("image data URL prefix mismatch: %q", urlValue)
	}
}

func TestClientAnalyzeImages_PropagatesAPIError(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "sample.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad image"}`))
	}))
	defer srv.Close()

	client := NewClient("test-key", srv.URL, "glm-4.6v")
	_, err := client.AnalyzeImages(context.Background(), "Explain", []string{imagePath})
	if err == nil {
		t.Fatal("expected AnalyzeImages to fail on HTTP 400")
	}
	if !strings.Contains(err.Error(), "bad image") {
		t.Fatalf("error = %v, want API response text", err)
	}
}
