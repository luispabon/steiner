package output

import (
	"fmt"
)

// ImagePlaceholder returns display text for an image given its metadata.
// Used in non-TUI output rendering.
func ImagePlaceholder(width, height int, mediaType string, sizeBytes int) string {
	if width > 0 && height > 0 {
		return fmt.Sprintf("[image: %dx%d %s %s]", width, height, MediaTypeShort(mediaType), FormatFileSize(sizeBytes))
	}
	if sizeBytes > 0 {
		return fmt.Sprintf("[image: %s %s]", MediaTypeShort(mediaType), FormatFileSize(sizeBytes))
	}
	return "[image]"
}

// MediaTypeShort returns a short form of a media type (e.g., "image/png" -> "png").
func MediaTypeShort(mediaType string) string {
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

// FormatFileSize formats a byte count as a human-readable size (e.g., "512B", "234KB", "1.5MB").
func FormatFileSize(n int) string {
	if n == 0 {
		return ""
	}
	const (
		kilobyte = 1024
		megabyte = kilobyte * 1024
	)
	if n >= megabyte {
		return fmt.Sprintf("%.1fMB", float64(n)/float64(megabyte))
	}
	if n >= kilobyte {
		return fmt.Sprintf("%dKB", n/kilobyte)
	}
	return fmt.Sprintf("%dB", n)
}
