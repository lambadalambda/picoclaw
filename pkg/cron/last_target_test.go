package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadLastTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last_target.json")

	if err := SaveLastTarget(path, LastTarget{Channel: "deltachat", ChatID: "12"}); err != nil {
		t.Fatalf("SaveLastTarget error = %v", err)
	}

	lt, ok, err := LoadLastTarget(path)
	if err != nil {
		t.Fatalf("LoadLastTarget error = %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if lt.Channel != "deltachat" || lt.ChatID != "12" {
		t.Fatalf("got %q/%q, want deltachat/12", lt.Channel, lt.ChatID)
	}
	if lt.UpdatedAtMS <= 0 {
		t.Fatalf("expected UpdatedAtMS to be set, got %d", lt.UpdatedAtMS)
	}
}

func TestLoadLastTarget_InvalidContentReturnsOkFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last_target.json")

	// Write an invalid target (missing chat_id)
	b, _ := json.Marshal(map[string]interface{}{"channel": "deltachat"})
	if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, ok, err := LoadLastTarget(path)
	if err != nil {
		t.Fatalf("LoadLastTarget error = %v", err)
	}
	if ok {
		t.Fatal("expected ok=false")
	}
}
