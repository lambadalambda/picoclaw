package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryStore_WriteLongTerm_CapsFileSize(t *testing.T) {
	ms := NewMemoryStore(t.TempDir())

	content := "# Memory\n\n" + strings.Repeat("x", memoryContextFileMaxChars+5000)
	if err := ms.WriteLongTerm(content); err != nil {
		t.Fatalf("WriteLongTerm() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(ms.memoryDir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("os.ReadFile(MEMORY.md) error: %v", err)
	}
	if len(data) > memoryContextFileMaxChars {
		t.Fatalf("MEMORY.md size = %d, want <= %d", len(data), memoryContextFileMaxChars)
	}
	if !strings.Contains(string(data), memoryContextTrimNotice) {
		t.Fatalf("expected MEMORY.md to include trim notice when capped")
	}
}

func TestMemoryStore_AppendToday_CapsFileSize(t *testing.T) {
	ms := NewMemoryStore(t.TempDir())

	chunk := strings.Repeat("y", 10000)
	for i := 0; i < 8; i++ {
		if err := ms.AppendToday(chunk); err != nil {
			t.Fatalf("AppendToday() error at %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(ms.getTodayFile())
	if err != nil {
		t.Fatalf("os.ReadFile(today) error: %v", err)
	}
	if len(data) > memoryContextFileMaxChars {
		t.Fatalf("today file size = %d, want <= %d", len(data), memoryContextFileMaxChars)
	}
	if !strings.Contains(string(data), memoryContextTrimNotice) {
		t.Fatalf("expected today file to include trim notice when capped")
	}
}

func TestMemoryStore_GetMemoryContext_CapsOversizedExistingFiles(t *testing.T) {
	ms := NewMemoryStore(t.TempDir())

	if err := os.WriteFile(filepath.Join(ms.memoryDir, "MEMORY.md"), []byte("# Memory\n\n"+strings.Repeat("a", memoryContextFileMaxChars+7000)), 0644); err != nil {
		t.Fatalf("os.WriteFile(MEMORY.md) error: %v", err)
	}

	today := time.Now()
	for i := 0; i < 3; i++ {
		date := today.AddDate(0, 0, -i)
		dateStr := date.Format("20060102")
		monthDir := filepath.Join(ms.memoryDir, dateStr[:6])
		if err := os.MkdirAll(monthDir, 0755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error: %v", monthDir, err)
		}
		path := filepath.Join(monthDir, dateStr+".md")
		content := "# " + date.Format("2006-01-02") + "\n\n" + strings.Repeat("b", memoryContextFileMaxChars+3000)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("os.WriteFile(%q) error: %v", path, err)
		}
	}

	ctx := ms.GetMemoryContext()
	if len(ctx) > (4*memoryContextFileMaxChars)+2000 {
		t.Fatalf("context size = %d, unexpectedly large", len(ctx))
	}
	if !strings.Contains(ctx, memoryContextTrimNotice) {
		t.Fatalf("expected capped memory context to include trim notice")
	}
}
