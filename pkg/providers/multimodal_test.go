package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInlineImagePath_RejectsMissingFile(t *testing.T) {
	err := ValidateInlineImagePath("/tmp/does-not-exist-inline-image.png")
	if err == nil {
		t.Fatal("ValidateInlineImagePath() error = nil, want missing file error")
	}
}

func TestValidateInlineImagePath_AcceptsSmallPNGByExtension(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(path, []byte("not-real-png"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := ValidateInlineImagePath(path); err != nil {
		t.Fatalf("ValidateInlineImagePath() error = %v, want nil", err)
	}
}

func TestSupportsInlineVisionTransport_FallbackProviderMatchesRequestedModel(t *testing.T) {
	fp := newFallbackProvider("glm-5", []fallbackCandidate{
		{model: "glm-5", provider: &HTTPProvider{}},
		{model: "gpt-4o", provider: &HTTPProvider{}},
	})

	if SupportsInlineVisionTransport(fp, "glm-5") {
		t.Fatal("SupportsInlineVisionTransport(glm-5) = true, want false")
	}
	if !SupportsInlineVisionTransport(fp, "gpt-4o") {
		t.Fatal("SupportsInlineVisionTransport(gpt-4o) = false, want true")
	}
}
