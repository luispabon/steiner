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
