package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestImagePlaceholder_WithDimensions(t *testing.T) {
	img := agent.ImageBlock{
		MediaType: "image/png",
		Width:     1024,
		Height:    768,
		SizeBytes: 234 * 1024, // 234 KB
	}
	expected := "[image: 1024x768 png 234KB]"
	got := imagePlaceholder(img)
	if got != expected {
		t.Errorf("imagePlaceholder with dimensions: want %q, got %q", expected, got)
	}
}

func TestImagePlaceholder_WithSize_NoDimensions(t *testing.T) {
	img := agent.ImageBlock{
		MediaType: "image/jpeg",
		Width:     0,
		Height:    0,
		SizeBytes: 2 * 1024 * 1024, // 2 MB
	}
	expected := "[image: jpg 2.0MB]"
	got := imagePlaceholder(img)
	if got != expected {
		t.Errorf("imagePlaceholder with size, no dimensions: want %q, got %q", expected, got)
	}
}

func TestImagePlaceholder_NoData(t *testing.T) {
	img := agent.ImageBlock{
		MediaType: "",
		Width:     0,
		Height:    0,
		SizeBytes: 0,
	}
	expected := "[image]"
	got := imagePlaceholder(img)
	if got != expected {
		t.Errorf("imagePlaceholder with no data: want %q, got %q", expected, got)
	}
}

func TestMediaTypeShort(t *testing.T) {
	tests := []struct {
		mediaType string
		expected  string
	}{
		{"image/png", "png"},
		{"image/jpeg", "jpg"},
		{"image/gif", "gif"},
		{"image/webp", "webp"},
		{"image/svg+xml", "image/svg+xml"},
		{"", ""},
	}
	for _, tt := range tests {
		got := mediaTypeShort(tt.mediaType)
		if got != tt.expected {
			t.Errorf("mediaTypeShort(%q): want %q, got %q", tt.mediaType, tt.expected, got)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		bytes    int
		expected string
	}{
		{0, ""},
		{512, "0KB"},
		{1024, "1KB"},
		{234 * 1024, "234KB"},
		{1024 * 1024, "1.0MB"},
		{1536 * 1024, "1.5MB"}, // 1.5 * 1024 * 1024
		{10956800, "10.4MB"},   // 10.4 * 1024 * 1024
	}
	for _, tt := range tests {
		got := formatFileSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("formatFileSize(%d): want %q, got %q", tt.bytes, tt.expected, got)
		}
	}
}

func TestImageBlocksText_Multiple(t *testing.T) {
	images := []agent.ImageBlock{
		{
			MediaType: "image/png",
			Width:     800,
			Height:    600,
			SizeBytes: 100 * 1024,
		},
		{
			MediaType: "image/jpeg",
			Width:     1920,
			Height:    1080,
			SizeBytes: 500 * 1024,
		},
	}
	got := imageBlocksText(images)
	expected := "[image: 800x600 png 100KB]\n[image: 1920x1080 jpg 500KB]"
	if got != expected {
		t.Errorf("imageBlocksText: want %q, got %q", expected, got)
	}
}

func TestImageBlocksText_Empty(t *testing.T) {
	images := []agent.ImageBlock{}
	got := imageBlocksText(images)
	if got != "" {
		t.Errorf("imageBlocksText(empty): want empty string, got %q", got)
	}
}
