package agent

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_UsesCurrentDateHeading(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	prompt := cb.BuildSystemPrompt()

	if !strings.Contains(prompt, "## Current Date") {
		t.Fatalf("BuildSystemPrompt() missing current date heading")
	}
	if strings.Contains(prompt, "## Current Time") {
		t.Fatalf("BuildSystemPrompt() should not include current time heading")
	}
}

func TestBuildMessages_IncludesTelegramDeliveryConstraints(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	msgs := cb.BuildMessages(nil, "", "hi", nil, "telegram", "123")
	if len(msgs) == 0 {
		t.Fatalf("BuildMessages returned no messages")
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %q", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "Telegram messages are limited to 4096 characters") {
		t.Fatalf("expected system prompt to include Telegram delivery constraints")
	}
}
