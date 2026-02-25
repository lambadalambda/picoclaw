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
