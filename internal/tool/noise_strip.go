package tool

import "fmt"

// ShapeIngestedToolResult returns a tool result payload for prompt ingestion.
// Tool handlers own their output budget and cleanup, so every result passes
// through unchanged.
func ShapeIngestedToolResult(_, content string) string {
	return content
}

// TruncationMarker formats a truncation marker for tool output.
func TruncationMarker(shown, total int) string {
	return fmt.Sprintf("<truncated output shown=%d total=%d>", shown, total)
}
