package tools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
)

type mockExecutor struct {
	lastContent string
	lastSession string
	lastChannel string
	lastChatID  string
	response    string
	err         error
	callCount   int
}

func (m *mockExecutor) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	m.callCount++
	m.lastContent = content
	m.lastSession = sessionKey
	m.lastChannel = channel
	m.lastChatID = chatID
	return m.response, m.err
}

func newCronToolWithService(t *testing.T) (*CronTool, *cron.CronService, *mockExecutor, *bus.MessageBus) {
	t.Helper()

	service := cron.NewCronService(filepath.Join(t.TempDir(), "cron.json"), nil)
	executor := &mockExecutor{response: "ok"}
	msgBus := bus.NewMessageBus()
	tool := NewCronTool(service, executor, msgBus)

	return tool, service, executor, msgBus
}

func TestCronTool_AddJobRequiresSessionContext(t *testing.T) {
	tool, _, _, _ := newCronToolWithService(t)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":     "add",
		"message":    "send reminder",
		"at_seconds": float64(60),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no session context") {
		t.Fatalf("expected session context error, got %q", result)
	}
}

func TestCronTool_AddJobWithRegistryContextInjection(t *testing.T) {
	tool, service, _, _ := newCronToolWithService(t)
	registry := NewToolRegistry()
	registry.Register(tool)

	result, err := registry.ExecuteWithContext(context.Background(), "cron", map[string]interface{}{
		"action":     "add",
		"message":    "send reminder",
		"at_seconds": float64(60),
	}, "telegram", "ctx-chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Created job") {
		t.Fatalf("expected created message, got %q", result)
	}

	jobs := service.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Payload.Channel != "telegram" || jobs[0].Payload.To != "ctx-chat" {
		t.Fatalf("job payload channel/chat = %s/%s, want telegram/ctx-chat", jobs[0].Payload.Channel, jobs[0].Payload.To)
	}
}

func TestCronTool_AddAndListJobs(t *testing.T) {
	tool, service, _, _ := newCronToolWithService(t)

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "add",
		"message":       "remind me",
		"at_seconds":    float64(120),
		"deliver":       true,
		"channel":       "telegram",
		"chat_id":       "chat-1",
		"cron_expr":     "ignored",
		"every_seconds": float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Created job") {
		t.Fatalf("expected created message, got %q", result)
	}

	list, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(list, "Scheduled jobs:") {
		t.Fatalf("expected list output, got %q", list)
	}

	jobs := service.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
}

func TestCronTool_AddJobPriorityAtOverEvery(t *testing.T) {
	tool, service, _, _ := newCronToolWithService(t)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "add",
		"message":       "priority test",
		"at_seconds":    float64(30),
		"channel":       "telegram",
		"chat_id":       "chat-2",
		"every_seconds": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	jobs := service.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Schedule.Kind != "at" {
		t.Fatalf("expected at schedule, got %q", jobs[0].Schedule.Kind)
	}
	if jobs[0].Schedule.EveryMS != nil {
		t.Fatal("expected every schedule to be empty when at_seconds is used")
	}
}

func TestCronTool_RemoveAndEnableDisableJobs(t *testing.T) {
	tool, service, _, _ := newCronToolWithService(t)

	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "add",
		"message":       "recurring",
		"every_seconds": float64(120),
		"channel":       "slack",
		"chat_id":       "channel-1",
	}); err != nil {
		t.Fatalf("unexpected error adding job")
	}

	jobs := service.ListJobs(true)
	jobID := jobs[0].ID

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "disable",
		"job_id": jobID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "disabled") {
		t.Fatalf("expected disabled message, got %q", result)
	}

	result, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "enable",
		"job_id": jobID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "enabled") {
		t.Fatalf("expected enabled message, got %q", result)
	}

	result, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "remove",
		"job_id": jobID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Removed job") {
		t.Fatalf("expected removed message, got %q", result)
	}

	jobs = service.ListJobs(true)
	if len(jobs) != 0 {
		t.Fatalf("expected all jobs removed, got %d", len(jobs))
	}
}

func TestCronTool_ExecuteJobDeliverDirect(t *testing.T) {
	tool, _, _, msgBus := newCronToolWithService(t)

	job := &cron.CronJob{
		ID: "direct-1",
		Payload: cron.CronPayload{
			Message: "ping",
			Deliver: true,
			Channel: "telegram",
			To:      "chat-1",
		},
	}

	if got := tool.ExecuteJob(context.Background(), job); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, ok := msgBus.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("expected outbound message from direct delivery")
	}
	if out.Channel != "telegram" || out.ChatID != "chat-1" || out.Content != "ping" {
		t.Fatalf("unexpected outbound message %#v", out)
	}
}

func TestCronTool_ExecuteJobProcessThroughAgent(t *testing.T) {
	tool, _, executor, _ := newCronToolWithService(t)

	job := &cron.CronJob{
		ID: "agent-1",
		Payload: cron.CronPayload{
			Message: "build report",
			Deliver: false,
			Channel: "cli",
			To:      "user-1",
		},
	}

	if got := tool.ExecuteJob(context.Background(), job); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}

	if executor.callCount != 1 {
		t.Fatalf("expected executor to be called once, got %d", executor.callCount)
	}
	if executor.lastContent != "build report" {
		t.Fatalf("expected content %q, got %q", "build report", executor.lastContent)
	}
	if executor.lastSession != "cron-agent-1" {
		t.Fatalf("unexpected session key: %q", executor.lastSession)
	}
	if executor.lastChannel != "cli" {
		t.Fatalf("unexpected channel: %q", executor.lastChannel)
	}
	if executor.lastChatID != "user-1" {
		t.Fatalf("unexpected chat id: %q", executor.lastChatID)
	}
}

func TestCronTool_ExecuteJobProcessThroughAgentError(t *testing.T) {
	tool, _, executor, _ := newCronToolWithService(t)
	executor.err = errors.New("agent failure")

	job := &cron.CronJob{
		ID: "agent-error",
		Payload: cron.CronPayload{
			Message: "failed task",
			Deliver: false,
		},
	}

	got := tool.ExecuteJob(context.Background(), job)
	if !strings.Contains(got, "Error:") {
		t.Fatalf("expected error result, got %q", got)
	}
}

func TestCronTool_ExecuteRejectsUnknownAction(t *testing.T) {
	tool, _, _, _ := newCronToolWithService(t)

	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "invalid",
	}); err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestCronTool_ListNoJobs(t *testing.T) {
	tool, _, _, _ := newCronToolWithService(t)

	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "No scheduled jobs." {
		t.Fatalf("expected no jobs message, got %q", got)
	}
}

func TestCronTool_AddJobMissingMessage(t *testing.T) {
	tool, _, _, _ := newCronToolWithService(t)

	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":     "add",
		"at_seconds": float64(30),
		"channel":    "telegram",
		"chat_id":    "chat-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "message is required") {
		t.Fatalf("expected required message error, got %q", got)
	}
}

func TestCronTool_ExecuteJobDeliverFalseWithoutExecutor_DoesNotPanic(t *testing.T) {
	service := cron.NewCronService(filepath.Join(t.TempDir(), "cron.json"), nil)
	tool := NewCronTool(service, nil, bus.NewMessageBus())

	job := &cron.CronJob{
		ID: "nil-executor",
		Payload: cron.CronPayload{
			Message: "run through agent",
			Deliver: false,
			Channel: "cli",
			To:      "user-1",
		},
	}

	didPanic := false
	func() {
		defer func() {
			if recover() != nil {
				didPanic = true
			}
		}()
		_ = tool.ExecuteJob(context.Background(), job)
	}()

	if didPanic {
		t.Fatal("ExecuteJob should not panic when executor is nil")
	}
}
