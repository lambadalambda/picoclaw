package cron

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCronService_StartAfterStop_RestartsLoop(t *testing.T) {
	triggered := make(chan struct{}, 4)

	cs := NewCronService(filepath.Join(t.TempDir(), "cron.json"), func(job *CronJob) (string, error) {
		select {
		case triggered <- struct{}{}:
		default:
		}
		return "ok", nil
	})

	every := int64(1000)
	if _, err := cs.AddJob("tick", CronSchedule{Kind: "every", EveryMS: &every}, "run", false, "", ""); err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	if err := cs.Start(); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}

	select {
	case <-triggered:
		// first run happened
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("expected first run before stopping service")
	}

	cs.Stop()

	// Drain any stale signals so the second start must produce a fresh run.
	drainDone := false
	for !drainDone {
		select {
		case <-triggered:
		default:
			drainDone = true
		}
	}

	if err := cs.Start(); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}

	select {
	case <-triggered:
		// expected after restart
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("expected job to run after restart")
	}
}
