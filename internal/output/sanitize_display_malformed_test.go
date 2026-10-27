package output

import "testing"

// TestSanitizeDisplayTextMalformedSequences covers incomplete and malformed
// escape input. An unterminated string sequence (OSC, DCS) hides the text after
// its introducer, as a terminal would, and text before it is kept.
func TestSanitizeDisplayTextMalformedSequences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "incomplete CSI at end", in: "a\x1b[31", want: "a"},
		{name: "CSI interrupted by new ESC keeps remainder", in: "a\x1b[31\x1b[0mb", want: "ab"},
		{name: "unterminated OSC hides rest", in: "a\x1b]0;title", want: "a"},
		{name: "unterminated DCS hides rest", in: "a\x1bP1$r", want: "a"},
		{name: "ESC before CR consumes next byte as final char", in: "a\x1b\rb", want: "a"},
		{name: "8-bit CSI removed with its parameters", in: "a\u009b31mb", want: "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeDisplayText(tt.in); got != tt.want {
				t.Errorf("SanitizeDisplayText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
