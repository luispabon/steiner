package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTruncationTailPriorityBashShape(t *testing.T) {
	raw := bashIngestionResult{
		ExitCode: 1,
		Output:   "HEAD-SENTINEL\n" + strings.Repeat("filler line\n", 900) + "\x1b[31mwarning: retry\x1b[0m\nwarning: retry\nwarning: retry\nfinal tail\n",
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw bash result: %v", err)
	}
	content := string(payload)
	got := shapeBashIngestedResult(content)

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
	if !strings.Contains(result.Message, "<truncated output shown=") {
		t.Fatalf("Message = %q, want truncation marker", result.Message)
	}
	if strings.Contains(result.Message, "bytes=") {
		t.Fatalf("Message = %q, want shown/total marker", result.Message)
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
	if !strings.Contains(got, "progress 2%") {
		t.Fatalf("stripToolNoise() = %q, want final progress line", got)
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
