package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBashResultAlwaysEmitsExitCode(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(&BashResult{ArtifactStatus: bashArtifactNone})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"exit_code":0`) {
		t.Errorf("JSON %s lacks exit_code 0", data)
	}
}

func TestBashArtifactReadBackLimitations(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	readDef := NewReadTool(Env{WorkDir: dir, PathPolicy: &policy})
	read := func(path string) *ReadResult {
		t.Helper()
		resI, err := readDef.Handler(context.Background(), map[string]any{"path": path})
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		switch r := resI.(type) {
		case *ReadResult:
			return r
		case ReadResult:
			return &r
		}
		t.Fatalf("read result type %T", resI)
		return nil
	}

	bin := runBashInDir(t, dir, map[string]any{"command": "printf 'a\\0b\\n'; seq 1 5000", "max_output_bytes": 100, "raw_output": true})
	if bin.FilePath == "" || !strings.Contains(bin.Message, "NUL") || !strings.Contains(bin.Message, "binary") {
		t.Fatalf("binary artifact: path=%q message=%q", bin.FilePath, bin.Message)
	}
	if got := read(bin.FilePath); !strings.Contains(got.Output, "cannot read binary file") || got.Path != "" {
		t.Errorf("read of NUL artifact = %+v, want binary rejection", got)
	}

	long := runBashInDir(t, dir, map[string]any{"command": "head -c 6000 /dev/zero | tr '\\0' x; echo; seq 1 5000", "max_output_bytes": 100})
	if long.FilePath == "" || !strings.Contains(long.Message, "clips lines longer than") {
		t.Fatalf("long-line artifact: path=%q message=%q", long.FilePath, long.Message)
	}
	got := read(long.FilePath)
	first, _, _ := strings.Cut(strings.TrimPrefix(got.Output, "[stdout]\n"), "\n")
	if len(first) >= 6000 {
		t.Errorf("long line not clipped: %d bytes", len(first))
	}
}

func TestBashNoticeKeptWithinBudget(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	const budget = 400
	res := runBashInDir(t, t.TempDir(), map[string]any{"command": "seq 1 5000; sleep 30", "timeout_seconds": 1, "max_output_bytes": budget})
	if !res.TimedOut || len(res.Output) > budget {
		t.Fatalf("timedOut=%v len=%d", res.TimedOut, len(res.Output))
	}
	if !strings.HasSuffix(res.Output, "output above is partial]") || !strings.Contains(res.Output, "deadline exceeded") {
		t.Errorf("notice missing from output: %q", res.Output)
	}
}

func TestBashMessageUnitsCleanVersusRaw(t *testing.T) {
	t.Parallel()
	skipUnlessUnix(t)
	cmd := "yes x | head -c 5000000"
	clean := runBashInDir(t, t.TempDir(), map[string]any{"command": cmd, "max_output_bytes": 500})
	for _, want := range []string{"raw source bytes (rendered size unknown)", "capture gap", "rendered bytes"} {
		if !strings.Contains(clean.Message, want) {
			t.Errorf("clean message lacks %q: %q", want, clean.Message)
		}
	}
	if strings.Contains(clean.Message, "total") {
		t.Errorf("clean message sums incompatible units: %q", clean.Message)
	}
	raw := runBashInDir(t, t.TempDir(), map[string]any{"command": cmd, "max_output_bytes": 500, "raw_output": true})
	if strings.Contains(raw.Message, "raw source") || !strings.Contains(raw.Message, "capture gap") {
		t.Errorf("raw message = %q", raw.Message)
	}
}

func TestBashStreamForFormatBoundsCleanedExpansion(t *testing.T) {
	t.Parallel()
	// Each lone ESC cleans to a much longer malformed-escape label.
	head := []byte(strings.Repeat("\x1b", 1<<18))
	tail := []byte("é END\n")
	c := streamCapture{Head: head, Tail: tail, TotalBytes: int64(len(head) + len(tail))}
	s, loss := bashStreamForFormat(c, false)
	if len(s.Head) > bashArtifactMaxBytes || len(s.Tail) > bashArtifactMaxBytes {
		t.Fatalf("retained head=%d tail=%d, want <= %d each", len(s.Head), len(s.Tail), bashArtifactMaxBytes)
	}
	if s.Omitted <= 0 || s.SourceOmitted != 0 || loss != (bashStreamLoss{}) {
		t.Errorf("Omitted=%d SourceOmitted=%d loss=%+v: want rendered omission only", s.Omitted, s.SourceOmitted, loss)
	}
	if !strings.HasSuffix(string(s.Tail), "é END\n") || !utf8.Valid(s.Head) || !utf8.Valid(s.Tail) {
		t.Errorf("tail %q or UTF-8 boundaries wrong", s.Tail[max(0, len(s.Tail)-20):])
	}
}

func TestBashStreamForFormatCaptureGapUnits(t *testing.T) {
	t.Parallel()
	c := streamCapture{Head: []byte("head\n"), Tail: []byte("\ntail\n"), TotalBytes: 1000}
	s, loss := bashStreamForFormat(c, false)
	gap := int64(1000 - len(c.Head) - len(c.Tail))
	if s.SourceOmitted != loss.gap+loss.skipped || loss.gap != gap || s.Omitted != 0 {
		t.Errorf("clean: stream %+v loss %+v", s, loss)
	}
	s, _ = bashStreamForFormat(c, true)
	if s.Omitted != gap || s.SourceOmitted != 0 {
		t.Errorf("raw: Omitted=%d SourceOmitted=%d, want %d, 0", s.Omitted, s.SourceOmitted, gap)
	}
}

func TestBashFormatterSourceOmissionUnits(t *testing.T) {
	t.Parallel()
	src := bashOutStream{Head: []byte("HEAD"), Tail: []byte("TAIL"), SourceOmitted: 500}
	p := formatBashPreview(bashOutInput{Stdout: src, InlineLimit: 1000})
	want := "HEAD\n[... 0 bytes and 500 raw source bytes omitted ...]\nTAIL"
	if p.Complete || p.Text != want || p.StdoutOmitted != 0 || p.StdoutSource != 500 || p.SourceOmitted != 500 {
		t.Errorf("preview %+v", p)
	}
	mixed := bashOutStream{Head: []byte("HEAD"), Tail: []byte("TAIL"), Omitted: 10, SourceOmitted: 20}
	p = formatBashPreview(bashOutInput{Stdout: mixed, Stderr: bashOutS("e"), InlineLimit: 1000})
	if p.Complete || p.StdoutOmitted != 10 || p.StdoutSource != 20 || p.StderrSource != 0 || !strings.Contains(p.Text, "10 bytes and 20 raw source bytes omitted") {
		t.Errorf("mixed preview %+v", p)
	}
	a := formatBashArtifact(bashOutInput{Stdout: bashOutS("o"), Stderr: src})
	if a.Complete || a.StderrSource != 500 || a.StderrOmitted != 0 || a.SourceOmitted != 500 {
		t.Errorf("artifact %+v", a)
	}
}
