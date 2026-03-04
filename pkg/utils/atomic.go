package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AtomicWriteFile writes data to a temp file in the same directory, then renames it
// over the destination path.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is empty")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if perm != 0 {
		if err := os.Chmod(tmpName, perm); err != nil {
			return err
		}
	}

	return os.Rename(tmpName, path)
}
