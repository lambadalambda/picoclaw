package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	os.MkdirAll(filepath.Join(workspace, "memory"), 0755)

	s, err := NewMemoryStore(filepath.Join(workspace, "memory", "memory.db"), workspace)
	if err != nil {
		t.Fatalf("NewMemoryStore failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- Schema and lifecycle ---

func TestNewMemoryStore(t *testing.T) {
	s := newTestStore(t)
	if s == nil {
		t.Fatal("expected non-nil MemoryStore")
	}
}

func TestSchemaVersion(t *testing.T) {
	s := newTestStore(t)
	version, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion failed: %v", err)
	}
	if version != 1 {
		t.Errorf("expected schema version 1, got %d", version)
	}
}

// --- Store ---

func TestStore(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Store("user prefers dark mode", "preference", "chat", nil)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}
}

func TestStore_WithMetadata(t *testing.T) {
	s := newTestStore(t)

	meta := map[string]string{"source_channel": "telegram", "user": "alice"}
	id, err := s.Store("important fact", "fact", "chat", meta)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	mem, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if mem.Metadata["source_channel"] != "telegram" {
		t.Errorf("expected metadata source_channel=telegram, got %v", mem.Metadata)
	}
}

func TestStore_WritesToMarkdown_Preference(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Store("user likes vim", "preference", "chat", nil)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Should be appended to MEMORY.md
	memoryFile := filepath.Join(s.workspace, "memory", "MEMORY.md")
	data, err := os.ReadFile(memoryFile)
	if err != nil {
		t.Fatalf("failed to read MEMORY.md: %v", err)
	}
	if !strings.Contains(string(data), "user likes vim") {
		t.Errorf("expected MEMORY.md to contain stored memory, got:\n%s", string(data))
	}
}

func TestStore_WritesToMarkdown_Event(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Store("deployed v2.0", "event", "chat", nil)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Should be in today's daily log
	today := time.Now().Format("20060102")
	monthDir := today[:6]
	dailyFile := filepath.Join(s.workspace, "memory", monthDir, today+".md")
	data, err := os.ReadFile(dailyFile)
	if err != nil {
		t.Fatalf("failed to read daily log: %v", err)
	}
	if !strings.Contains(string(data), "deployed v2.0") {
		t.Errorf("expected daily log to contain stored memory, got:\n%s", string(data))
	}
}

// --- Get ---

func TestGet(t *testing.T) {
	s := newTestStore(t)

	id, _ := s.Store("test memory", "fact", "manual", nil)
	mem, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if mem.Content != "test memory" {
		t.Errorf("expected content 'test memory', got %q", mem.Content)
	}
	if mem.Category != "fact" {
		t.Errorf("expected category 'fact', got %q", mem.Category)
	}
	if mem.Source != "manual" {
		t.Errorf("expected source 'manual', got %q", mem.Source)
	}
	if mem.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Get(999)
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	s := newTestStore(t)

	id, _ := s.Store("to delete", "note", "manual", nil)
	err := s.Delete(id)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = s.Get(id)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestDelete_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Delete(999)
	if err != nil {
		t.Errorf("Delete of nonexistent should not error, got: %v", err)
	}
}

// --- List ---

func TestList(t *testing.T) {
	s := newTestStore(t)

	s.Store("pref 1", "preference", "chat", nil)
	s.Store("fact 1", "fact", "chat", nil)
	s.Store("pref 2", "preference", "chat", nil)

	all, err := s.List("", 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 memories, got %d", len(all))
	}

	prefs, err := s.List("preference", 10)
	if err != nil {
		t.Fatalf("List filtered failed: %v", err)
	}
	if len(prefs) != 2 {
		t.Errorf("expected 2 preferences, got %d", len(prefs))
	}
}

func TestList_Limit(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 10; i++ {
		s.Store("memory", "note", "chat", nil)
	}

	limited, err := s.List("", 3)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(limited) != 3 {
		t.Errorf("expected 3, got %d", len(limited))
	}
}

// --- Search (FTS5) ---

func TestSearch(t *testing.T) {
	s := newTestStore(t)

	s.Store("user prefers dark mode and vim keybindings", "preference", "chat", nil)
	s.Store("user works at Sipeed on MaixCam hardware", "fact", "chat", nil)
	s.Store("deployed version 3.0 to production", "event", "chat", nil)

	results, err := s.Search("vim keybindings", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}
	if !strings.Contains(results[0].Content, "vim") {
		t.Errorf("expected first result to contain 'vim', got %q", results[0].Content)
	}
}

func TestSearch_CategoryFilter(t *testing.T) {
	s := newTestStore(t)

	s.Store("user prefers Go", "preference", "chat", nil)
	s.Store("Go 1.25 was released", "event", "chat", nil)

	results, err := s.Search("Go", 5, "preference")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with category filter, got %d", len(results))
	}
	if results[0].Category != "preference" {
		t.Errorf("expected category 'preference', got %q", results[0].Category)
	}
}

func TestSearch_NoResults(t *testing.T) {
	s := newTestStore(t)

	s.Store("unrelated content", "note", "chat", nil)

	results, err := s.Search("quantum physics", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	s := newTestStore(t)

	s.Store("something", "note", "chat", nil)

	results, err := s.Search("", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	// Empty query should return empty results, not error
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

// --- Stats ---

func TestStats(t *testing.T) {
	s := newTestStore(t)

	s.Store("pref 1", "preference", "chat", nil)
	s.Store("fact 1", "fact", "chat", nil)
	s.Store("pref 2", "preference", "chat", nil)

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("expected total 3, got %d", stats.Total)
	}
	if stats.ByCategory["preference"] != 2 {
		t.Errorf("expected 2 preferences, got %d", stats.ByCategory["preference"])
	}
	if stats.ByCategory["fact"] != 1 {
		t.Errorf("expected 1 fact, got %d", stats.ByCategory["fact"])
	}
}

// --- Reindex ---

func TestReindex(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	memoryDir := filepath.Join(workspace, "memory")
	os.MkdirAll(memoryDir, 0755)

	// Create MEMORY.md with content
	memoryContent := "# Memory\n\n## Preferences\n\n- user likes Go\n- user prefers dark mode\n\n## Facts\n\n- user works at Sipeed\n"
	os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte(memoryContent), 0644)

	// Create a daily log
	today := time.Now().Format("20060102")
	monthDir := today[:6]
	os.MkdirAll(filepath.Join(memoryDir, monthDir), 0755)
	dailyContent := "# 2026-02-12\n\n- deployed v2.0 to production\n- fixed critical bug in auth\n"
	os.WriteFile(filepath.Join(memoryDir, monthDir, today+".md"), []byte(dailyContent), 0644)

	// Create store and reindex
	s, err := NewMemoryStore(filepath.Join(memoryDir, "memory.db"), workspace)
	if err != nil {
		t.Fatalf("NewMemoryStore failed: %v", err)
	}
	defer s.Close()

	err = s.Reindex()
	if err != nil {
		t.Fatalf("Reindex failed: %v", err)
	}

	// Should find content from MEMORY.md
	results, err := s.Search("dark mode", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results from reindexed MEMORY.md")
	}

	// Should find content from daily log
	results, err = s.Search("deployed", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results from reindexed daily log")
	}

	// Stats should show imported entries
	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.Total == 0 {
		t.Error("expected non-zero total after reindex")
	}
}

func TestReindex_Idempotent(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	memoryDir := filepath.Join(workspace, "memory")
	os.MkdirAll(memoryDir, 0755)

	os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte("- user likes Go\n"), 0644)

	s, err := NewMemoryStore(filepath.Join(memoryDir, "memory.db"), workspace)
	if err != nil {
		t.Fatalf("NewMemoryStore failed: %v", err)
	}
	defer s.Close()

	s.Reindex()
	stats1, _ := s.Stats()

	// Reindex again — should not create duplicates
	s.Reindex()
	stats2, _ := s.Stats()

	if stats2.Total != stats1.Total {
		t.Errorf("reindex created duplicates: %d vs %d", stats1.Total, stats2.Total)
	}
}
