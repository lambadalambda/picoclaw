package heartbeat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestActivity_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "last_activity.json")

	activity := Activity{
		Channel: "telegram",
		ChatID:  "chat123",
	}

	if err := SaveActivity(path, activity); err != nil {
		t.Fatalf("SaveActivity failed: %v", err)
	}

	loaded, ok, err := LoadActivity(path)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}

	if loaded.Channel != activity.Channel {
		t.Errorf("expected channel=%s, got %s", activity.Channel, loaded.Channel)
	}
	if loaded.ChatID != activity.ChatID {
		t.Errorf("expected chat_id=%s, got %s", activity.ChatID, loaded.ChatID)
	}
	if loaded.UpdatedAtMS == 0 {
		t.Error("expected UpdatedAtMS to be set")
	}
}

func TestLoadActivity_FileNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nonexistent.json")

	_, ok, err := LoadActivity(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for nonexistent file")
	}
}

func TestActivity_Normalize(t *testing.T) {
	activity := Activity{
		Channel: "  telegram  ",
		ChatID:  "  chat123  ",
	}

	normalized := activity.normalize()

	if normalized.Channel != "telegram" {
		t.Errorf("expected channel='telegram', got %q", normalized.Channel)
	}
	if normalized.ChatID != "chat123" {
		t.Errorf("expected chat_id='chat123', got %q", normalized.ChatID)
	}
}

func TestActivity_Valid(t *testing.T) {
	tests := []struct {
		activity Activity
		expected bool
	}{
		{Activity{Channel: "telegram", ChatID: "chat123"}, true},
		{Activity{Channel: "telegram", ChatID: ""}, false},
		{Activity{Channel: "", ChatID: "chat123"}, false},
		{Activity{Channel: "", ChatID: ""}, false},
		{Activity{Channel: "  telegram  ", ChatID: "  chat123  "}, true},
	}

	for _, test := range tests {
		result := test.activity.valid()
		if result != test.expected {
			t.Errorf("activity.valid() = %v, expected %v for %+v", result, test.expected, test.activity)
		}
	}
}

func TestFormatTimeSince(t *testing.T) {
	tests := []struct {
		offset   time.Duration
		expected string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{1 * time.Minute, "1 minute ago"},
		{5 * time.Minute, "5 minutes ago"},
		{1 * time.Hour, "1 hour ago"},
		{3 * time.Hour, "3 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{48 * time.Hour, "2 days ago"},
	}

	for _, test := range tests {
		timestampMS := time.Now().Add(-test.offset).UnixMilli()
		result := FormatTimeSince(timestampMS)

		if result != test.expected {
			t.Errorf("FormatTimeSince(%v) = %q, expected %q", test.offset, result, test.expected)
		}
	}
}

func TestActivityPath(t *testing.T) {
	path := ActivityPath("/home/user/workspace")
	expected := "/home/user/workspace/cron/last_activity.json"

	if path != expected {
		t.Errorf("ActivityPath() = %q, expected %q", path, expected)
	}
}

func TestActivityPath_EmptyWorkspace(t *testing.T) {
	path := ActivityPath("")
	if path != "" {
		t.Errorf("expected empty path for empty workspace, got %q", path)
	}
}

func TestSaveActivity_InvalidActivity(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "last_activity.json")

	tests := []struct {
		activity Activity
	}{
		{Activity{Channel: "", ChatID: ""}},
		{Activity{Channel: "telegram", ChatID: ""}},
		{Activity{Channel: "", ChatID: "chat123"}},
	}

	for _, test := range tests {
		err := SaveActivity(path, test.activity)
		if err == nil {
			t.Errorf("expected error for invalid activity %+v", test.activity)
		}
	}
}
