package agent

import (
	"os"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/heartbeat"
)

func TestRecordLastActiveTarget_LocalNotificationsDoNotRecordActivity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	tests := []struct {
		name         string
		msg          bus.InboundMessage
		shouldRecord bool
		reasonNot    string
	}{
		{
			name: "normal user message records activity",
			msg: bus.InboundMessage{
				Channel:    "telegram",
				ChatID:     "chat123",
				SenderID:   "user456",
				SessionKey: "session1",
			},
			shouldRecord: true,
		},
		{
			name: "local:* sender ID does not record activity",
			msg: bus.InboundMessage{
				Channel:    "telegram",
				ChatID:     "chat123",
				SenderID:   "local:notify789",
				SessionKey: "session2",
			},
			shouldRecord: false,
			reasonNot:    "bot-injected local notification via sender ID",
		},
		{
			name: "LOCAL:* (case insensitive) sender ID does not record activity",
			msg: bus.InboundMessage{
				Channel:    "telegram",
				ChatID:     "chat123",
				SenderID:   "LOCAL:notify789",
				SessionKey: "session3",
			},
			shouldRecord: false,
			reasonNot:    "bot-injected local notification via sender ID (case insensitive)",
		},
		{
			name: "local_notify metadata does not record activity",
			msg: bus.InboundMessage{
				Channel:    "telegram",
				ChatID:     "chat123",
				SenderID:   "user456",
				SessionKey: "session4",
				Metadata:   map[string]string{"local_notify": "1"},
			},
			shouldRecord: false,
			reasonNot:    "bot-injected local notification via metadata",
		},
		{
			name: "system channel does not record activity",
			msg: bus.InboundMessage{
				Channel:    "system",
				ChatID:     "chat123",
				SenderID:   "user456",
				SessionKey: "session5",
			},
			shouldRecord: false,
			reasonNot:    "system channel",
		},
		{
			name: "cli channel does not record activity",
			msg: bus.InboundMessage{
				Channel:    "cli",
				ChatID:     "chat123",
				SenderID:   "user456",
				SessionKey: "session6",
			},
			shouldRecord: false,
			reasonNot:    "cli channel",
		},
		{
			name: "cron session does not record activity",
			msg: bus.InboundMessage{
				Channel:    "telegram",
				ChatID:     "chat123",
				SenderID:   "user456",
				SessionKey: "cron-session-1",
			},
			shouldRecord: false,
			reasonNot:    "cron background session",
		},
		{
			name: "heartbeat session does not record activity",
			msg: bus.InboundMessage{
				Channel:    "telegram",
				ChatID:     "chat123",
				SenderID:   "user456",
				SessionKey: "heartbeat:telegram:chat123",
			},
			shouldRecord: false,
			reasonNot:    "heartbeat background session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activityPath := heartbeat.ActivityPath(al.workspace)
			lastTargetPath := cron.LastTargetPath(al.workspace)

			_ = os.Remove(activityPath)
			_ = os.Remove(lastTargetPath)

			al.recordLastActiveTarget(tt.msg)

			activity, ok, err := heartbeat.LoadActivity(activityPath)
			if err != nil {
				t.Fatalf("LoadActivity failed: %v", err)
			}

			if tt.shouldRecord {
				if !ok {
					t.Fatalf("expected activity to be recorded, but it was not")
				}
				if activity.Channel != tt.msg.Channel {
					t.Errorf("expected channel=%s, got %s", tt.msg.Channel, activity.Channel)
				}
				if activity.ChatID != tt.msg.ChatID {
					t.Errorf("expected chat_id=%s, got %s", tt.msg.ChatID, activity.ChatID)
				}
			} else {
				if ok {
					t.Errorf("expected activity NOT to be recorded due to %s, but found: channel=%s, chat_id=%s",
						tt.reasonNot, activity.Channel, activity.ChatID)
				}
			}
		})
	}
}

func TestRecordLastActiveTarget_LastTargetSeparateFromActivity(t *testing.T) {
	cfg := config.DefaultConfig()
	workspace := t.TempDir()
	cfg.Agents.Defaults.Workspace = workspace

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	msg := bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat456",
		SenderID:   "user789",
		SessionKey: "normal-session",
	}

	al.recordLastActiveTarget(msg)

	lastTargetPath := cron.LastTargetPath(al.workspace)
	lastTarget, ok, err := cron.LoadLastTarget(lastTargetPath)
	if err != nil {
		t.Fatalf("LoadLastTarget failed: %v", err)
	}
	if !ok {
		t.Fatal("expected last target to be recorded")
	}
	if lastTarget.Channel != msg.Channel {
		t.Errorf("expected last target channel=%s, got %s", msg.Channel, lastTarget.Channel)
	}
	if lastTarget.ChatID != msg.ChatID {
		t.Errorf("expected last target chat_id=%s, got %s", msg.ChatID, lastTarget.ChatID)
	}

	activityPath := heartbeat.ActivityPath(al.workspace)
	activity, ok, err := heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok {
		t.Fatal("expected activity to be recorded")
	}
	if activity.Channel != msg.Channel {
		t.Errorf("expected activity channel=%s, got %s", msg.Channel, activity.Channel)
	}
	if activity.ChatID != msg.ChatID {
		t.Errorf("expected activity chat_id=%s, got %s", msg.ChatID, activity.ChatID)
	}
}

func TestRecordLastActiveTarget_LastTargetFiltersBackgroundButActivityFiltersBoth(t *testing.T) {
	cfg := config.DefaultConfig()
	workspace := t.TempDir()
	cfg.Agents.Defaults.Workspace = workspace

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	msg := bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat999",
		SenderID:   "local:notify",
		SessionKey: "normal-session",
	}

	al.recordLastActiveTarget(msg)

	lastTargetPath := cron.LastTargetPath(al.workspace)
	lastTarget, ok, err := cron.LoadLastTarget(lastTargetPath)
	if err != nil {
		t.Fatalf("LoadLastTarget failed: %v", err)
	}
	if ok {
		t.Errorf("expected last target NOT to be recorded for local notification, but found: %+v", lastTarget)
	}

	activityPath := heartbeat.ActivityPath(al.workspace)
	activity, ok, err := heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if ok {
		t.Errorf("expected activity NOT to be recorded for local notification, but found: %+v", activity)
	}
}

func TestRecordLastActiveTarget_EmptyChannelOrChatID(t *testing.T) {
	cfg := config.DefaultConfig()
	workspace := t.TempDir()
	cfg.Agents.Defaults.Workspace = workspace

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	activityPath := heartbeat.ActivityPath(al.workspace)

	tests := []struct {
		name    string
		channel string
		chatID  string
	}{
		{"empty channel", "", "chat123"},
		{"empty chat_id", "telegram", ""},
		{"both empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := bus.InboundMessage{
				Channel:    tt.channel,
				ChatID:     tt.chatID,
				SenderID:   "user123",
				SessionKey: "session1",
			}

			al.recordLastActiveTarget(msg)

			_, ok, err := heartbeat.LoadActivity(activityPath)
			if err != nil {
				t.Fatalf("LoadActivity failed: %v", err)
			}
			if ok {
				t.Errorf("expected activity NOT to be recorded for %s", tt.name)
			}
		})
	}
}

func TestRecordLastActiveTarget_UpdatesActivityTimestamp(t *testing.T) {
	cfg := config.DefaultConfig()
	workspace := t.TempDir()
	cfg.Agents.Defaults.Workspace = workspace

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	activityPath := heartbeat.ActivityPath(al.workspace)

	msg1 := bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat123",
		SenderID:   "user456",
		SessionKey: "session1",
	}
	al.recordLastActiveTarget(msg1)

	activity1, ok, err := heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok {
		t.Fatal("expected activity to be recorded")
	}

	time.Sleep(2 * time.Millisecond)

	msg2 := bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat789",
		SenderID:   "user456",
		SessionKey: "session2",
	}
	al.recordLastActiveTarget(msg2)

	activity2, ok, err := heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok {
		t.Fatal("expected activity to be recorded")
	}

	if activity2.UpdatedAtMS <= activity1.UpdatedAtMS {
		t.Errorf("expected UpdatedAtMS to increase, got %d -> %d", activity1.UpdatedAtMS, activity2.UpdatedAtMS)
	}
}

func TestRecordLastActiveTarget_MultipleFilters(t *testing.T) {
	cfg := config.DefaultConfig()
	workspace := t.TempDir()
	cfg.Agents.Defaults.Workspace = workspace

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	activityPath := heartbeat.ActivityPath(al.workspace)

	al.recordLastActiveTarget(bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat1",
		SenderID:   "user1",
		SessionKey: "session1",
	})

	activity, ok, err := heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok || activity.ChatID != "chat1" {
		t.Fatal("expected first message to record activity")
	}

	al.recordLastActiveTarget(bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat2",
		SenderID:   "local:notify",
		SessionKey: "session2",
	})

	activity, ok, err = heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok {
		t.Fatal("expected activity file to still exist")
	}
	if activity.ChatID != "chat1" {
		t.Errorf("expected activity to remain at chat1 (local notification should not update), got %s", activity.ChatID)
	}
}

func TestRecordLastActiveTarget_LastTargetUpdatesIndependently(t *testing.T) {
	cfg := config.DefaultConfig()
	workspace := t.TempDir()
	cfg.Agents.Defaults.Workspace = workspace

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.bus.Close()

	lastTargetPath := cron.LastTargetPath(al.workspace)
	activityPath := heartbeat.ActivityPath(al.workspace)

	al.recordLastActiveTarget(bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat1",
		SenderID:   "user1",
		SessionKey: "session1",
	})

	lastTarget, ok, err := cron.LoadLastTarget(lastTargetPath)
	if err != nil {
		t.Fatalf("LoadLastTarget failed: %v", err)
	}
	if !ok || lastTarget.ChatID != "chat1" {
		t.Fatal("expected first message to record last target")
	}

	activity, ok, err := heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok || activity.ChatID != "chat1" {
		t.Fatal("expected first message to record activity")
	}

	al.recordLastActiveTarget(bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat2",
		SenderID:   "local:notify",
		SessionKey: "session2",
	})

	lastTarget, ok, err = cron.LoadLastTarget(lastTargetPath)
	if err != nil {
		t.Fatalf("LoadLastTarget failed: %v", err)
	}
	if !ok {
		t.Fatal("expected last target file to still exist")
	}
	if lastTarget.ChatID != "chat1" {
		t.Errorf("expected last target to remain at chat1 (local notification should not update), got %s", lastTarget.ChatID)
	}

	activity, ok, err = heartbeat.LoadActivity(activityPath)
	if err != nil {
		t.Fatalf("LoadActivity failed: %v", err)
	}
	if !ok {
		t.Fatal("expected activity file to still exist")
	}
	if activity.ChatID != "chat1" {
		t.Errorf("expected activity to remain at chat1 (local notification should not update), got %s", activity.ChatID)
	}
}
