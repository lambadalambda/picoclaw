package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LastTarget tracks the last active channel/chat pair that interacted with the agent.
// Cron jobs can use this as a default delivery target.
type LastTarget struct {
	Channel     string `json:"channel"`
	ChatID      string `json:"chat_id"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func (t LastTarget) normalize() LastTarget {
	t.Channel = strings.TrimSpace(t.Channel)
	t.ChatID = strings.TrimSpace(t.ChatID)
	return t
}

func (t LastTarget) valid() bool {
	t = t.normalize()
	return t.Channel != "" && t.ChatID != ""
}

// LoadLastTarget reads a LastTarget file from disk.
// ok=false means no target is available (missing file or invalid content).
func LoadLastTarget(path string) (target LastTarget, ok bool, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return LastTarget{}, false, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LastTarget{}, false, nil
		}
		return LastTarget{}, false, err
	}

	if err := json.Unmarshal(b, &target); err != nil {
		return LastTarget{}, false, err
	}
	target = target.normalize()
	if !target.valid() {
		return LastTarget{}, false, nil
	}
	return target, true, nil
}

// ResolveLastTarget is a convenience wrapper around LoadLastTarget that returns
// the channel/chat_id pair directly.
func ResolveLastTarget(path string) (channel, chatID string, ok bool, err error) {
	lt, ok, err := LoadLastTarget(path)
	if err != nil || !ok {
		return "", "", ok, err
	}
	return lt.Channel, lt.ChatID, true, nil
}

// SaveLastTarget writes a LastTarget file atomically.
func SaveLastTarget(path string, target LastTarget) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("last target path is empty")
	}

	target = target.normalize()
	if target.Channel == "" || target.ChatID == "" {
		return fmt.Errorf("last target must include channel and chat_id")
	}
	target.UpdatedAtMS = time.Now().UnixMilli()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(target, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Atomic-ish write: write a temp file in the same dir, then rename.
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
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LastTargetPath returns the canonical last_target.json path inside a workspace.
func LastTargetPath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, "cron", "last_target.json")
}
