package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type stubVisionAnalyzer struct {
	result string
	err    error
	calls  int
}

func (s *stubVisionAnalyzer) AnalyzeImages(_ context.Context, _ string, _ []string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.result, nil
}

func TestBuildUserMessageWithMediaContext_UsesFallbackForNonVisionModel(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	analyzer := &stubVisionAnalyzer{result: "Detected a terminal error screenshot."}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: false},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "What is this?", []string{imagePath}, "trace-test")

	if analyzer.calls != 1 {
		t.Fatalf("analyzer call count = %d, want 1", analyzer.calls)
	}
	if len(inline) != 0 {
		t.Fatalf("inline media = %v, want []", inline)
	}
	if !strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("message should include fallback section, got: %q", got)
	}
	if !strings.Contains(got, analyzer.result) {
		t.Fatalf("message should include analyzer output, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_NoAnalyzerAddsNotice(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.jpg")
	if err := os.WriteFile(imagePath, []byte("jpg"), 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	al := &AgentLoop{modelCapabilities: providers.ModelCapabilities{SupportsVision: false}}
	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "Please inspect", []string{imagePath}, "trace-test")

	if len(inline) != 0 {
		t.Fatalf("inline media = %v, want []", inline)
	}
	if !strings.Contains(got, "image analysis unavailable") {
		t.Fatalf("message should include unavailable notice, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_VisionModelWithoutInlineTransportUsesFallback(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.webp")
	if err := os.WriteFile(imagePath, []byte("webp"), 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	analyzer := &stubVisionAnalyzer{result: "Detected a settings dialog."}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: true},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "Describe", []string{imagePath}, "trace-test")

	if analyzer.calls != 1 {
		t.Fatalf("analyzer call count = %d, want 1", analyzer.calls)
	}
	if len(inline) != 0 {
		t.Fatalf("inline media = %v, want []", inline)
	}
	if !strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("message should include fallback section, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_VisionModelWithInlineTransportSkipsFallback(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.webp")
	if err := os.WriteFile(imagePath, []byte("webp"), 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	analyzer := &stubVisionAnalyzer{result: "should not be called"}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: true, SupportsInlineVision: true},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "Describe", []string{imagePath}, "trace-test")

	if analyzer.calls != 0 {
		t.Fatalf("analyzer call count = %d, want 0", analyzer.calls)
	}
	if len(inline) != 1 || inline[0] != imagePath {
		t.Fatalf("inline media = %v, want [%s]", inline, imagePath)
	}
	if strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("inline-vision model should skip fallback section, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_InlineModelFallsBackForUnsupportedImageType(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.tiff")
	if err := os.WriteFile(imagePath, []byte("tiff"), 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	analyzer := &stubVisionAnalyzer{result: "Detected a printed stack trace."}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: true, SupportsInlineVision: true},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "Describe", []string{imagePath}, "trace-test")

	if analyzer.calls != 1 {
		t.Fatalf("analyzer call count = %d, want 1", analyzer.calls)
	}
	if len(inline) != 0 {
		t.Fatalf("inline media = %v, want []", inline)
	}
	if !strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("message should include fallback section, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_InlineModelFallsBackWhenInlineValidationFails(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "missing.png")

	analyzer := &stubVisionAnalyzer{result: "Detected an exception stack trace."}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: true, SupportsInlineVision: true},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "Describe", []string{imagePath}, "trace-test")

	if analyzer.calls != 1 {
		t.Fatalf("analyzer call count = %d, want 1", analyzer.calls)
	}
	if len(inline) != 0 {
		t.Fatalf("inline media = %v, want []", inline)
	}
	if !strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("message should include fallback section, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_InlineModelInlinesExtensionlessPNG(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input")
	// PNG signature is enough for http.DetectContentType.
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(imagePath, pngHeader, 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	analyzer := &stubVisionAnalyzer{result: "should not be called"}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: true, SupportsInlineVision: true},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "Describe", []string{imagePath}, "trace-test")

	if analyzer.calls != 0 {
		t.Fatalf("analyzer call count = %d, want 0", analyzer.calls)
	}
	if len(inline) != 1 || inline[0] != imagePath {
		t.Fatalf("inline media = %v, want [%s]", inline, imagePath)
	}
	if strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("inline-vision model should skip fallback section, got: %q", got)
	}
}

func TestBuildUserMessageWithMediaContext_NonVisionModelFallsBackForExtensionlessImage(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input")
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(imagePath, pngHeader, 0644); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	analyzer := &stubVisionAnalyzer{result: "Detected a screenshot."}
	al := &AgentLoop{
		modelCapabilities: providers.ModelCapabilities{SupportsVision: false},
		visionAnalyzer:    analyzer,
	}

	got, inline := al.buildUserMessageWithMediaContext(context.Background(), "What is this?", []string{imagePath}, "trace-test")

	if analyzer.calls != 1 {
		t.Fatalf("analyzer call count = %d, want 1", analyzer.calls)
	}
	if len(inline) != 0 {
		t.Fatalf("inline media = %v, want []", inline)
	}
	if !strings.Contains(got, "Automatic image analysis") {
		t.Fatalf("message should include fallback section, got: %q", got)
	}
}
