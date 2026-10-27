package output

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// SanitizeDisplayText returns s with terminal control sequences removed so it
// can be written to a terminal without driving it. ANSI escape sequences (CSI,
// OSC, and others) are removed whole, and so is an 8-bit CSI (U+009B) with its
// parameter and intermediate bytes. Remaining C0 controls other than tab and
// newline, DEL, and other C1 controls are dropped, including a bare carriage
// return that could overwrite the start of a line. Bidi controls and line or
// paragraph separators are dropped too, since they can reorder or split
// displayed text. Use it for display only; model-facing and persisted text keep
// the original bytes.
func SanitizeDisplayText(s string) string {
	if isSafeDisplayASCII(s) {
		return s
	}
	return sanitizeDisplayTextSlow(s)
}

// isSafeDisplayASCII reports whether every byte of s is tab, newline, or in
// 0x20 to 0x7e. The slow path returns such text unchanged: ansi.Strip writes
// those bytes through (no ESC is present, so the parser stays in ground state),
// stripC1CSI finds no U+009B, and the strings.Map callback keeps them. Other
// C0 controls, including carriage return, are rejected because the slow path
// drops them.
func isSafeDisplayASCII(s string) bool {
	for i := range len(s) {
		if (s[i] < 0x20 && s[i] != '\t' && s[i] != '\n') || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// sanitizeDisplayTextSlow is the full sanitiser behind SanitizeDisplayText.
// Tests compare the fast path against it.
func sanitizeDisplayTextSlow(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
			r == 0x2028, r == 0x2029, r == 0x200e, r == 0x200f, r == 0x061c:
			return -1
		}
		return r
	}, stripC1CSI(ansi.Strip(s)))
}

// stripC1CSI removes 8-bit CSI sequences: U+009B, then parameter bytes
// (0x30-0x3f), then intermediate bytes (0x20-0x2f), then one final byte
// (0x40-0x7e). Dropping only the introducer would leave the parameters as
// visible text.
func stripC1CSI(s string) string {
	if !strings.ContainsRune(s, 0x9b) {
		return s
	}
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != 0x9b {
			b.WriteRune(runes[i])
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] >= 0x30 && runes[j] <= 0x3f {
			j++
		}
		for j < len(runes) && runes[j] >= 0x20 && runes[j] <= 0x2f {
			j++
		}
		if j < len(runes) && runes[j] >= 0x40 && runes[j] <= 0x7e {
			j++
		}
		i = j - 1
	}
	return b.String()
}
