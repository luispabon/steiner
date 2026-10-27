package tool

import "fmt"

// TruncationMarker formats a truncation marker for tool output.
func TruncationMarker(shown, total int) string {
	return fmt.Sprintf("<truncated output shown=%d total=%d>", shown, total)
}
