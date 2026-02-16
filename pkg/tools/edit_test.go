package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileTool_AllowedDir_AllowsInside(t *testing.T) {
	root := t.TempDir()
	allowedDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	path := filepath.Join(allowedDir, "note.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	tool := NewEditFileTool(allowedDir)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":     path,
		"old_text": "hello",
		"new_text": "hi",
	})
	if err != nil {
		t.Fatalf("expected edit to succeed, got error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "hi world" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestEditFileTool_AllowedDirPrefixBypassRejected(t *testing.T) {
	root := t.TempDir()
	allowedDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Outside allowedDir but shares a raw string prefix with it.
	escapeDir := allowedDir + "-escape"
	if err := os.MkdirAll(escapeDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	outsidePath := filepath.Join(escapeDir, "leak.txt")
	if err := os.WriteFile(outsidePath, []byte("secret value"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	tool := NewEditFileTool(allowedDir)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":     outsidePath,
		"old_text": "secret",
		"new_text": "public",
	})
	if err == nil {
		t.Fatal("expected rejection for path outside allowed directory")
	}
	if !strings.Contains(err.Error(), "outside allowed directory") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ensure outside file was not modified.
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "secret value" {
		t.Fatalf("outside file was modified: %q", string(data))
	}
}
