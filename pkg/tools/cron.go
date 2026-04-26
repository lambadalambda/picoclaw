package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// JobExecutor is the interface for executing cron jobs through the agent
type JobExecutor interface {
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
}

// CronTool provides scheduling capabilities for the agent
type CronTool struct {
	cronService *cron.CronService
	executor    JobExecutor
	msgBus      *bus.MessageBus
	// lastTargetPath points to a json file written by the agent runtime.
	// When a cron job has no explicit channel/to, this is used to resolve
	// the most recently active chat for delivery.
	lastTargetPath string
}

// NewCronTool creates a new CronTool
func NewCronTool(cronService *cron.CronService, executor JobExecutor, msgBus *bus.MessageBus, lastTargetPath string) *CronTool {
	return &CronTool{
		cronService:    cronService,
		executor:       executor,
		msgBus:         msgBus,
		lastTargetPath: strings.TrimSpace(lastTargetPath),
	}
}

// Name returns the tool name
func (t *CronTool) Name() string {
	return "cron"
}

// Description returns the tool description
func (t *CronTool) Description() string {
	return "Schedule reminders and tasks. IMPORTANT: When user asks to be reminded or scheduled, you MUST call this tool. Use 'at_seconds' for one-time reminders (e.g., 'remind me in 10 minutes' → at_seconds=600). Use 'every_seconds' ONLY for recurring tasks (e.g., 'every 2 hours' → every_seconds=7200). Use 'cron_expr' for complex recurring schedules (e.g., '0 9 * * *' for daily at 9am). Reminder delivery is processed by the agent, and user-visible output must be sent via the message tool. By default, cron jobs target the most recently active chat (last channel/chat used). To pin delivery to a specific channel/chat, set both 'channel' and 'chat_id'."
}

// Parameters returns the tool parameters schema
func (t *CronTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "list", "remove", "enable", "disable"},
				"description": "Action to perform. Use 'add' when user wants to schedule a reminder or task.",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "The reminder/task message to display when triggered (required for add)",
			},
			"at_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "One-time reminder: seconds from now when to trigger (e.g., 600 for 10 minutes later). Use this for one-time reminders like 'remind me in 10 minutes'.",
			},
			"every_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "Recurring interval in seconds (e.g., 3600 for every hour). Use this ONLY for recurring tasks like 'every 2 hours' or 'daily reminder'.",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "Cron expression for complex recurring schedules (e.g., '0 9 * * *' for daily at 9am). Use this for complex recurring schedules.",
			},
			"job_id": map[string]interface{}{
				"type":        "string",
				"description": "Job ID (for remove/enable/disable)",
			},
			"deliver": map[string]interface{}{
				"type":        "boolean",
				"description": "Deprecated compatibility field. Must be false. Delivery is always processed by the agent and sent via message tool.",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel override for the job",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID override for the job",
			},
		},
		"required": []string{"action"},
	}
}

// Execute runs the tool with given arguments
func (t *CronTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, ok := args["action"].(string)
	if !ok {
		return "", fmt.Errorf("action is required")
	}

	switch action {
	case "add":
		return t.addJob(args)
	case "list":
		return t.listJobs()
	case "remove":
		return t.removeJob(args)
	case "enable":
		return t.enableJob(args, true)
	case "disable":
		return t.enableJob(args, false)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (t *CronTool) addJob(args map[string]interface{}) (string, error) {
	// If channel/chat_id are provided, the job is pinned.
	_, channelSet := args["channel"]
	_, chatIDSet := args["chat_id"]
	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channelSet || chatIDSet {
		if channel == "" || chatID == "" {
			return "Error: channel and chat_id must both be set when pinning cron delivery", nil
		}
	} else {
		// Default behavior: deliver to most recently active chat at execution time.
		channel = ""
		chatID = ""
	}

	message, ok := args["message"].(string)
	if !ok || message == "" {
		return "Error: message is required for add", nil
	}

	var schedule cron.CronSchedule

	// Check for at_seconds (one-time), every_seconds (recurring), or cron_expr
	atSeconds, hasAt := args["at_seconds"].(float64)
	everySeconds, hasEvery := args["every_seconds"].(float64)
	cronExpr, hasCron := args["cron_expr"].(string)

	// Priority: at_seconds > every_seconds > cron_expr
	if hasAt {
		atMS := time.Now().UnixMilli() + int64(atSeconds)*1000
		schedule = cron.CronSchedule{
			Kind: "at",
			AtMS: &atMS,
		}
	} else if hasEvery {
		everyMS := int64(everySeconds) * 1000
		schedule = cron.CronSchedule{
			Kind:    "every",
			EveryMS: &everyMS,
		}
	} else if hasCron {
		schedule = cron.CronSchedule{
			Kind: "cron",
			Expr: cronExpr,
		}
	} else {
		return "Error: one of at_seconds, every_seconds, or cron_expr is required", nil
	}

	// Read deliver parameter, default to false. Direct bus delivery is disabled;
	// all user-visible sends must go through the message tool.
	deliver := false
	if d, ok := args["deliver"].(bool); ok {
		deliver = d
	}
	if deliver {
		return "Error: deliver=true is no longer supported. Schedule agent-processed jobs and use the message tool for user-visible delivery.", nil
	}

	// Truncate message for job name (max 30 chars)
	messagePreview := utils.Truncate(message, 30)

	job, err := t.cronService.AddJob(
		messagePreview,
		schedule,
		message,
		deliver,
		channel,
		chatID,
	)
	if err != nil {
		return fmt.Sprintf("Error adding job: %v", err), nil
	}

	return fmt.Sprintf("Created job '%s' (id: %s)", job.Name, job.ID), nil
}

func (t *CronTool) resolveLastTarget() (string, string) {
	if t.lastTargetPath == "" {
		return "", ""
	}
	ch, chatID, ok, err := cron.ResolveLastTarget(t.lastTargetPath)
	if err != nil || !ok {
		return "", ""
	}
	return ch, chatID
}

func (t *CronTool) listJobs() (string, error) {
	jobs := t.cronService.ListJobs(false)

	if len(jobs) == 0 {
		return "No scheduled jobs.", nil
	}

	result := "Scheduled jobs:\n"
	for _, j := range jobs {
		var scheduleInfo string
		if j.Schedule.Kind == "every" && j.Schedule.EveryMS != nil {
			scheduleInfo = fmt.Sprintf("every %ds", *j.Schedule.EveryMS/1000)
		} else if j.Schedule.Kind == "cron" {
			scheduleInfo = j.Schedule.Expr
		} else if j.Schedule.Kind == "at" {
			scheduleInfo = "one-time"
		} else {
			scheduleInfo = "unknown"
		}
		result += fmt.Sprintf("- %s (id: %s, %s)\n", j.Name, j.ID, scheduleInfo)
	}

	return result, nil
}

func (t *CronTool) removeJob(args map[string]interface{}) (string, error) {
	jobID, ok := args["job_id"].(string)
	if !ok || jobID == "" {
		return "Error: job_id is required for remove", nil
	}

	if t.cronService.RemoveJob(jobID) {
		return fmt.Sprintf("Removed job %s", jobID), nil
	}
	return fmt.Sprintf("Job %s not found", jobID), nil
}

func (t *CronTool) enableJob(args map[string]interface{}, enable bool) (string, error) {
	jobID, ok := args["job_id"].(string)
	if !ok || jobID == "" {
		return "Error: job_id is required for enable/disable", nil
	}

	job := t.cronService.EnableJob(jobID, enable)
	if job == nil {
		return fmt.Sprintf("Job %s not found", jobID), nil
	}

	status := "enabled"
	if !enable {
		status = "disabled"
	}
	return fmt.Sprintf("Job '%s' %s", job.Name, status), nil
}

// ExecuteJob executes a cron job through the agent
func (t *CronTool) ExecuteJob(ctx context.Context, job *cron.CronJob) string {
	// Get channel/chatID from job payload
	channel := strings.TrimSpace(job.Payload.Channel)
	chatID := strings.TrimSpace(job.Payload.To)
	if channel == "" || chatID == "" {
		lastChannel, lastChatID := t.resolveLastTarget()
		if lastChannel != "" && lastChatID != "" {
			channel = lastChannel
			chatID = lastChatID
		}
	}

	// Default values if still not set
	if channel == "" {
		channel = "cli"
	}
	if chatID == "" {
		chatID = "direct"
	}

	// Process all jobs through the agent so any user-visible output is sent via
	// the message tool only.
	if t.executor == nil {
		return "Error: executor not configured"
	}

	sessionKey := fmt.Sprintf("cron-%s", job.ID)

	// Call agent with the job's message
	response, err := t.executor.ProcessDirectWithChannel(
		ctx,
		job.Payload.Message,
		sessionKey,
		channel,
		chatID,
	)

	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	_ = response
	return "ok"
}
