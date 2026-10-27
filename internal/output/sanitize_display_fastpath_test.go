package output

import "testing"

// sanitizeDisplaySink keeps AllocsPerRun results from being optimised away.
var sanitizeDisplaySink string

// sanitizeFastPathCases covers the input classes on either side of the
// printable-ASCII fast path. The fast path must not change any of them.
var sanitizeFastPathCases = []struct {
	name string
	in   string
}{
	{name: "empty", in: ""},
	{name: "plain ASCII", in: "hello, world! ~{}[]<>"},
	{name: "tab", in: "a\tb"},
	{name: "newline", in: "a\nb"},
	{name: "lone tab", in: "\t"},
	{name: "tab and newline", in: "a\tb\nc"},
	{name: "multi-line ASCII", in: "line one\nline two\n"},
	{name: "trailing newline", in: "text\n"},
	{name: "CRLF", in: "\r\n"},
	{name: "trailing carriage return", in: "line\r"},
	{name: "carriage return", in: "a\rb"},
	{name: "SGR escape", in: "\x1b[31mred\x1b[0m"},
	{name: "CSI clear screen", in: "a\x1b[2Jb"},
	{name: "OSC title with BEL", in: "a\x1b]0;title\x07b"},
	{name: "OSC title with ST", in: "a\x1b]0;title\x1b\\b"},
	{name: "DEL", in: "a\x7fb"},
	{name: "8-bit CSI U+009B", in: "a\u009b31mb"},
	{name: "bidi override U+202E", in: "a\u202eb"},
	{name: "invalid UTF-8", in: "a\xffb"},
	{name: "mixed", in: "x\t\x1b[1m\u009b\ny\u202e\xc3(z\x7f"},
}

func TestSanitizeDisplayTextFastPathMatchesSlow(t *testing.T) {
	t.Parallel()

	for _, tt := range sanitizeFastPathCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, want := SanitizeDisplayText(tt.in), sanitizeDisplayTextSlow(tt.in); got != want {
				t.Errorf("SanitizeDisplayText(%q) = %q, slow path = %q", tt.in, got, want)
			}
		})
	}
}

// TestSanitizeDisplayTextCleanASCIINoAllocs must not run in parallel:
// AllocsPerRun reads process-wide allocation counters.
func TestSanitizeDisplayTextCleanASCIINoAllocs(t *testing.T) {
	inputs := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "word", in: "general-purpose"},
		{name: "multi-line ASCII", in: "line one\nline two\n"},
		{name: "tab-separated ASCII", in: "col1\tcol2\tcol3"},
		{name: "printable ASCII", in: " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"},
	}

	for _, tt := range inputs {
		allocs := testing.AllocsPerRun(100, func() {
			sanitizeDisplaySink = SanitizeDisplayText(tt.in)
		})
		if allocs != 0 {
			t.Errorf("SanitizeDisplayText(%s) allocs = %v, want 0", tt.name, allocs)
		}
	}
}

// TestIsSafeDisplayASCII pins which inputs take the fast path. Carriage return
// must not: the slow path drops it, so returning the input unchanged would
// differ.
func TestIsSafeDisplayASCII(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: true},
		{name: "printable ASCII", in: "hello, ~{}", want: true},
		{name: "lone tab", in: "\t", want: true},
		{name: "tab and newline", in: "a\tb\nc", want: true},
		{name: "carriage return", in: "a\rb", want: false},
		{name: "CRLF", in: "\r\n", want: false},
		{name: "trailing carriage return", in: "line\r", want: false},
		{name: "NUL", in: "a\x00b", want: false},
		{name: "vertical tab", in: "a\x0bb", want: false},
		{name: "form feed", in: "a\x0cb", want: false},
		{name: "ESC", in: "\x1b[31m", want: false},
		{name: "DEL", in: "a\x7fb", want: false},
		{name: "non-ASCII", in: "a\u00e9b", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSafeDisplayASCII(tt.in); got != tt.want {
				t.Errorf("isSafeDisplayASCII(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func FuzzSanitizeDisplayTextFastPath(f *testing.F) {
	for _, tt := range sanitizeFastPathCases {
		f.Add(tt.in)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := SanitizeDisplayText(s), sanitizeDisplayTextSlow(s); got != want {
			t.Errorf("SanitizeDisplayText(%q) = %q, slow path = %q", s, got, want)
		}
	})
}
