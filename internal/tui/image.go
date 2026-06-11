package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// imagePlaceholder returns display text for an image block.
func imagePlaceholder(img agent.ImageBlock) string {
	if img.Width > 0 && img.Height > 0 {
		return fmt.Sprintf("[image: %dx%d %s %s]", img.Width, img.Height, output.MediaTypeShort(img.MediaType), output.FormatFileSize(img.SizeBytes))
	}
	if img.SizeBytes > 0 {
		return fmt.Sprintf("[image: %s %s]", output.MediaTypeShort(img.MediaType), output.FormatFileSize(img.SizeBytes))
	}
	return "[image]"
}

// imageBlocksText returns placeholder text for all images in a message,
// separated by newlines.
func imageBlocksText(images []agent.ImageBlock) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, img := range images {
		parts = append(parts, imagePlaceholder(img))
	}
	return strings.Join(parts, "\n")
}
