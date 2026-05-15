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
		Message   string `json:"message"`
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
	if result.Message != "" {
		t.Fatalf("Message = %q, want empty", result.Message)
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
		Message   string `json:"message"`
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
	if !strings.Contains(result.Output, "warning: retry (repeated 3x)") {
		t.Fatalf("Output = %q, want warning collapse", result.Output)
	}

	shaped, _, shown, total := shapeToolText(raw.Output, defaultBashIngestionLimitBytes, tailPriorityStrategy)
	if result.Output != shaped {
		t.Fatalf("Output = %q, want shaped tail-priority output %q", result.Output, shaped)
	}
	wantMarker := truncationMarker(shown, total)
	if result.Message != wantMarker {
		t.Fatalf("Message = %q, want %q", result.Message, wantMarker)
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
	content := `{"matches":240,"returned":240,"output":"` + strings.Join(lines, `\n`) + `"}`

	got := shapeGrepIngestedResult(content)

	var result struct {
		Matches  int    `json:"matches"`
		Returned int    `json:"returned"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("unmarshal shaped grep result: %v", err)
	}
	if !strings.Contains(result.Output, "info: retrying (repeated 200x)") {
		t.Fatalf("Output = %q, want collapsed info summary", result.Output)
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
	if strings.Contains(got, "progress 2%") {
		t.Fatalf("stripToolNoise() = %q, want progress line stripped", got)
	}
}

func TestNoiseStripCollapsesDuplicateBlankAndInfoLines(t *testing.T) {
	got := stripToolNoise("alpha\n\n\nwarning: wait\nwarning: wait\nwarning: wait\ninfo: done\ninfo: done\nomega\n")
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("stripToolNoise() = %q, want blank collapse", got)
	}
	if !strings.Contains(got, "warning: wait (repeated 3x)") {
		t.Fatalf("stripToolNoise() = %q, want warning collapse", got)
	}
	if !strings.Contains(got, "info: done (repeated 2x)") {
		t.Fatalf("stripToolNoise() = %q, want info collapse", got)
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

func TestNoiseStripRemovesProgressBarLines(t *testing.T) {
	input := "header\n[########>       ]\n[========>] 45%\nfooter\n"
	got := stripToolNoise(input)
	if strings.Contains(got, "[########>") {
		t.Fatalf("stripToolNoise() = %q, want progress bar removed", got)
	}
	if strings.Contains(got, "[========>] 45%") {
		t.Fatalf("stripToolNoise() = %q, want progress bar with pct removed", got)
	}
	if !strings.Contains(got, "header") {
		t.Fatalf("stripToolNoise() = %q, want header preserved", got)
	}
	if !strings.Contains(got, "footer") {
		t.Fatalf("stripToolNoise() = %q, want footer preserved", got)
	}
}

func TestNoiseStripRemovesPercentageOnlyLines(t *testing.T) {
	input := "start\n45%\n100%\nend\n"
	got := stripToolNoise(input)
	if strings.Contains(got, "45%") {
		t.Fatalf("stripToolNoise() = %q, want percentage line removed", got)
	}
	if strings.Contains(got, "100%") {
		t.Fatalf("stripToolNoise() = %q, want percentage line removed", got)
	}
	if !strings.Contains(got, "start") {
		t.Fatalf("stripToolNoise() = %q, want start preserved", got)
	}
	if !strings.Contains(got, "end") {
		t.Fatalf("stripToolNoise() = %q, want end preserved", got)
	}
}

func TestNoiseStripRemovesDownloadKeywordLines(t *testing.T) {
	input := "before\nDownloading package 45%\nReceiving objects: 67%\nExtracting archive (32%)\nafter\n"
	got := stripToolNoise(input)
	if strings.Contains(got, "Downloading") {
		t.Fatalf("stripToolNoise() = %q, want download line removed", got)
	}
	if strings.Contains(got, "Receiving objects") {
		t.Fatalf("stripToolNoise() = %q, want receiving line removed", got)
	}
	if strings.Contains(got, "Extracting") {
		t.Fatalf("stripToolNoise() = %q, want extracting line removed", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("stripToolNoise() = %q, want non-progress lines preserved", got)
	}
}

func TestNoiseStripRemovesSpinnerLines(t *testing.T) {
	input := "line1\n⠋\n\\\n|\n/\n-\nline2\n"
	got := stripToolNoise(input)
	if strings.Contains(got, "⠋") {
		t.Fatalf("stripToolNoise() = %q, want braille spinner removed", got)
	}
	if strings.Contains(got, "\n\\\n") {
		t.Fatalf("stripToolNoise() = %q, want ascii spinner removed", got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") {
		t.Fatalf("stripToolNoise() = %q, want non-spinner lines preserved", got)
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
