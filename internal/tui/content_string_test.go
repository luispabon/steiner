package tui

import "strings"

// String returns the rendered transcript: blocks(width) joined with "\n". The
// viewport consumes blocks() directly, so only tests need the joined form.
func (b *contentBuffer) String(width int) string {
	return strings.Join(b.blocks(width), "\n")
}
