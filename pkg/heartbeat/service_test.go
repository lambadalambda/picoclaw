package heartbeat

import (
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
