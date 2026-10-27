package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBashShapePassesThroughUnderLimit(t *testing.T) {
	raw := bashIngestionResult{
		ExitCode: 0,
		Output:   "head\n" + strings.Repeat("a", 12*1024-6),
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw bash result: %v", err)
	}

	got := shapeBashIngestedResult(string(payload))

	var result struct {
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated"`
		Output    string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("unmarshal shaped bash result: %v", err)
	}
	if result.Truncated {
		t.Fatal("Truncated = true, want false")
	}
	if result.Output != raw.Output {
		t.Fatalf("Output length = %d, want %d", len(result.Output), len(raw.Output))
	}
}

func TestBashShapeTruncatesOverLimitWithTailPriority(t *testing.T) {
	head := "HEAD-SENTINEL\n"
	mid := strings.Repeat("filler line\n", 1200)
	tail := "\x1b[31mwarning: retry\x1b[0m\nwarning: retry\nwarning: retry\nfinal tail\n"
	raw := bashIngestionResult{
		ExitCode: 1,
		Output:   head + mid + tail,
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw bash result: %v", err)
	}

	got := shapeBashIngestedResult(string(payload))

	var result struct {
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated"`
		Output    string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("unmarshal shaped bash result: %v", err)
	}
	if !result.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if !strings.Contains(result.Output, "final tail") {
		t.Fatalf("Output = %q, want tail content", result.Output)
	}
	if strings.Contains(result.Output, "HEAD-SENTINEL") {
		t.Fatalf("Output = %q, want head content truncated", result.Output)
	}
	if strings.Contains(result.Output, "\x1b[") {
		t.Fatalf("Output = %q, want ANSI stripped", result.Output)
	}
	if got := strings.Count(result.Output, "warning: retry\n"); got != 3 {
		t.Fatalf("warning: retry lines = %d, want 3 preserved in %q", got, result.Output)
	}

	_, _, shown, total := shapeToolText(raw.Output, defaultBashIngestionLimitBytes, tailPriorityStrategy)
	wantMarker := TruncationMarker(shown, total)
	if !strings.Contains(result.Output, wantMarker) {
		t.Fatalf("Output = %q, want truncation marker %q", result.Output, wantMarker)
	}
	if shown >= total {
		t.Fatalf("shown = %d, total = %d, want truncation", shown, total)
	}
}

func TestTruncationCountCapGrepShape(t *testing.T) {
	lines := make([]string, 0, 240)
	for i := 0; i < 205; i++ {
		lines = append(lines, "info: retrying")
	}
	for i := 0; i < 35; i++ {
		lines = append(lines, "match line")
	}
	content := `{"output":"` + strings.Join(lines, `\n`) + `"}`

	got := shapeGrepIngestedResult(content)

	var result struct {
		Matches    int    `json:"matches,omitempty"`
		NextOffset int    `json:"next_offset,omitempty"`
		Output     string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("unmarshal shaped grep result: %v", err)
	}
	if got := strings.Count(result.Output, "info: retrying"); got != 200 {
		t.Fatalf("info lines = %d, want 200 preserved by count cap", got)
	}
	if !strings.Contains(result.Output, "<truncated output shown=") {
		t.Fatalf("Output = %q, want truncation marker", result.Output)
	}
	if strings.Contains(result.Output, "match line") {
		t.Fatalf("Output = %q, want tail lines truncated by count cap", result.Output)
	}
}

func TestTruncationHeadText(t *testing.T) {
	got, truncated := headText("alpha\nbeta\ngamma\n", 8)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if got != "alpha\nbe" {
		t.Fatalf("headText() = %q, want prefix", got)
	}
}

func TestNoiseStripRemovesANSIAndCarriageReturns(t *testing.T) {
	got := stripToolNoise("one\x1b[31m!\x1b[0m\rspinner\rfinal\nprogress 1%\rprogress 2%\n")
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("stripToolNoise() = %q, want ANSI removed", got)
	}
	if strings.Contains(got, "one!") {
		t.Fatalf("stripToolNoise() = %q, want carriage-return overwrite removed", got)
	}
	if !strings.Contains(got, "final") {
		t.Fatalf("stripToolNoise() = %q, want final overwrite line", got)
	}
	if !strings.Contains(got, "progress 2%") {
		t.Fatalf("stripToolNoise() = %q, want progress line preserved", got)
	}
}

func TestNoiseStripPreservesSemanticToolText(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"progress bar", "header\n[########>       ]\n[========>] 45%\nfooter\n"},
		{"percentage only lines", "start\n45%\n100%\nend\n"},
		{"download keyword lines", "before\nDownloading package 45%\nReceiving objects: 67%\nExtracting archive (32%)\nafter\n"},
		{"braille spinner", "line1\n⠋\nline2\n"},
		{"labeled ascii spinners", "before\n- compiling\n| compiling\n/ compiling\n\\ compiling\nafter\n"},
		{"bare dash items", "- first\n-\n- second\n"},
		{"blank lines", "alpha\n\n\nomega\n"},
		{"repeated warnings and info", "warning: wait\nwarning: wait\nwarning: wait\ninfo: done\ninfo: done\n"},
		{"diagnostics", "main.go:12:5: warning: unused variable x\nmain.go:12:5: warning: unused variable x\n  --> main.go:12:5\n"},
		{"diff whitespace", "@@ -1,3 +1,3 @@\n \n-\told\n+\tnew\n \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripToolNoise(tt.input); got != tt.input {
				t.Fatalf("stripToolNoise() = %q, want input preserved %q", got, tt.input)
			}
		})
	}
}

func TestNoiseStripLeavesReadContentUntouched(t *testing.T) {
	content := `{"path":"README.md","output":"alpha\n\nbeta\n"}`
	if got := ShapeIngestedToolResult("read", content); got != content {
		t.Fatalf("ShapeIngestedToolResult(read) = %q, want unchanged", got)
	}
}

func TestTruncationMarkerUsesShownAndTotal(t *testing.T) {
	capture := StreamCapture{
		Bytes:     128,
		Shown:     24,
		Truncated: true,
		Preview:   "tail",
	}
	got := capture.Summary()
	if !strings.Contains(got, "<truncated output shown=24 total=128>") {
		t.Fatalf("Summary() = %q, want shown/total marker", got)
	}
	if strings.Contains(got, "bytes=") {
		t.Fatalf("Summary() = %q, want no bytes= marker", got)
	}
}

func TestNoiseStripLeavesPercentageInContext(t *testing.T) {
	input := "coverage: 87.5% of statements\n75% of tests pass\n"
	got := stripToolNoise(input)
	if !strings.Contains(got, "coverage: 87.5% of statements") {
		t.Fatalf("stripToolNoise() = %q, want percentage in context preserved", got)
	}
	if !strings.Contains(got, "75% of tests pass") {
		t.Fatalf("stripToolNoise() = %q, want percentage at line start with context preserved", got)
	}
}
