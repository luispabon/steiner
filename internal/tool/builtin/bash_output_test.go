package builtin

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func bashOutS(s string) bashOutStream { return bashOutStream{Head: []byte(s)} }

func TestBashPreviewInlineWhenFits(t *testing.T) {
	in := bashOutInput{Stdout: bashOutS("out\n"), Stderr: bashOutS("err"), InlineLimit: 1000}
	p := formatBashPreview(in)
	want := "out\n[stderr]\nerr"
	if p.Text != want || !p.Complete || p.OmittedBytes != 0 {
		t.Fatalf("got %+v want text %q complete", p, want)
	}
	in.Stdout = bashOutS("out")
	if got := formatBashPreview(in).Text; got != "out\n[stderr]\nerr" {
		t.Fatalf("separator: %q", got)
	}
	in.Stderr = bashOutStream{}
	in.Stdout = bashOutS("  keep ws \n\n")
	if got := formatBashPreview(in).Text; got != "  keep ws \n\n" {
		t.Fatalf("whitespace: %q", got)
	}
}

func TestBashPreviewExactBoundary(t *testing.T) {
	s := strings.Repeat("a", 50)
	for _, tc := range []struct {
		limit    int
		complete bool
	}{{50, true}, {49, false}, {51, true}} {
		p := formatBashPreview(bashOutInput{Stdout: bashOutS(s), InlineLimit: tc.limit})
		if p.Complete != tc.complete || len(p.Text) > tc.limit {
			t.Errorf("limit %d: complete=%v len=%d", tc.limit, p.Complete, len(p.Text))
		}
	}
}

func TestBashPreviewHeadTailSplit(t *testing.T) {
	s := strings.Repeat("h", 500) + strings.Repeat("t", 500)
	p := formatBashPreview(bashOutInput{Stdout: bashOutS(s), InlineLimit: 200})
	if len(p.Text) > 200 || p.Complete {
		t.Fatalf("len=%d complete=%v", len(p.Text), p.Complete)
	}
	marker := bashOutMarker(p.OmittedBytes)
	i := strings.Index(p.Text, marker)
	if i < 0 {
		t.Fatalf("marker %q missing in %q", marker, p.Text)
	}
	head, tail := p.Text[:i], p.Text[i+len(marker):]
	if !strings.HasPrefix(s, head) || !strings.HasSuffix(s, tail) {
		t.Fatalf("head/tail not from source")
	}
	if int64(len(head)+len(tail))+p.OmittedBytes != int64(len(s)) {
		t.Fatalf("accounting: %d+%d+%d", len(head), len(tail), p.OmittedBytes)
	}
	if len(tail) <= len(head)*2 || len(head) == 0 {
		t.Fatalf("expected ~30/70, head=%d tail=%d", len(head), len(tail))
	}
}

func TestBashPreviewCaptureOmission(t *testing.T) {
	in := bashOutInput{Stdout: bashOutStream{Head: []byte("HEAD"), Tail: []byte("TAIL"), Omitted: 12345}, InlineLimit: 1000}
	p := formatBashPreview(in)
	if p.Complete || p.OmittedBytes != 12345 {
		t.Fatalf("%+v", p)
	}
	if !strings.HasPrefix(p.Text, "HEAD") || !strings.HasSuffix(p.Text, "TAIL") || !strings.Contains(p.Text, "12345 bytes omitted") {
		t.Fatalf("text %q", p.Text)
	}
}

func TestBashPreviewNonzeroStderrPriority(t *testing.T) {
	stdout := strings.Repeat("o", 1000)
	stderr := "boom: failed\n"
	p := formatBashPreview(bashOutInput{Stdout: bashOutS(stdout), Stderr: bashOutS(stderr), ExitCode: 2, InlineLimit: 200})
	if len(p.Text) > 200 || !strings.HasSuffix(p.Text, "[stderr]\n"+stderr) {
		t.Fatalf("len=%d text=%q", len(p.Text), p.Text)
	}
	if p.Complete || !strings.Contains(p.Text, "bytes omitted") {
		t.Fatalf("stdout should be cut: %+v", p)
	}
	// Large stderr takes the whole budget; stdout is fully omitted.
	big := strings.Repeat("e", 500)
	p = formatBashPreview(bashOutInput{Stdout: bashOutS("stdout"), Stderr: bashOutS(big), ExitCode: 1, InlineLimit: 100})
	if len(p.Text) > 100 || strings.Contains(p.Text, "stdout") || p.Complete {
		t.Fatalf("len=%d text=%q", len(p.Text), p.Text)
	}
	if p.OmittedBytes < int64(len("stdout")) {
		t.Fatalf("omitted %d", p.OmittedBytes)
	}
}

func TestBashPreviewSuccessCombinedCut(t *testing.T) {
	in := bashOutInput{Stdout: bashOutS(strings.Repeat("o", 400)), Stderr: bashOutS(strings.Repeat("e", 400)), InlineLimit: 300}
	p := formatBashPreview(in)
	if len(p.Text) > 300 || p.Complete || !strings.HasPrefix(p.Text, "ooo") || !strings.HasSuffix(p.Text, "eee") {
		t.Fatalf("len=%d text=%q", len(p.Text), p.Text)
	}
	if p.OmittedBytes <= 0 || p.OmittedBytes > 800 {
		t.Fatalf("omitted %d", p.OmittedBytes)
	}
}

func TestBashPreviewTinyBudgets(t *testing.T) {
	for _, limit := range []int{-5, 0, 1, 5, 10, 20, 25, 30, 40} {
		for _, code := range []int{0, 1} {
			in := bashOutInput{Stdout: bashOutS(strings.Repeat("ö", 100)), Stderr: bashOutS(strings.Repeat("é", 100)), ExitCode: code, InlineLimit: limit}
			p := formatBashPreview(in)
			if len(p.Text) > max(limit, 0) || !utf8.ValidString(p.Text) {
				t.Errorf("limit %d code %d: len=%d valid=%v", limit, code, len(p.Text), utf8.ValidString(p.Text))
			}
			if p.Complete || p.OmittedBytes <= 0 {
				t.Errorf("limit %d code %d: must report omission: %+v", limit, code, p)
			}
		}
	}
	p := formatBashPreview(bashOutInput{Stdout: bashOutS("abcdefghij"), InlineLimit: 3})
	if p.Text != "" || p.OmittedBytes != 10 {
		t.Fatalf("marker cannot fit: %+v", p)
	}
}

func TestBashPreviewUTF8Boundaries(t *testing.T) {
	s := strings.Repeat("日本語", 200)
	for limit := 30; limit < 120; limit++ {
		p := formatBashPreview(bashOutInput{Stdout: bashOutS(s), InlineLimit: limit})
		if len(p.Text) > limit || !utf8.ValidString(p.Text) {
			t.Fatalf("limit %d: len=%d valid=%v", limit, len(p.Text), utf8.ValidString(p.Text))
		}
	}
}

func TestBashPreviewInvalidUTF8Expansion(t *testing.T) {
	// 10 invalid bytes become 30 bytes of U+FFFD, so they do not fit in 20.
	in := bashOutInput{Stdout: bashOutStream{Head: []byte(strings.Repeat("\xff", 10))}, InlineLimit: 100}
	if p := formatBashPreview(in); !p.Complete || len(p.Text) != 30 || !utf8.ValidString(p.Text) {
		t.Fatalf("%+v", p)
	}
	in.InlineLimit = 29
	if p := formatBashPreview(in); p.Complete || len(p.Text) > 29 || !utf8.ValidString(p.Text) {
		t.Fatalf("%+v", p)
	}
}

func TestBashArtifactFormatComplete(t *testing.T) {
	a := formatBashArtifact(bashOutInput{Stdout: bashOutS("o"), Stderr: bashOutS("e")})
	if a.Body != "[stdout]\no\n[stderr]\ne" || !a.Complete {
		t.Fatalf("%+v", a)
	}
	a = formatBashArtifact(bashOutInput{})
	if a.Body != "[stdout]\n\n[stderr]\n" || !a.Complete {
		t.Fatalf("empty: %+v", a)
	}
}

func TestBashArtifactFormatCaptureOmissionIncomplete(t *testing.T) {
	a := formatBashArtifact(bashOutInput{Stdout: bashOutStream{Head: []byte("h"), Tail: []byte("t"), Omitted: 99}, Stderr: bashOutS("e")})
	if a.Complete || a.StdoutOmitted != 99 || a.StderrOmitted != 0 || !strings.Contains(a.Body, "99 bytes omitted") {
		t.Fatalf("%+v", a)
	}
}

func TestBashArtifactFormatAllocation(t *testing.T) {
	big := strings.Repeat("x", 5000)
	overhead := len(bashOutStdoutLabel) + len(bashOutArtifactSep) + len(bashOutStderrLabel)
	for _, tc := range []struct {
		name        string
		code        int
		out, err    string
		limit       int
		wantOutKept int // approx kept bytes, 0 means check fully kept
		fullOut     bool
		fullErr     bool
	}{
		{name: "success both big", code: 0, out: big, err: big, limit: 1000 + overhead},
		{name: "nonzero both big", code: 1, out: big, err: big, limit: 1000 + overhead},
		{name: "success small stderr redistributes", code: 0, out: big, err: "tiny", limit: 1000 + overhead, fullErr: true},
		{name: "nonzero small stdout redistributes", code: 1, out: "tiny", err: big, limit: 1000 + overhead, fullOut: true},
		{name: "success small stdout redistributes", code: 0, out: "tiny", err: big, limit: 1000 + overhead, fullOut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := formatBashArtifact(bashOutInput{Stdout: bashOutS(tc.out), Stderr: bashOutS(tc.err), ExitCode: tc.code, ArtifactLimit: tc.limit})
			if len(a.Body) > tc.limit {
				t.Fatalf("body %d > limit %d", len(a.Body), tc.limit)
			}
			if tc.fullOut && a.StdoutOmitted != 0 || tc.fullErr && a.StderrOmitted != 0 {
				t.Fatalf("expected full stream: %+v", a)
			}
			if a.Complete {
				t.Fatalf("must be incomplete")
			}
			if !strings.Contains(a.Body, "[stdout]\n") || !strings.Contains(a.Body, "\n[stderr]\n") {
				t.Fatalf("labels missing")
			}
			if len(a.Body) < tc.limit-60 {
				t.Fatalf("budget under-used: %d of %d", len(a.Body), tc.limit)
			}
		})
	}
	// Priority split is 70/30 when both overflow.
	a := formatBashArtifact(bashOutInput{Stdout: bashOutS(big), Stderr: bashOutS(big), ExitCode: 1, ArtifactLimit: 1000 + overhead})
	if a.StderrOmitted >= a.StdoutOmitted {
		t.Fatalf("stderr should keep more: out omitted %d err omitted %d", a.StdoutOmitted, a.StderrOmitted)
	}
	a = formatBashArtifact(bashOutInput{Stdout: bashOutS(big), Stderr: bashOutS(big), ArtifactLimit: 1000 + overhead})
	if a.StdoutOmitted >= a.StderrOmitted {
		t.Fatalf("stdout should keep more: out omitted %d err omitted %d", a.StdoutOmitted, a.StderrOmitted)
	}
}

func TestBashArtifactFormatCapAndTiny(t *testing.T) {
	big := strings.Repeat("日", 600000) // 1.8 MB
	a := formatBashArtifact(bashOutInput{Stdout: bashOutS(big), Stderr: bashOutS(big)})
	if len(a.Body) > bashArtifactMaxBytes || a.Complete || !utf8.ValidString(a.Body) {
		t.Fatalf("len=%d complete=%v", len(a.Body), a.Complete)
	}
	a = formatBashArtifact(bashOutInput{Stdout: bashOutS(big), ArtifactLimit: bashArtifactMaxBytes * 2})
	if len(a.Body) > bashArtifactMaxBytes {
		t.Fatalf("limit not capped: %d", len(a.Body))
	}
	for limit := 1; limit < 58; limit++ {
		a := formatBashArtifact(bashOutInput{Stdout: bashOutS("日本語日本語日本語"), Stderr: bashOutS("éééééé"), ArtifactLimit: limit})
		if len(a.Body) > limit || !utf8.ValidString(a.Body) || a.Complete {
			t.Fatalf("limit %d: len=%d complete=%v", limit, len(a.Body), a.Complete)
		}
		if a.StdoutOmitted+a.StderrOmitted <= 0 {
			t.Fatalf("limit %d: no omission metadata", limit)
		}
	}
}
