package channels

import (
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
