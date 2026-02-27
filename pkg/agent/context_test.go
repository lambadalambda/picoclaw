package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
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

func TestBuildMessages_AttachesInlineMediaPartsOnUserMessage(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	mediaPath := "/accounts/1/dc.db-blobs/input.png"
	msgs := cb.BuildMessages(nil, "", "describe this", []string{mediaPath}, "deltachat", "42")

	if len(msgs) == 0 {
		t.Fatalf("BuildMessages returned no messages")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Fatalf("expected last message to be user, got %q", last.Role)
	}
	if len(last.Parts) != 1 {
		t.Fatalf("len(last.Parts) = %d, want 1", len(last.Parts))
	}
	if last.Parts[0].Type != providers.MessagePartTypeImage {
		t.Fatalf("last.Parts[0].Type = %q, want %q", last.Parts[0].Type, providers.MessagePartTypeImage)
	}
	if last.Parts[0].Path != mediaPath {
		t.Fatalf("last.Parts[0].Path = %q, want %q", last.Parts[0].Path, mediaPath)
	}

	encoded, err := json.Marshal(last)
	if err != nil {
		t.Fatalf("json.Marshal(last) error: %v", err)
	}
	payload := string(encoded)
	if strings.Contains(payload, "parts") {
		t.Fatalf("serialized message should omit runtime media parts, got: %s", payload)
	}
	if strings.Contains(payload, mediaPath) {
		t.Fatalf("serialized message should not expose inline media paths, got: %s", payload)
	}
}
