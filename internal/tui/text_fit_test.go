package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// singleLineDisplaySink keeps AllocsPerRun results from being optimised away.
var singleLineDisplaySink string

// singleLineReplacePath is the full pre-optimisation path: a new replacer on
// every call, applied after SanitizeDisplayText. Tests compare
// singleLineDisplayText against it.
func singleLineReplacePath(text string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(output.SanitizeDisplayText(text))
}

func TestSingleLineDisplayText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain ASCII unchanged", in: "general-purpose", want: "general-purpose"},
		{name: "tab becomes space", in: "a\tb", want: "a b"},
		{name: "newline becomes space", in: "a\nb", want: "a b"},
		{name: "CRLF becomes one space", in: "a\r\nb", want: "a b"},
		{name: "carriage return dropped", in: "a\rb", want: "ab"},
		{name: "SGR escape removed", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "OSC title removed", in: "a\x1b]0;title\x07b", want: "ab"},
		{name: "DEL dropped", in: "a\x7fb", want: "ab"},
		{name: "8-bit CSI removed", in: "a\u009b31mb", want: "ab"},
		{name: "bidi override dropped", in: "a\u202eb", want: "ab"},
		{name: "invalid UTF-8 byte dropped", in: "a\xffb", want: "ab"},
		{name: "mixed", in: "x\t\x1b[1my\u202e\n\x7fz", want: "x y z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := singleLineDisplayText(tt.in); got != tt.want {
				t.Errorf("singleLineDisplayText(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if got, want := singleLineDisplayText(tt.in), singleLineReplacePath(tt.in); got != want {
				t.Errorf("singleLineDisplayText(%q) = %q, replace path = %q", tt.in, got, want)
			}
		})
	}
}

// TestSingleLineDisplayTextCleanASCIINoAllocs must not run in parallel:
// AllocsPerRun reads process-wide allocation counters.
func TestSingleLineDisplayTextCleanASCIINoAllocs(t *testing.T) {
	inputs := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "agent id", in: "agent-7f3a9c"},
		{name: "type label", in: "general-purpose"},
	}

	for _, tt := range inputs {
		allocs := testing.AllocsPerRun(100, func() {
			singleLineDisplaySink = singleLineDisplayText(tt.in)
		})
		if allocs != 0 {
			t.Errorf("singleLineDisplayText(%s) allocs = %v, want 0", tt.name, allocs)
		}
	}
}

func FuzzSingleLineDisplayTextFastPath(f *testing.F) {
	for _, seed := range []string{"", "plain", "a\tb", "a\nb", "a\r\nb", "\x1b[31mred", "a\u009b31mb", "a\u202eb", "a\xffb", "x\t\x1b[1my\u202e\n\x7fz"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := singleLineDisplayText(s), singleLineReplacePath(s); got != want {
			t.Errorf("singleLineDisplayText(%q) = %q, replace path = %q", s, got, want)
		}
	})
}
