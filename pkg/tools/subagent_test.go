package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type scriptedProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	callIdx   int
}

func (p *scriptedProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.callIdx >= len(p.responses) {
		return &providers.LLMResponse{Content: ""}, nil
	}
	r := p.responses[p.callIdx]
	p.callIdx++
	return r, nil
}

func (p *scriptedProvider) GetDefaultModel() string { return "test-model" }

func TestSubagentManager_SubagentReportPublishesInbound(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	prov := &scriptedProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "tc1",
				Name: "subagent_report",
				Arguments: map[string]interface{}{
					"event":   "progress",
					"content": "step 1",
				},
			}},
		},
		{Content: "done"},
	}}

	sm := NewSubagentManager(prov, "test-model", t.TempDir(), msgBus)
	_, err := sm.Spawn(context.Background(), "do work", "imggen", "telegram", "chat1")
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	gotProgress := false
	gotComplete := false

	for !(gotProgress && gotComplete) {
		msg, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			break
		}

		if msg.Channel != "system" {
			continue
		}
		if msg.ChatID != "telegram:chat1" {
			continue
		}

		event := ""
		if msg.Metadata != nil {
			event = msg.Metadata["subagent_event"]
		}
		switch event {
		case "progress":
			gotProgress = true
			if msg.Content != "step 1" {
				t.Errorf("progress content = %q, want %q", msg.Content, "step 1")
			}
		case "complete":
			gotComplete = true
			if msg.Content == "" {
				t.Error("expected non-empty completion content")
			}
		}
	}

	if !gotProgress {
		t.Fatal("expected progress report inbound message")
	}
	if !gotComplete {
		t.Fatal("expected completion inbound message")
	}
}
