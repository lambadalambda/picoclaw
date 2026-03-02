package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolRegistry_UnsafeToolsRequireApproval(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(path, []byte("top secret"), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	gate := NewUnsafeToolGate(50 * time.Millisecond)
	registry := NewToolRegistry()
	registry.SetUnsafeToolGate(gate)
	registry.Register(NewUnsafeReadFileTool())
	registry.Register(NewReadFileTool(root))

	sessionKey := "telegram:123"
	args := map[string]interface{}{
		"path":                 path,
		"__context_session_key": sessionKey,
	}

	_, err := registry.ExecuteWithContext(context.Background(), "unsafe_read_file", args, "", "")
	if err == nil {
		t.Fatalf("expected unsafe tool to be blocked")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "requires explicit user approval") {
		t.Fatalf("unexpected error: %v", err)
	}

	gate.Approve(sessionKey, time.Second)
	got, err := registry.ExecuteWithContext(context.Background(), "unsafe_read_file", args, "", "")
	if err != nil {
		t.Fatalf("expected unsafe tool to succeed after approval: %v", err)
	}
	if got != "top secret" {
		t.Fatalf("got %q, want %q", got, "top secret")
	}
}
