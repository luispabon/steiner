package builtin

import (
	"strings"
	"testing"
)

func TestBashCleanText(t *testing.T) {
	tests := []struct {
		name             string
		in               string
		cutStart, cutEnd bool
		want             string
		wantSkipped      int
	}{
		{name: "plain", in: "hello\n", want: "hello\n"},
		{name: "crlf", in: "a\r\nb\r\n", want: "a\nb\n"},
		{name: "trailing cr", in: "a\r", want: "a\r"},
		{name: "cr cr lf", in: "a\r\r\nb", want: "a\r\nb"},
		{name: "standalone cr", in: "50%\r100%\n", want: "50%\r100%\n"},
		{name: "standalone controls", in: "a\x00b\x07c\x08d\x7fe\tf", want: "a\x00b\x07c\x08d\x7fe\tf"},
		{name: "utf8 c1 survives", in: "a\u009b31mb\u009d0;x\u009c", want: "a\u009b31mb\u009d0;x\u009c"},
		{name: "sgr", in: "\x1b[1;31mred\x1b[0m", want: "red"},
		{name: "csi cursor", in: "a\x1b[2K\x1b[1Gb", want: "ab"},
		{name: "esc final", in: "a\x1bcb\x1b(Bc", want: "abc"},
		{name: "osc title bel", in: "\x1b]0;title\x07x", want: "x"},
		{name: "osc title st", in: "\x1b]2;title\x1b\\x", want: "x"},
		{name: "dcs st", in: "a\x1bPq#0\x1b\\b", want: "ab"},
		{name: "osc8 bel", in: "\x1b]8;;http://a/b\x07link\x1b]8;;\x07 done", want: "link (http://a/b) done"},
		{name: "osc8 st", in: "\x1b]8;id=1;http://a/b\x1b\\link\x1b]8;;\x1b\\!", want: "link (http://a/b)!"},
		{name: "osc8 reopen closes previous", in: "\x1b]8;;http://a\x07A\x1b]8;;http://b\x07B\x1b]8;;\x07", want: "A (http://a)B (http://b)"},
		{name: "osc8 uri has newline stripped", in: "\x1b]8;;http://a\nb\x07x\x1b]8;;\x07", want: "x (http://ab)"},
		{name: "osc8 open at end closed", in: "\x1b]8;;http://a\x07tail", want: "tail (http://a)"},
		{name: "osc8 open at cut end closed", in: "\x1b]8;;http://a\x07tail", cutEnd: true, want: "tail (http://a)"},
		{name: "osc8 missing uri field", in: "\x1b]8;x\x07t", want: "[malformed escape: \\x1b]8;x\\x07]t"},
		{name: "malformed csi control", in: "a\x1b[\x01mb", want: "a[malformed escape: \\x1b[]\x01mb"},
		{name: "malformed csi newline", in: "\x1b[3\nx", want: "[malformed escape: \\x1b[3]\nx"},
		{name: "malformed osc interrupted", in: "\x1b]0;a\x1b[0mz", want: "[malformed escape: \\x1b]0;a]z"},
		{name: "lone esc mid", in: "\x1b\x1b[0mz", want: "[malformed escape: \\x1b]z"},
		{name: "incomplete csi", in: "ok\x1b[31", want: "ok[malformed escape: \\x1b[31]"},
		{name: "incomplete osc", in: "ok\x1b]0;ti", want: "ok[malformed escape: \\x1b]0;ti]"},
		{name: "incomplete lone esc", in: "ok\x1b", want: "ok[malformed escape: \\x1b]"},
		{name: "incomplete label truncated", in: "\x1b]0;0123456789", want: "[malformed escape: \\x1b]0;0123...]"},
		{name: "cut end drops incomplete csi", in: "ok\x1b[31", cutEnd: true, want: "ok"},
		{name: "cut end drops incomplete osc", in: "ok\x1b]0;ti", cutEnd: true, want: "ok"},
		{name: "cut end drops lone esc", in: "ok\x1b", cutEnd: true, want: "ok"},
		{name: "cut end drops partial rune", in: "ok\xe2\x82", cutEnd: true, want: "ok"},
		{name: "cut end keeps full rune", in: "ok\u20ac", cutEnd: true, want: "ok\u20ac"},
		{name: "cut end still labels mid malformed", in: "\x1b[3\nx", cutEnd: true, want: "[malformed escape: \\x1b[3]\nx"},
		{name: "no cut end keeps partial rune", in: "ok\xe2\x82", want: "ok\xe2\x82"},
		{name: "cut start to newline", in: "rest of line\nnext\n", cutStart: true, want: "next\n", wantSkipped: 13},
		{name: "cut start mid escape", in: "31mred\x1b[0m\nok", cutStart: true, want: "ok", wantSkipped: 11},
		{name: "cut start utf8 boundary", in: "\x82\xacabc", cutStart: true, want: "abc", wantSkipped: 2},
		{name: "cut start on boundary no newline", in: "abc", cutStart: true, want: "abc"},
		{name: "cut start newline first", in: "\nabc", cutStart: true, want: "abc", wantSkipped: 1},
		{name: "cut start empty", in: "", cutStart: true, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, skipped := cleanBashText([]byte(tc.in), tc.cutStart, tc.cutEnd)
			if got != tc.want || skipped != tc.wantSkipped {
				t.Fatalf("cleanBashText(%q, %v, %v) = (%q, %d), want (%q, %d)",
					tc.in, tc.cutStart, tc.cutEnd, got, skipped, tc.want, tc.wantSkipped)
			}
		})
	}
}

func TestBashCleanTextStartWindow(t *testing.T) {
	// Newline beyond the 4 KiB window is ignored; only UTF-8 alignment applies.
	in := strings.Repeat("x", bashCleanStartWindow) + "\nrest"
	got, skipped := cleanBashText([]byte(in), true, false)
	if skipped != 0 || got != in {
		t.Fatalf("skipped=%d, len(got)=%d; want 0 and unchanged", skipped, len(got))
	}
	// Newline exactly at the last byte of the window is used.
	in = strings.Repeat("x", bashCleanStartWindow-1) + "\nrest"
	got, skipped = cleanBashText([]byte(in), true, false)
	if skipped != bashCleanStartWindow || got != "rest" {
		t.Fatalf("skipped=%d got=%q; want %d and rest", skipped, got, bashCleanStartWindow)
	}
}

func TestBashCleanTextExpansionBounded(t *testing.T) {
	for _, in := range []string{
		strings.Repeat("\x1b", 1000),
		strings.Repeat("\x1b[\x01", 1000),
		strings.Repeat("\x1b]8;;u\x07x", 500),
		strings.Repeat("\x1b\x00", 1000),
	} {
		got, _ := cleanBashText([]byte(in), false, false)
		if len(got) > bashCleanMaxExpansion*len(in) {
			t.Fatalf("len(out)=%d exceeds %d*len(in)=%d", len(got), bashCleanMaxExpansion, len(in))
		}
	}
}
