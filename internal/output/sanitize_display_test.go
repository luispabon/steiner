package output

import "testing"

func TestSanitizeDisplayText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text unchanged", in: "hello\tworld\nline", want: "hello\tworld\nline"},
		{name: "unicode unchanged", in: "héllo → ✓", want: "héllo → ✓"},
		{name: "SGR color removed", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "CSI clear screen removed", in: "a\x1b[2Jb", want: "ab"},
		{name: "OSC title removed with BEL", in: "a\x1b]0;pwned\x07b", want: "ab"},
		{name: "OSC title removed with ST", in: "a\x1b]0;pwned\x1b\\b", want: "ab"},
		{name: "trailing lone ESC removed", in: "a\x1b", want: "a"},
		{name: "carriage return dropped", in: "old\rnew", want: "oldnew"},
		{name: "CRLF becomes LF", in: "a\r\nb", want: "a\nb"},
		{name: "BEL and backspace dropped", in: "a\x07\x08b", want: "ab"},
		{name: "DEL dropped", in: "a\x7fb", want: "ab"},
		{name: "C1 control dropped", in: "a\u0085b", want: "ab"},
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
