package providers

import (
	"encoding/base64"
	"image"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
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
		{model: "glm-5v-turbo", provider: &HTTPProvider{}},
		{model: "gpt-4o", provider: &HTTPProvider{}},
	})

	if SupportsInlineVisionTransport(fp, "glm-5") {
		t.Fatal("SupportsInlineVisionTransport(glm-5) = true, want false")
	}
	if !SupportsInlineVisionTransport(fp, "gpt-4o") {
		t.Fatal("SupportsInlineVisionTransport(gpt-4o) = false, want true")
	}
	if !SupportsInlineVisionTransport(fp, "glm-5v-turbo") {
		t.Fatal("SupportsInlineVisionTransport(glm-5v-turbo) = false, want true")
	}
}

func TestLoadInlineImageData_ShrinksLargeImages(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "big.png")

	// Generate a deterministic noisy image that will be large as PNG.
	w, h := 1024, 1024
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i+0] = uint8(rng.Intn(256))
			img.Pix[i+1] = uint8(rng.Intn(256))
			img.Pix[i+2] = uint8(rng.Intn(256))
			img.Pix[i+3] = 255
		}
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode png fixture: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	if st.Size() < 1024*1024 {
		t.Fatalf("expected fixture to be >1MB, got %d bytes", st.Size())
	}

	got, err := loadInlineImageData(path, "")
	if err != nil {
		t.Fatalf("loadInlineImageData() error = %v", err)
	}
	if got.MediaType != "image/jpeg" {
		t.Fatalf("MediaType = %q, want image/jpeg", got.MediaType)
	}
	if !strings.HasPrefix(got.DataURL, "data:image/jpeg;base64,") {
		t.Fatalf("DataURL prefix mismatch, got %q", got.DataURL[:minInt(32, len(got.DataURL))])
	}

	decoded, err := base64.StdEncoding.DecodeString(got.Base64Data)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if int64(len(decoded)) >= st.Size() {
		t.Fatalf("expected resized bytes to be smaller than original (%d >= %d)", len(decoded), st.Size())
	}
	if len(decoded) > 1024*1024 {
		t.Fatalf("expected resized bytes <=1MB, got %d", len(decoded))
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
