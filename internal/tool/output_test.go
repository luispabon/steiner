package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShapeIngestedToolResultBashPassesThroughExactly(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"exit_code": 1,
		"timed_out": true,
		"message":   "command timed out",
		"truncated": true,
		"output":    "  \x1b[31mred\x1b[0m\rspin\r\n\n\ttail  \n" + strings.Repeat("x", 20000),
	})
	if err != nil {
		t.Fatalf("marshal bash result: %v", err)
	}
	if got := ShapeIngestedToolResult("bash", string(payload)); got != string(payload) {
		t.Fatalf("ShapeIngestedToolResult(bash) changed the payload, want byte-for-byte pass-through")
	}
}

func TestShapeIngestedToolResultGrepPassesThrough(t *testing.T) {
	lines := make([]string, 0, 240)
	for i := 0; i < 205; i++ {
		lines = append(lines, "info: retrying")
	}
	for i := 0; i < 35; i++ {
		lines = append(lines, "\x1b[31mmatch line\x1b[0m")
	}
	lines = append(lines, "\x1b]8;;https://example.com\x07link\x1b]8;;\x07")
	payload, err := json.Marshal(struct {
		Matches    int    `json:"matches,omitempty"`
		Shown      int    `json:"shown,omitempty"`
		NextOffset int    `json:"next_offset,omitempty"`
		Output     string `json:"output"`
	}{Matches: 240, Shown: 199, NextOffset: 199, Output: strings.Join(lines, "\n")})
	if err != nil {
		t.Fatalf("marshal grep result: %v", err)
	}

	if got := ShapeIngestedToolResult("grep", string(payload)); got != string(payload) {
		t.Fatalf("ShapeIngestedToolResult(grep) changed the payload, want pass-through")
	}

	var result struct {
		Matches    int    `json:"matches"`
		Shown      int    `json:"shown"`
		NextOffset int    `json:"next_offset"`
		Output     string `json:"output"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("unmarshal grep result: %v", err)
	}
	if result.Matches != 240 || result.Shown != 199 || result.NextOffset != 199 {
		t.Fatalf("metadata = matches %d shown %d next_offset %d, want 240 199 199", result.Matches, result.Shown, result.NextOffset)
	}
	if strings.Count(result.Output, "info: retrying") != 205 {
		t.Fatalf("info lines = %d, want all 205 preserved", strings.Count(result.Output, "info: retrying"))
	}
	if !strings.Contains(result.Output, "\x1b[31mmatch line\x1b[0m") || !strings.Contains(result.Output, "\x1b]8;;https://example.com\x07link") {
		t.Fatalf("Output = %q, want source ANSI and OSC preserved", result.Output)
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
