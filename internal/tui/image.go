package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

// imagePlaceholder returns display text for an image block.
func imagePlaceholder(img agent.ImageBlock) string {
	if img.Width > 0 && img.Height > 0 {
		return fmt.Sprintf("[image: %dx%d %s %s]", img.Width, img.Height, mediaTypeShort(img.MediaType), formatFileSize(img.SizeBytes))
	}
	if img.SizeBytes > 0 {
		return fmt.Sprintf("[image: %s %s]", mediaTypeShort(img.MediaType), formatFileSize(img.SizeBytes))
	}
	return "[image]"
}

// mediaTypeShort returns a short form of a media type (e.g., "image/png" -> "png").
func mediaTypeShort(mediaType string) string {
	switch mediaType {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return mediaType
	}
}

// formatFileSize formats a byte count as a human-readable size (e.g., "234KB" or "1.5MB").
func formatFileSize(n int) string {
	if n == 0 {
		return ""
	}
	const (
		kilobyte = 1024
		megabyte = kilobyte * 1024
	)
	if n < megabyte {
		return fmt.Sprintf("%dKB", n/kilobyte)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/float64(megabyte))
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
