package heartbeat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatService_Start_BeginsTicker(t *testing.T) {
	beats := make(chan struct{}, 2)
	hs := NewHeartbeatService(t.TempDir(), func(prompt string) (string, error) {
		select {
		case beats <- struct{}{}:
		default:
		}
		return "ok", nil
	}, 1, true)

	if err := hs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	select {
	case <-beats:
		// expected
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("expected heartbeat callback after Start")
	}
}

func TestHeartbeatService_StartAfterStop_RecreatesStopChannel(t *testing.T) {
	beats := make(chan struct{}, 2)
	hs := NewHeartbeatService(t.TempDir(), func(prompt string) (string, error) {
		select {
		case beats <- struct{}{}:
		default:
		}
		return "ok", nil
	}, 1, true)

	hs.Stop()

	if err := hs.Start(); err != nil {
		t.Fatalf("Start failed after Stop: %v", err)
	}

	select {
	case <-beats:
		// expected
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("expected heartbeat callback after Stop+Start")
	}
}

func TestHeartbeatService_StartWithNonPositiveInterval_ReturnsError(t *testing.T) {
	hs := NewHeartbeatService(t.TempDir(), func(prompt string) (string, error) {
		return "ok", nil
	}, 0, true)

	if err := hs.Start(); err == nil {
		t.Fatal("expected error for non-positive heartbeat interval")
	}
}

func TestHeartbeatService_BuildPromptReadsWorkspaceHeartbeatFile(t *testing.T) {
	workspace := t.TempDir()
	content := "HEARTBEAT RULE: daytime proactive check"
	if err := os.WriteFile(filepath.Join(workspace, "HEARTBEAT.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write HEARTBEAT.md: %v", err)
	}

	hs := NewHeartbeatService(workspace, nil, 60, true)
	prompt := hs.buildPrompt()

	if !strings.Contains(prompt, content) {
		t.Fatalf("expected prompt to include workspace HEARTBEAT.md content, got: %q", prompt)
	}
}

func TestHeartbeatService_BuildPromptDoesNotReadMemoryHeartbeatFile(t *testing.T) {
	workspace := t.TempDir()
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}

	legacyContent := "LEGACY MEMORY HEARTBEAT CONTENT"
	if err := os.WriteFile(filepath.Join(memoryDir, "HEARTBEAT.md"), []byte(legacyContent), 0644); err != nil {
		t.Fatalf("write memory/HEARTBEAT.md: %v", err)
	}

	hs := NewHeartbeatService(workspace, nil, 60, true)
	prompt := hs.buildPrompt()

	if strings.Contains(prompt, legacyContent) {
		t.Fatalf("did not expect prompt to include memory/HEARTBEAT.md content, got: %q", prompt)
	}
}
