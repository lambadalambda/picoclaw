package cron

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestService(t *testing.T) *CronService {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron.json")
	return NewCronService(storePath, nil)
}

func TestNewCronService(t *testing.T) {
	cs := newTestService(t)
	if cs == nil {
		t.Fatal("expected non-nil CronService")
	}
	if cs.store == nil {
		t.Fatal("expected non-nil store")
	}
	if len(cs.store.Jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(cs.store.Jobs))
	}
}

func TestAddJob_Every(t *testing.T) {
	cs := newTestService(t)
	every := int64(60000) // 60 seconds

	job, err := cs.AddJob("test-every", CronSchedule{
		Kind:    "every",
		EveryMS: &every,
	}, "do something", false, "", "")

	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.Name != "test-every" {
		t.Errorf("expected name 'test-every', got %q", job.Name)
	}
	if !job.Enabled {
		t.Error("expected job to be enabled")
	}
	if job.State.NextRunAtMS == nil {
		t.Error("expected NextRunAtMS to be set")
	}
	if job.Payload.Message != "do something" {
		t.Errorf("expected message 'do something', got %q", job.Payload.Message)
	}
	if job.DeleteAfterRun {
		t.Error("every-type job should not be deleted after run")
	}
}

func TestAddJob_At(t *testing.T) {
	cs := newTestService(t)
	future := time.Now().Add(1 * time.Hour).UnixMilli()

	job, err := cs.AddJob("test-at", CronSchedule{
		Kind: "at",
		AtMS: &future,
	}, "one-time task", false, "", "")

	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if !job.DeleteAfterRun {
		t.Error("at-type job should be marked for deletion after run")
	}
	if job.State.NextRunAtMS == nil {
		t.Error("expected NextRunAtMS for future at-job")
	}
	if *job.State.NextRunAtMS != future {
		t.Errorf("expected NextRunAtMS=%d, got %d", future, *job.State.NextRunAtMS)
	}
}

func TestAddJob_AtPast(t *testing.T) {
	cs := newTestService(t)
	past := time.Now().Add(-1 * time.Hour).UnixMilli()

	job, err := cs.AddJob("test-at-past", CronSchedule{
		Kind: "at",
		AtMS: &past,
	}, "past task", false, "", "")

	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.State.NextRunAtMS != nil {
		t.Error("at-job in the past should have nil NextRunAtMS")
	}
}

func TestAddJob_Cron(t *testing.T) {
	cs := newTestService(t)

	job, err := cs.AddJob("test-cron", CronSchedule{
		Kind: "cron",
		Expr: "*/5 * * * *", // every 5 minutes
	}, "cron task", false, "", "")

	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.State.NextRunAtMS == nil {
		t.Error("expected NextRunAtMS for cron job")
	}

	// Next run should be in the future
	if *job.State.NextRunAtMS <= time.Now().UnixMilli() {
		t.Error("next run should be in the future")
	}
}

func TestRemoveJob(t *testing.T) {
	cs := newTestService(t)
	every := int64(60000)
	job, _ := cs.AddJob("to-remove", CronSchedule{Kind: "every", EveryMS: &every}, "msg", false, "", "")

	if !cs.RemoveJob(job.ID) {
		t.Error("expected RemoveJob to return true")
	}
	if len(cs.ListJobs(true)) != 0 {
		t.Errorf("expected 0 jobs after removal, got %d", len(cs.ListJobs(true)))
	}
}

func TestRemoveJob_NotFound(t *testing.T) {
	cs := newTestService(t)
	if cs.RemoveJob("nonexistent") {
		t.Error("expected RemoveJob to return false for nonexistent ID")
	}
}

func TestEnableJob(t *testing.T) {
	cs := newTestService(t)
	every := int64(60000)
	job, _ := cs.AddJob("toggle", CronSchedule{Kind: "every", EveryMS: &every}, "msg", false, "", "")

	// Disable
	disabled := cs.EnableJob(job.ID, false)
	if disabled == nil {
		t.Fatal("expected non-nil result from EnableJob")
	}
	if disabled.Enabled {
		t.Error("expected job to be disabled")
	}
	if disabled.State.NextRunAtMS != nil {
		t.Error("disabled job should have nil NextRunAtMS")
	}

	// Re-enable
	enabled := cs.EnableJob(job.ID, true)
	if !enabled.Enabled {
		t.Error("expected job to be enabled")
	}
	if enabled.State.NextRunAtMS == nil {
		t.Error("re-enabled job should have NextRunAtMS")
	}
}

func TestEnableJob_NotFound(t *testing.T) {
	cs := newTestService(t)
	result := cs.EnableJob("nonexistent", true)
	if result != nil {
		t.Error("expected nil for nonexistent job")
	}
}

func TestListJobs(t *testing.T) {
	cs := newTestService(t)
	every := int64(60000)
	job1, _ := cs.AddJob("job1", CronSchedule{Kind: "every", EveryMS: &every}, "msg1", false, "", "")
	cs.AddJob("job2", CronSchedule{Kind: "every", EveryMS: &every}, "msg2", false, "", "")

	// Disable job1
	cs.EnableJob(job1.ID, false)

	all := cs.ListJobs(true)
	if len(all) != 2 {
		t.Errorf("expected 2 jobs total, got %d", len(all))
	}

	enabled := cs.ListJobs(false)
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled job, got %d", len(enabled))
	}
	if enabled[0].Name != "job2" {
		t.Errorf("expected enabled job to be 'job2', got %q", enabled[0].Name)
	}
}

func TestStatus(t *testing.T) {
	cs := newTestService(t)
	every := int64(60000)
	cs.AddJob("job1", CronSchedule{Kind: "every", EveryMS: &every}, "msg", false, "", "")

	status := cs.Status()
	if status["jobs"] != 1 {
		t.Errorf("expected 1 job in status, got %v", status["jobs"])
	}
	if status["enabled"] != false {
		t.Errorf("expected enabled=false before Start, got %v", status["enabled"])
	}
}

func TestComputeNextRun_EveryNil(t *testing.T) {
	cs := newTestService(t)
	result := cs.computeNextRun(&CronSchedule{Kind: "every", EveryMS: nil}, time.Now().UnixMilli())
	if result != nil {
		t.Error("expected nil for every-schedule with nil EveryMS")
	}
}

func TestComputeNextRun_EveryZero(t *testing.T) {
	cs := newTestService(t)
	zero := int64(0)
	result := cs.computeNextRun(&CronSchedule{Kind: "every", EveryMS: &zero}, time.Now().UnixMilli())
	if result != nil {
		t.Error("expected nil for every-schedule with 0 EveryMS")
	}
}

func TestComputeNextRun_CronEmptyExpr(t *testing.T) {
	cs := newTestService(t)
	result := cs.computeNextRun(&CronSchedule{Kind: "cron", Expr: ""}, time.Now().UnixMilli())
	if result != nil {
		t.Error("expected nil for cron-schedule with empty expression")
	}
}

func TestComputeNextRun_CronInvalidExpr(t *testing.T) {
	cs := newTestService(t)
	result := cs.computeNextRun(&CronSchedule{Kind: "cron", Expr: "not a cron expr"}, time.Now().UnixMilli())
	if result != nil {
		t.Error("expected nil for invalid cron expression")
	}
}

func TestComputeNextRun_UnknownKind(t *testing.T) {
	cs := newTestService(t)
	result := cs.computeNextRun(&CronSchedule{Kind: "unknown"}, time.Now().UnixMilli())
	if result != nil {
		t.Error("expected nil for unknown schedule kind")
	}
}

func TestSaveAndLoad(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron.json")
	cs1 := NewCronService(storePath, nil)
	every := int64(60000)
	cs1.AddJob("persistent", CronSchedule{Kind: "every", EveryMS: &every}, "survives restart", false, "", "")

	// Create a new service from the same path
	cs2 := NewCronService(storePath, nil)
	jobs := cs2.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after reload, got %d", len(jobs))
	}
	if jobs[0].Name != "persistent" {
		t.Errorf("expected job name 'persistent', got %q", jobs[0].Name)
	}
	if jobs[0].Payload.Message != "survives restart" {
		t.Errorf("expected message 'survives restart', got %q", jobs[0].Payload.Message)
	}
}

func TestAddJob_WithDelivery(t *testing.T) {
	cs := newTestService(t)
	every := int64(60000)

	job, err := cs.AddJob("deliver-job", CronSchedule{Kind: "every", EveryMS: &every},
		"send this", true, "telegram", "user123")

	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if !job.Payload.Deliver {
		t.Error("expected Deliver=true")
	}
	if job.Payload.Channel != "telegram" {
		t.Errorf("expected channel 'telegram', got %q", job.Payload.Channel)
	}
	if job.Payload.To != "user123" {
		t.Errorf("expected To 'user123', got %q", job.Payload.To)
	}
}

func TestStartStop(t *testing.T) {
	cs := newTestService(t)

	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	status := cs.Status()
	if status["enabled"] != true {
		t.Error("expected enabled=true after Start")
	}

	// Double start should be idempotent
	if err := cs.Start(); err != nil {
		t.Fatalf("second Start should not fail: %v", err)
	}

	cs.Stop()

	// Double stop should be safe
	cs.Stop()
}
