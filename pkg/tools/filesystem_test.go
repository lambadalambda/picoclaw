package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileTool_ExecuteReadsFile(t *testing.T) {
	tool := &ReadFileTool{}
	content := "hello from test file"

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := ensureWriteFile(path, content); err != nil {
		t.Fatalf("failed to setup test file: %v", err)
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": path,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != content {
		t.Fatalf("expected %q, got %q", content, result)
	}
}

func TestReadFileTool_ExecuteMissingPath(t *testing.T) {
	tool := &ReadFileTool{}

	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when path is missing")
	}
}

func TestReadFileTool_ExecuteWithLineRange(t *testing.T) {
	tool := &ReadFileTool{}
	content := "line 1\nline 2\nline 3\nline 4\n"

	path := filepath.Join(t.TempDir(), "book.txt")
	if err := ensureWriteFile(path, content); err != nil {
		t.Fatalf("failed to setup test file: %v", err)
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 2,
		"max_lines":  2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "line 2\nline 3\n" {
		t.Fatalf("expected line slice, got %q", result)
	}
}

func TestReadFileTool_ExecuteWithLineRangeUntilEnd(t *testing.T) {
	tool := &ReadFileTool{}
	content := "line 1\nline 2\nline 3\nline 4\n"

	path := filepath.Join(t.TempDir(), "book.txt")
	if err := ensureWriteFile(path, content); err != nil {
		t.Fatalf("failed to setup test file: %v", err)
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "line 3\nline 4\n" {
		t.Fatalf("expected tail slice, got %q", result)
	}
}

func TestReadFileTool_ExecuteLineRangeValidation(t *testing.T) {
	tool := &ReadFileTool{}
	content := "line 1\nline 2\n"

	path := filepath.Join(t.TempDir(), "book.txt")
	if err := ensureWriteFile(path, content); err != nil {
		t.Fatalf("failed to setup test file: %v", err)
	}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 0,
	})
	if err == nil || !strings.Contains(err.Error(), "start_line") {
		t.Fatalf("expected start_line validation error, got %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 1,
		"max_lines":  -1,
	})
	if err == nil || !strings.Contains(err.Error(), "max_lines") {
		t.Fatalf("expected max_lines validation error, got %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 99,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds total lines") {
		t.Fatalf("expected out-of-range error, got %v", err)
	}
}

func TestWriteFileTool_ExecuteCreatesDirectories(t *testing.T) {
	tool := &WriteFileTool{}

	file := filepath.Join(t.TempDir(), "nested", "dir", "output.txt")
	content := "generated output"

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":    file,
		"content": content,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "File written successfully" {
		t.Fatalf("unexpected result: %q", result)
	}

	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": file,
	}); err == nil {
		t.Fatalf("expected error when writing args are incomplete, got nil")
	}

	readTool := &ReadFileTool{}
	raw, err := readTool.Execute(context.Background(), map[string]interface{}{
		"path": file,
	})
	if err != nil {
		t.Fatalf("readback failed: %v", err)
	}
	if raw != content {
		t.Fatalf("expected %q, got %q", content, raw)
	}
}

func TestWriteFileTool_ExecuteRequiresContent(t *testing.T) {
	tool := &WriteFileTool{}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": filepath.Join(t.TempDir(), "out.txt"),
	})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

func TestListDirTool_Execute(t *testing.T) {
	root := t.TempDir()
	if _, err := (&WriteFileTool{}).Execute(context.Background(), map[string]interface{}{
		"path":    filepath.Join(root, "file.txt"),
		"content": "data",
	}); err != nil {
		t.Fatalf("failed to prepare file: %v", err)
	}
	if _, err := (&WriteFileTool{}).Execute(context.Background(), map[string]interface{}{
		"path":    filepath.Join(root, "nested", "more.txt"),
		"content": "deeper",
	}); err != nil {
		t.Fatalf("failed to prepare nested file: %v", err)
	}

	tool := &ListDirTool{}
	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got, "FILE: file.txt") {
		t.Fatalf("expected root file listing, got %q", got)
	}
	if !strings.Contains(got, "DIR:  nested") {
		t.Fatalf("expected nested directory listing, got %q", got)
	}
}

// ensureWriteFile mirrors os.WriteFile usage to keep test setup concise and explicit.
func ensureWriteFile(path, content string) error {
	if _, err := (&WriteFileTool{}).Execute(context.Background(), map[string]interface{}{
		"path":    path,
		"content": content,
	}); err != nil {
		return err
	}

	return nil
}
