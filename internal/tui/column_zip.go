package tui

import "strings"

// zipColumns composes three side-by-side blocks by concatenating
// corresponding lines, without the ragged-block width measurement
// lipgloss.JoinHorizontal performs. It is only valid when every block is
// already rectangular: every line the same width, and all blocks the same
// height. renderBaseView guarantees this — the sidebar, divider, and main
// column are each rendered at fixed width/height styles — so this produces
// byte-identical output to lipgloss.JoinHorizontal(lipgloss.Top, ...) at a
// fraction of the cost (JoinHorizontal calls ansi.StringWidth per line to
// find the max width and pad short lines, which is unnecessary work when the
// blocks are already the same width).
func zipColumns(left, divider, right string) string {
	l := strings.Split(left, "\n")
	d := strings.Split(divider, "\n")
	r := strings.Split(right, "\n")
	n := max(len(l), max(len(d), len(r)))
	at := func(s []string, i int) string {
		if i < len(s) {
			return s[i]
		}
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(left) + len(divider) + len(right) + n)
	for i := range n {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(at(l, i))
		sb.WriteString(at(d, i))
		sb.WriteString(at(r, i))
	}
	return sb.String()
}
