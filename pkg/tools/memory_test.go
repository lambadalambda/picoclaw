package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
)

func newTestMemoryStore(t *testing.T) *memory.MemoryStore {
	t.Helper()
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	os.MkdirAll(filepath.Join(workspace, "memory"), 0755)

	s, err := memory.NewMemoryStore(filepath.Join(workspace, "memory", "memory.db"), workspace)
	if err != nil {
		t.Fatalf("NewMemoryStore failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- MemorySearchTool ---

func TestMemorySearchTool_Name(t *testing.T) {
	tool := NewMemorySearchTool(nil)
	if tool.Name() != "memory_search" {
		t.Errorf("expected name 'memory_search', got %q", tool.Name())
	}
}

func TestMemorySearchTool_Execute(t *testing.T) {
	store := newTestMemoryStore(t)
	store.Store("user prefers dark mode", "preference", "chat", nil)
	store.Store("user works at Sipeed", "fact", "chat", nil)

	tool := NewMemorySearchTool(store)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "dark mode",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "dark mode") {
		t.Errorf("expected result to contain 'dark mode', got:\n%s", result)
	}
}

func TestMemorySearchTool_WithCategory(t *testing.T) {
	store := newTestMemoryStore(t)
	store.Store("user prefers Go", "preference", "chat", nil)
	store.Store("Go 1.25 released", "event", "chat", nil)

	tool := NewMemorySearchTool(store)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":    "Go",
		"category": "preference",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "prefers Go") {
		t.Errorf("expected filtered result, got:\n%s", result)
	}
	if strings.Contains(result, "released") {
		t.Errorf("should not contain event result with category filter, got:\n%s", result)
	}
}

func TestMemorySearchTool_NoResults(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemorySearchTool(store)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "nonexistent",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "No memories found") {
		t.Errorf("expected 'No memories found', got:\n%s", result)
	}
}

func TestMemorySearchTool_MissingQuery(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemorySearchTool(store)

	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing query")
	}
}

// --- MemoryStoreTool ---

func TestMemoryStoreTool_Name(t *testing.T) {
	tool := NewMemoryStoreTool(nil)
	if tool.Name() != "memory_store" {
		t.Errorf("expected name 'memory_store', got %q", tool.Name())
	}
}

func TestMemoryStoreTool_Execute(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemoryStoreTool(store)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content":  "user likes vim keybindings",
		"category": "preference",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "stored") || !strings.Contains(result, "preference") {
		t.Errorf("expected confirmation with category, got:\n%s", result)
	}

	// Verify it's searchable
	searchTool := NewMemorySearchTool(store)
	searchResult, err := searchTool.Execute(context.Background(), map[string]interface{}{
		"query": "vim",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if !strings.Contains(searchResult, "vim") {
		t.Errorf("stored memory should be searchable, got:\n%s", searchResult)
	}
}

func TestMemoryStoreTool_DefaultCategory(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemoryStoreTool(store)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"content": "some note without category",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "general") {
		t.Errorf("expected default category 'general', got:\n%s", result)
	}
}

func TestMemoryStoreTool_MissingContent(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemoryStoreTool(store)

	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing content")
	}
}

func TestMemoryStoreTool_Parameters(t *testing.T) {
	tool := NewMemoryStoreTool(nil)
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["content"]; !ok {
		t.Error("expected 'content' parameter")
	}
	if _, ok := props["category"]; !ok {
		t.Error("expected 'category' parameter")
	}
}
