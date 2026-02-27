package utils

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadFile_DoesNotDuplicateExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-image-bytes"))
	}))
	defer srv.Close()

	localPath := DownloadFile(srv.URL+"/photos/test.jpg", "photos/test.jpg", DownloadOptions{LoggerPrefix: "test"})
	if localPath == "" {
		t.Fatalf("expected non-empty localPath")
	}
	defer os.Remove(localPath)

	if !strings.Contains(localPath, "picoclaw_media") {
		t.Fatalf("expected path to include picoclaw_media, got %q", localPath)
	}
	if !strings.HasSuffix(localPath, "_test.jpg") {
		t.Fatalf("expected path to end with _test.jpg, got %q", localPath)
	}

	info, err := os.Stat(localPath)
	if err != nil {
		t.Fatalf("expected downloaded file to exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected downloaded file to have non-zero size")
	}
}

func TestDownloadFile_PreservesComplexExtensions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake"))
	}))
	defer srv.Close()

	localPath := DownloadFile(srv.URL+"/archive.tar.gz", "archive.tar.gz", DownloadOptions{LoggerPrefix: "test"})
	if localPath == "" {
		t.Fatalf("expected non-empty localPath")
	}
	defer os.Remove(localPath)

	if !strings.HasSuffix(localPath, "_archive.tar.gz") {
		t.Fatalf("expected path to end with _archive.tar.gz, got %q", localPath)
	}

	// Ensure filename is clean (no directory traversal)
	if strings.Contains(filepath.Base(localPath), "..") {
		t.Fatalf("expected sanitized filename, got %q", filepath.Base(localPath))
	}
}
