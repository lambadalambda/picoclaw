package utils

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile_ReplacesViaRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")

	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("seed write failed: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	defer f.Close()

	if err := AtomicWriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatalf("AtomicWriteFile failed: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek failed: %v", err)
	}
	oldView, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read old fd failed: %v", err)
	}
	if string(oldView) != "old" {
		t.Fatalf("expected open fd to still see old content, got %q", string(oldView))
	}

	newView, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read new path failed: %v", err)
	}
	if string(newView) != "new" {
		t.Fatalf("expected path to contain new content, got %q", string(newView))
	}
}
