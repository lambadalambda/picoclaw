package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type recordingImageAnalyzer struct {
	result string
	err    error

	calls  int
	prompt string
	paths  []string
}

func (a *recordingImageAnalyzer) AnalyzeImages(_ context.Context, prompt string, imagePaths []string) (string, error) {
	a.calls++
	a.prompt = prompt
	a.paths = append([]string(nil), imagePaths...)
	if a.err != nil {
		return "", a.err
	}
	return a.result, nil
}

func tinyPNGBytes(t *testing.T) []byte {
	t.Helper()
	const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/w8AAgMBgN0lZ0QAAAAASUVORK5CYII="
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode png fixture: %v", err)
	}
	return data
}

func writeTempImage(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, tinyPNGBytes(t), 0600); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}
	return path
}

func TestImageInspectTool_LocalSourceUsesPrimaryAnalyzer(t *testing.T) {
	workspace := t.TempDir()
	_ = writeTempImage(t, workspace, "shot.png")

	primary := &recordingImageAnalyzer{result: "Primary analysis"}
	tool := NewImageInspectTool(workspace, primary, "primary-model", nil, "")

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"sources":  []interface{}{"shot.png"},
		"question": "read all visible text",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if len(primary.paths) != 1 {
		t.Fatalf("primary paths len = %d, want 1", len(primary.paths))
	}
	if !strings.HasSuffix(primary.paths[0], "/shot.png") {
		t.Fatalf("unexpected analyzed path: %q", primary.paths[0])
	}
	if !strings.Contains(primary.prompt, "read all visible text") {
		t.Fatalf("prompt = %q, want question text", primary.prompt)
	}
	if !strings.Contains(out, "backend: primary-model") {
		t.Fatalf("output missing backend label: %q", out)
	}
	if !strings.Contains(out, "Primary analysis") {
		t.Fatalf("output missing analysis text: %q", out)
	}
}

func TestImageInspectTool_URLSourceDownloadsAndCleansTempFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNGBytes(t))
	}))
	defer server.Close()

	primary := &recordingImageAnalyzer{result: "URL analysis"}
	tool := NewImageInspectTool(t.TempDir(), primary, "primary-model", nil, "")
	tool.allowPrivateHosts = true

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"sources": []interface{}{server.URL + "/screen.png"},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if len(primary.paths) != 1 {
		t.Fatalf("primary paths len = %d, want 1", len(primary.paths))
	}
	if _, statErr := os.Stat(primary.paths[0]); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("downloaded temp file should be removed, stat err = %v", statErr)
	}
	if !strings.Contains(out, "kind=url") {
		t.Fatalf("output missing URL source info: %q", out)
	}
}

func TestImageInspectTool_BlocksPrivateHostsByDefault(t *testing.T) {
	tool := NewImageInspectTool(t.TempDir(), &recordingImageAnalyzer{result: "ok"}, "primary-model", nil, "")

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"sources": []interface{}{"http://127.0.0.1/local.png"},
	})
	if err == nil {
		t.Fatal("Execute should fail for private host URL")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Fatalf("error = %q, want private host message", err)
	}
}

func TestImageInspectTool_FallsBackWhenPrimaryFails(t *testing.T) {
	workspace := t.TempDir()
	_ = writeTempImage(t, workspace, "sample.png")

	primary := &recordingImageAnalyzer{err: errors.New("primary down")}
	fallback := &recordingImageAnalyzer{result: "Fallback analysis"}
	tool := NewImageInspectTool(workspace, primary, "primary-model", fallback, "fallback-model")

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"sources": []interface{}{"sample.png"},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls)
	}
	if !strings.Contains(out, "backend: fallback-model") {
		t.Fatalf("output missing fallback backend label: %q", out)
	}
}

func TestImageInspectTool_ReturnsWarningsForSkippedSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bad.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("not an image"))
		default:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tinyPNGBytes(t))
		}
	}))
	defer server.Close()

	primary := &recordingImageAnalyzer{result: "Mixed analysis"}
	tool := NewImageInspectTool(t.TempDir(), primary, "primary-model", nil, "")
	tool.allowPrivateHosts = true

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"sources": []interface{}{server.URL + "/bad.txt", server.URL + "/ok.png"},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "Warnings:") {
		t.Fatalf("output missing warnings section: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "did not return image content") {
		t.Fatalf("output missing non-image warning: %q", out)
	}
}

func TestImageInspectTool_InlineModeReturnsPartsWithoutAnalyzer(t *testing.T) {
	workspace := t.TempDir()
	imgPath := writeTempImage(t, workspace, "shot.png")

	primary := &recordingImageAnalyzer{result: "Primary analysis"}
	tool := NewImageInspectTool(workspace, primary, "primary-model", nil, "")

	rich, ok := interface{}(tool).(ToolWithResult)
	if !ok {
		t.Fatalf("ImageInspectTool should implement ToolWithResult")
	}

	res, err := rich.ExecuteResult(context.Background(), map[string]interface{}{
		"sources":                 []interface{}{imgPath},
		"__context_inline_vision": true,
		"question":                "what is shown?",
	})
	if err != nil {
		t.Fatalf("ExecuteResult failed: %v", err)
	}
	if primary.calls != 0 {
		t.Fatalf("primary calls = %d, want 0", primary.calls)
	}
	if len(res.Parts) != 1 {
		t.Fatalf("len(res.Parts) = %d, want 1", len(res.Parts))
	}
	if res.Parts[0].Type != providers.MessagePartTypeImage {
		t.Fatalf("Parts[0].Type = %q, want %q", res.Parts[0].Type, providers.MessagePartTypeImage)
	}
	if res.Parts[0].Path != imgPath {
		t.Fatalf("Parts[0].Path = %q, want %q", res.Parts[0].Path, imgPath)
	}
	if !strings.Contains(strings.ToLower(res.Content), "attached") {
		t.Fatalf("content should mention attached images, got: %q", res.Content)
	}
}
