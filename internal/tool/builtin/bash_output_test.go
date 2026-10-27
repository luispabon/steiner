package builtin

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func bashOutS(s string) bashOutStream { return bashOutStream{Head: []byte(s)} }

func bashOutTotal(p bashOutPreview) int64 { return p.StdoutOmitted + p.StderrOmitted }

func TestBashPreviewInlineWhenFits(t *testing.T) {
	in := bashOutInput{Stdout: bashOutS("out\n"), Stderr: bashOutS("err"), InlineLimit: 1000}
	p := formatBashPreview(in)
	want := "out\n[stderr]\nerr"
	if p.Text != want || !p.Complete || bashOutTotal(p) != 0 {
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
	// Both streams: whole output exactly at, and one under, the limit.
	in := bashOutInput{Stdout: bashOutS("o\n"), Stderr: bashOutS("e"), InlineLimit: len("o\n[stderr]\ne")}
	for _, code := range []int{0, 1} {
		in.ExitCode = code
		if p := formatBashPreview(in); !p.Complete || p.Text != "o\n[stderr]\ne" {
			t.Errorf("code %d exact fit lost: %+v", code, p)
		}
		in.InlineLimit--
		if p := formatBashPreview(in); p.Complete || len(p.Text) > in.InlineLimit {
			t.Errorf("code %d one under: %+v", code, p)
		}
		in.InlineLimit++
	}
}

func TestBashPreviewHeadTailSplit(t *testing.T) {
	s := strings.Repeat("h", 500) + strings.Repeat("t", 500)
	p := formatBashPreview(bashOutInput{Stdout: bashOutS(s), InlineLimit: 200})
	if len(p.Text) > 200 || p.Complete {
		t.Fatalf("len=%d complete=%v", len(p.Text), p.Complete)
	}
	marker := bashOutMarker(p.StdoutOmitted)
	i := strings.Index(p.Text, marker)
	if i < 0 {
		t.Fatalf("marker %q missing in %q", marker, p.Text)
	}
	head, tail := p.Text[:i], p.Text[i+len(marker):]
	if !strings.HasPrefix(s, head) || !strings.HasSuffix(s, tail) {
		t.Fatalf("head/tail not from source")
	}
	if int64(len(head)+len(tail))+p.StdoutOmitted != int64(len(s)) {
		t.Fatalf("accounting: %d+%d+%d", len(head), len(tail), p.StdoutOmitted)
	}
	// avail = 200 - len(marker for 1000) = 200-30 = 170: head 51, tail 119.
	if len(head) != 51 || len(tail) != 119 {
		t.Fatalf("expected 30/70 of 170, head=%d tail=%d", len(head), len(tail))
	}
}

func TestBashPreviewCaptureOmission(t *testing.T) {
	in := bashOutInput{Stdout: bashOutStream{Head: []byte("HEAD"), Tail: []byte("TAIL"), Omitted: 12345}, InlineLimit: 1000}
	p := formatBashPreview(in)
	want := "HEAD\n[... 12345 bytes omitted ...]\nTAIL"
	if p.Complete || p.Text != want || p.StdoutOmitted != 12345 || p.SourceOmitted != 12345 {
		t.Fatalf("%+v", p)
	}
}

func TestBashPreviewSourceVsRenderedOmission(t *testing.T) {
	// 10 source bytes missing plus a rendered cut: total exceeds source.
	s := bashOutStream{Head: []byte(strings.Repeat("A", 100)), Tail: []byte(strings.Repeat("B", 100)), Omitted: 10}
	p := formatBashPreview(bashOutInput{Stdout: s, InlineLimit: 100})
	if p.SourceOmitted != 10 || p.StdoutOmitted <= 10 || p.Complete {
		t.Fatalf("%+v", p)
	}
	kept := strings.Count(p.Text, "A") + strings.Count(p.Text, "B")
	if p.StdoutOmitted != int64(210-kept) {
		t.Fatalf("omitted %d, kept %d", p.StdoutOmitted, kept)
	}
}

func TestBashPreviewNonzeroStderrPriority(t *testing.T) {
	stdout := strings.Repeat("Q", 1000)
	stderr := "boom: failed\n"
	p := formatBashPreview(bashOutInput{Stdout: bashOutS(stdout), Stderr: bashOutS(stderr), ExitCode: 2, InlineLimit: 200})
	if len(p.Text) != 200 || !strings.HasSuffix(p.Text, "[stderr]\n"+stderr) {
		t.Fatalf("len=%d text=%q", len(p.Text), p.Text)
	}
	if p.Complete || p.StderrOmitted != 0 || p.StdoutOmitted != int64(1000-strings.Count(p.Text, "Q")) {
		t.Fatalf("%+v", p)
	}
	// Large stderr takes the whole budget; stdout is fully omitted.
	big := strings.Repeat("e", 500)
	p = formatBashPreview(bashOutInput{Stdout: bashOutS("STDOUT"), Stderr: bashOutS(big), ExitCode: 1, InlineLimit: 100})
	if len(p.Text) > 100 || strings.Contains(p.Text, "STDOUT") || p.Complete || p.StdoutOmitted != 6 {
		t.Fatalf("len=%d %+v", len(p.Text), p)
	}
}

func TestBashPreviewSuccessCombinedCounts(t *testing.T) {
	for limit := 0; limit <= 700; limit++ {
		in := bashOutInput{Stdout: bashOutS(strings.Repeat("Q", 300)), Stderr: bashOutS(strings.Repeat("Z", 300)), InlineLimit: limit}
		p := formatBashPreview(in)
		if len(p.Text) > limit {
			t.Fatalf("limit %d: len %d", limit, len(p.Text))
		}
		q, z := strings.Count(p.Text, "Q"), strings.Count(p.Text, "Z")
		if p.StdoutOmitted != int64(300-q) || p.StderrOmitted != int64(300-z) {
			t.Fatalf("limit %d: counts %+v kept q=%d z=%d", limit, p, q, z)
		}
		if z > 0 && strings.Index(p.Text, "[stderr]\n") > strings.Index(p.Text, "Z") {
			t.Fatalf("limit %d: stderr unlabeled: %q", limit, p.Text)
		}
		if p.Complete != (q == 300 && z == 300) {
			t.Fatalf("limit %d: complete=%v q=%d z=%d", limit, p.Complete, q, z)
		}
		if !p.Complete && p.StdoutOmitted+p.StderrOmitted == 0 {
			t.Fatalf("limit %d: incomplete without omission", limit)
		}
	}
}

func TestBashPreviewBothGapsKeepsStderrLabel(t *testing.T) {
	in := bashOutInput{
		Stdout:      bashOutStream{Head: []byte("OH"), Tail: []byte("OT"), Omitted: 100},
		Stderr:      bashOutStream{Head: []byte("EH"), Tail: []byte("ET"), Omitted: 200},
		InlineLimit: 1000,
	}
	p := formatBashPreview(in)
	want := "OH\n[... 304 bytes omitted ...]\n[stderr]\nET"
	if p.Text != want {
		t.Fatalf("text %q want %q", p.Text, want)
	}
	// stdout kept OH of 104; stderr kept ET of 204; source gaps 300.
	if p.StdoutOmitted != 102 || p.StderrOmitted != 202 || p.SourceOmitted != 300 || p.Complete {
		t.Fatalf("%+v", p)
	}
}

func TestBashPreviewStdoutOnlyGapKeepsLabel(t *testing.T) {
	in := bashOutInput{
		Stdout:      bashOutStream{Head: []byte("OH"), Tail: []byte("OT"), Omitted: 50},
		Stderr:      bashOutS("err"),
		InlineLimit: 1000,
	}
	p := formatBashPreview(in)
	want := "OH\n[... 50 bytes omitted ...]\nOT\n[stderr]\nerr"
	if p.Text != want || p.StdoutOmitted != 50 || p.StderrOmitted != 0 {
		t.Fatalf("%q %+v", p.Text, p)
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
			if p.Complete || bashOutTotal(p) <= 0 {
				t.Errorf("limit %d code %d: must report omission: %+v", limit, code, p)
			}
		}
	}
	p := formatBashPreview(bashOutInput{Stdout: bashOutS("abcdefghij"), InlineLimit: 3})
	if p.Text != "" || p.StdoutOmitted != 10 {
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
		if p.StdoutOmitted != int64(len(s)-(len(p.Text)-len(bashOutMarker(p.StdoutOmitted)))) {
			t.Fatalf("limit %d: count %d", limit, p.StdoutOmitted)
		}
	}
}

func TestBashPreviewContiguousHeadTailSplitRune(t *testing.T) {
	// "日" is e6 97 a5; Head/Tail split it, Omitted is 0, so it is one rune.
	in := bashOutInput{Stdout: bashOutStream{Head: []byte("ab\xe6\x97"), Tail: []byte("\xa5cd")}, InlineLimit: 100}
	if p := formatBashPreview(in); p.Text != "ab日cd" || !p.Complete {
		t.Fatalf("%+v", p)
	}
	// Any limit-driven cut must not split the rune either.
	in.Stdout = bashOutStream{Head: []byte(strings.Repeat("x", 60) + "\xe6\x97"), Tail: []byte("\xa5" + strings.Repeat("y", 60))}
	for limit := 25; limit < 125; limit++ {
		in.InlineLimit = limit
		p := formatBashPreview(in)
		if len(p.Text) > limit || !utf8.ValidString(p.Text) {
			t.Fatalf("limit %d: len=%d valid=%v %q", limit, len(p.Text), utf8.ValidString(p.Text), p.Text)
		}
	}
}

func TestBashPreviewInvalidBytesPreserved(t *testing.T) {
	raw := "a\xff\xfeb\xc3"
	p := formatBashPreview(bashOutInput{Stdout: bashOutS(raw), InlineLimit: 100})
	if p.Text != raw || !p.Complete {
		t.Fatalf("raw bytes changed: %q %+v", p.Text, p)
	}
	a := formatBashArtifact(bashOutInput{Stdout: bashOutS(raw)})
	if a.Body != "[stdout]\n"+raw+"\n[stderr]\n" || !a.Complete {
		t.Fatalf("artifact raw bytes changed: %q", a.Body)
	}
	// Cuts among invalid bytes are made as is: exact byte accounting.
	bad := strings.Repeat("\xff", 100)
	p = formatBashPreview(bashOutInput{Stdout: bashOutS(bad), InlineLimit: 80})
	// The marker is sized for the full length (3 digits), so up to 1 byte of slack.
	if len(p.Text) < 79 || len(p.Text) > 80 || p.Complete {
		t.Fatalf("len=%d %+v", len(p.Text), p)
	}
	if kept := strings.Count(p.Text, "\xff"); p.StdoutOmitted != int64(100-kept) {
		t.Fatalf("omitted %d kept %d", p.StdoutOmitted, kept)
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
	want := "[stdout]\nh\n[... 99 bytes omitted ...]\nt\n[stderr]\ne"
	if a.Complete || a.Body != want || a.StdoutOmitted != 99 || a.StderrOmitted != 0 || a.SourceOmitted != 99 {
		t.Fatalf("%+v", a)
	}
}

func bashOutSections(t *testing.T, body string) (string, string) {
	t.Helper()
	i := strings.Index(body, "\n[stderr]\n")
	if !strings.HasPrefix(body, "[stdout]\n") || i < 0 {
		t.Fatalf("labels missing: %q", body)
	}
	return body[len("[stdout]\n"):i], body[i+len("\n[stderr]\n"):]
}

func TestBashArtifactFormatAllocation(t *testing.T) {
	big := strings.Repeat("x", 5000)
	overhead := len(bashOutStdoutLabel) + len(bashOutArtifactSep) + len(bashOutStderrLabel)
	limit := 1000 + overhead
	for _, tc := range []struct {
		name             string
		code             int
		out, err         string
		wantOut, wantErr int // exact section lengths
	}{
		{"success both big", 0, big, big, 700, 300},
		{"nonzero both big", 1, big, big, 300, 700},
		{"success small stderr redistributes", 0, big, "tiny", 996, 4},
		{"nonzero small stdout redistributes", 1, "tiny", big, 4, 996},
		{"success small stdout redistributes", 0, "tiny", big, 4, 996},
		{"nonzero small stderr redistributes", 1, big, "tiny", 996, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := formatBashArtifact(bashOutInput{Stdout: bashOutS(tc.out), Stderr: bashOutS(tc.err), ExitCode: tc.code, ArtifactLimit: limit})
			if len(a.Body) != limit || a.Complete {
				t.Fatalf("body %d limit %d complete=%v", len(a.Body), limit, a.Complete)
			}
			out, errS := bashOutSections(t, a.Body)
			if len(out) != tc.wantOut || len(errS) != tc.wantErr {
				t.Fatalf("sections out=%d err=%d want %d/%d", len(out), len(errS), tc.wantOut, tc.wantErr)
			}
			keptOut, keptErr := strings.Count(out, "x")+strings.Count(out, "tiny")*4, strings.Count(errS, "x")+strings.Count(errS, "tiny")*4
			if a.StdoutOmitted != int64(len(tc.out)-keptOut) || a.StderrOmitted != int64(len(tc.err)-keptErr) {
				t.Fatalf("counts %+v kept %d/%d", a, keptOut, keptErr)
			}
		})
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

func TestBashPreviewNoLabelReserveWithoutStderr(t *testing.T) {
	// Stdout is huge, stderr tiny at the tail: at limit 40 the label reserve
	// leaves no room for stderr, so the full limit must go to stdout.
	in := bashOutInput{Stdout: bashOutS(strings.Repeat("Q", 300)), Stderr: bashOutS(strings.Repeat("Z", 300)), InlineLimit: 40}
	p := formatBashPreview(in)
	if len(p.Text) > 40 || strings.Contains(p.Text, "[stderr]") && !strings.Contains(p.Text, "Z") {
		t.Fatalf("orphan label: %q", p.Text)
	}
	if p.StdoutOmitted != int64(300-strings.Count(p.Text, "Q")) || p.StderrOmitted != int64(300-strings.Count(p.Text, "Z")) {
		t.Fatalf("counts %+v for %q", p, p.Text)
	}
	// Sweep: whenever no Z is shown, stdout must use the capacity left by the
	// marker, i.e. the text is within one marker-digit byte of the limit.
	for limit := 31; limit <= 80; limit++ {
		in.InlineLimit = limit
		p := formatBashPreview(in)
		if strings.Contains(p.Text, "Z") {
			continue
		}
		if strings.Contains(p.Text, "[stderr]") {
			t.Fatalf("limit %d: orphan label %q", limit, p.Text)
		}
		if len(p.Text) < limit-1 {
			t.Fatalf("limit %d: unused capacity, len %d %q", limit, len(p.Text), p.Text)
		}
	}
}

func TestBashPreviewNonzeroNoOrphanLabel(t *testing.T) {
	for limit := 0; limit <= 60; limit++ {
		in := bashOutInput{Stdout: bashOutS(strings.Repeat("Q", 300)), Stderr: bashOutS(strings.Repeat("Z", 300)), ExitCode: 1, InlineLimit: limit}
		p := formatBashPreview(in)
		if len(p.Text) > limit {
			t.Fatalf("limit %d: len %d", limit, len(p.Text))
		}
		if strings.Contains(p.Text, "[stderr]\n") && !strings.Contains(p.Text, "Z") && !strings.HasSuffix(p.Text, "bytes omitted ...]\n") {
			t.Fatalf("limit %d: orphan label %q", limit, p.Text)
		}
		if p.StdoutOmitted != int64(300-strings.Count(p.Text, "Q")) || p.StderrOmitted != int64(300-strings.Count(p.Text, "Z")) {
			t.Fatalf("limit %d: counts %+v for %q", limit, p, p.Text)
		}
	}
	// Label alone (limit 9) must not appear; stdout gets the whole limit.
	p := formatBashPreview(bashOutInput{Stdout: bashOutS("abc"), Stderr: bashOutS("ERRORS"), ExitCode: 1, InlineLimit: 9})
	if p.Text != "abc" || p.StdoutOmitted != 0 || p.StderrOmitted != 6 || p.Complete {
		t.Fatalf("%q %+v", p.Text, p)
	}
}

func TestBashArtifactTinyLimitEmptyStreams(t *testing.T) {
	for limit := 1; limit < 19; limit++ {
		a := formatBashArtifact(bashOutInput{ArtifactLimit: limit})
		if a.Body != "" || a.Complete || a.StdoutOmitted != 0 || a.StderrOmitted != 0 {
			t.Fatalf("limit %d: %+v", limit, a)
		}
	}
	a := formatBashArtifact(bashOutInput{ArtifactLimit: 19})
	if a.Body != "[stdout]\n\n[stderr]\n" || !a.Complete {
		t.Fatalf("limit 19: %+v", a)
	}
}

func TestBashPreviewFourByteRuneBoundaries(t *testing.T) {
	const r = "\U0001F600" // f0 9f 98 80
	s := strings.Repeat("x", 7) + strings.Repeat(r, 20) + strings.Repeat("y", 7)
	// Contiguous Head/Tail split at every byte, including inside the rune.
	for split := 0; split <= len(s); split++ {
		in := bashOutStream{Head: []byte(s[:split]), Tail: []byte(s[split:])}
		if p := formatBashPreview(bashOutInput{Stdout: in, InlineLimit: 1000}); p.Text != s || !p.Complete {
			t.Fatalf("split %d: %q", split, p.Text)
		}
		for limit := 30; limit < 90; limit++ {
			p := formatBashPreview(bashOutInput{Stdout: in, InlineLimit: limit})
			if len(p.Text) > limit || !utf8.ValidString(p.Text) {
				t.Fatalf("split %d limit %d: len=%d valid=%v", split, limit, len(p.Text), utf8.ValidString(p.Text))
			}
		}
	}
}
