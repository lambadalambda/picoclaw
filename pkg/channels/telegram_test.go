package channels

import (
	"fmt"
	"strings"
	"testing"
)

func TestIsImageFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Images
		{"/tmp/photo.jpg", true},
		{"/tmp/photo.jpeg", true},
		{"/tmp/photo.JPG", true},
		{"/tmp/image.png", true},
		{"/tmp/image.PNG", true},
		{"/tmp/animation.gif", true},
		{"/tmp/sticker.webp", true},
		{"photo.JPEG", true},

		// Non-images
		{"/tmp/report.pdf", false},
		{"/tmp/data.txt", false},
		{"/tmp/archive.zip", false},
		{"/tmp/video.mp4", false},
		{"/tmp/audio.mp3", false},
		{"/tmp/binary.exe", false},
		{"", false},
		{"noextension", false},
		{"/tmp/.jpg", true}, // hidden file with image extension — still an image
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isImageFile(tt.path)
			if got != tt.want {
				t.Errorf("isImageFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractCodeBlocks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCodes int
		// Each placeholder should have a unique, sequential index
		wantDistinctPlaceholders int
	}{
		{
			name:                     "no code blocks",
			input:                    "hello world",
			wantCodes:                0,
			wantDistinctPlaceholders: 0,
		},
		{
			name:                     "single code block",
			input:                    "before\n```go\nfmt.Println(\"hi\")\n```\nafter",
			wantCodes:                1,
			wantDistinctPlaceholders: 1,
		},
		{
			name:                     "two code blocks",
			input:                    "```\nfirst\n```\nmiddle\n```\nsecond\n```",
			wantCodes:                2,
			wantDistinctPlaceholders: 2,
		},
		{
			name:                     "three code blocks",
			input:                    "```\nA\n```\n```\nB\n```\n```\nC\n```",
			wantCodes:                3,
			wantDistinctPlaceholders: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractCodeBlocks(tt.input)
			if len(result.codes) != tt.wantCodes {
				t.Errorf("got %d codes, want %d", len(result.codes), tt.wantCodes)
			}

			// Check that each placeholder has a unique index
			seen := make(map[string]bool)
			for i := 0; i < len(result.codes); i++ {
				placeholder := fmt.Sprintf("\x00CB%d\x00", i)
				if !strings.Contains(result.text, placeholder) {
					t.Errorf("missing placeholder %q in result text %q", placeholder, result.text)
				}
				seen[placeholder] = true
			}
			if len(seen) != tt.wantDistinctPlaceholders {
				t.Errorf("got %d distinct placeholders, want %d", len(seen), tt.wantDistinctPlaceholders)
			}
		})
	}
}

func TestMarkdownToTelegramHTML(t *testing.T) {
	// Verify existing functionality still works
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "plain text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "bold text",
			input: "**bold**",
			want:  "<b>bold</b>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdownToTelegramHTML(tt.input)
			if got != tt.want {
				t.Errorf("markdownToTelegramHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
