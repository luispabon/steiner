package output

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// SanitizeDisplayText returns s with terminal control sequences removed so it
// can be written to a terminal without driving it. ANSI escape sequences (CSI,
// OSC, and others) are removed whole. Remaining C0 controls other than tab and
// newline, DEL, and C1 controls are dropped, including a bare carriage return
// that could overwrite the start of a line. Use it for display only; model-facing
// and persisted text keep the original bytes.
func SanitizeDisplayText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		}
		return r
	}, ansi.Strip(s))
}
