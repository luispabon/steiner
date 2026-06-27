package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// newTestBuffer returns a minimal contentBuffer suitable for rendering tests.
func newTestBuffer(t *testing.T) *contentBuffer {
	t.Helper()
	lipgloss.SetColorProfile(termenv.Ascii)
	styles := theme.BuildStyles("#5599ff")
	return &contentBuffer{
		styles:   styles,
		segments: make([]contentSegment, 0),
	}
}

func TestCompactionBoxCollapsedInProgress(t *testing.T) {
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		subtitle:     "summarizing context",
		finished:     false,
		startTime:    0, // no wall clock in test
		spinnerFrame: 3,
	}

	out := b.renderCompactionBanner(cd, 80)

	// Must end with newline.
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output does not end with newline")
	}
	// Must contain the tag.
	if !strings.Contains(out, "compaction") {
		t.Errorf("output missing 'compaction' tag: %q", out)
	}
	// Must contain a spinner character (braille).
	hasSpinner := false
	for _, frame := range spinnerFrames {
		if strings.Contains(out, frame) {
			hasSpinner = true
			break
		}
	}
	if !hasSpinner {
		t.Errorf("collapsed in-progress output missing spinner: %q", out)
	}
	// Box is now non-uncollapsable: no disclosure triangle in either form.
	if strings.Contains(out, "▸") {
		t.Errorf("output should not contain '▸' disclosure triangle: %q", out)
	}
	if strings.Contains(out, "▾") {
		t.Errorf("output should not contain '▾' disclosure triangle: %q", out)
	}
}

func TestCompactionBoxCollapsedFinished(t *testing.T) {
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		subtitle:        "summarizing context",
		finished:        true,
		elapsed:         "3s",
		compactionCount: 2,
	}

	out := b.renderCompactionBanner(cd, 80)

	if !strings.Contains(out, "compaction") {
		t.Errorf("output missing 'compaction' tag: %q", out)
	}
	// Must show success checkmark.
	if !strings.Contains(out, "✓") {
		t.Errorf("finished collapsed output missing '✓': %q", out)
	}
	// Must show elapsed.
	if !strings.Contains(out, "3s") {
		t.Errorf("finished collapsed output missing elapsed '3s': %q", out)
	}
	// Must show count.
	if !strings.Contains(out, "#2") {
		t.Errorf("finished collapsed output missing '#2': %q", out)
	}
	// Collapsed: detail rows must NOT appear.
	if strings.Contains(out, "Compacted") {
		t.Errorf("collapsed output should not contain detail rows: %q", out)
	}
	// Box is now non-uncollapsable: no disclosure triangle in either form.
	if strings.Contains(out, "▸") {
		t.Errorf("output should not contain '▸' disclosure triangle: %q", out)
	}
	if strings.Contains(out, "▾") {
		t.Errorf("output should not contain '▾' disclosure triangle: %q", out)
	}
}

func TestCompactionBoxUsesNilGuard(t *testing.T) {
	b := newTestBuffer(t)
	seg := contentSegment{compactionData: nil}
	out := b.renderCompactionBannerSegment(seg, 80)
	if out != "" {
		t.Errorf("nil compactionData should produce empty string, got %q", out)
	}
}

func TestRenderDelegationHeaderAdvisor(t *testing.T) {
	b := newTestBuffer(t)
	header := b.renderDelegationHeader(&delegationDisplayState{
		isAdvisor:      true,
		toolLabel:      "advisor",
		status:         "budget_exhausted",
		modelName:      "advisor-model",
		advisorUse:     2,
		advisorMaxUses: 2,
		collapsed:      true,
	}, 80)

	if !strings.Contains(header, "advisor") {
		t.Fatalf("header = %q, want advisor label", header)
	}
	if strings.Contains(header, "pending") {
		t.Fatalf("header = %q, must not show pending agent id", header)
	}
	if !strings.Contains(header, "budget exhausted") {
		t.Fatalf("header = %q, want budget status", header)
	}
}

func TestDelegationRowsStylePromptBodyDifferentlyAndInsertSeparator(t *testing.T) {
	b := newTestBuffer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(termenv.Ascii)
	})
	dd := &delegationDisplayState{
		promptText:      "same text",
		promptCollapsed: false,
		entries: []delegationTranscriptEntry{
			{kind: delegationTranscriptEntryAssistant, body: "same text"},
		},
	}

	rows := b.delegationRows(dd, 80)
	promptIndex := -1
	transcriptIndex := -1
	separatorIndex := -1
	for i, row := range rows {
		switch row.kind {
		case delegationRowPromptBody:
			if promptIndex < 0 {
				promptIndex = i
			}
		case delegationRowTranscript:
			if transcriptIndex < 0 {
				transcriptIndex = i
			}
		case delegationRowSeparator:
			separatorIndex = i
		}
	}

	if promptIndex < 0 {
		t.Fatalf("rows = %#v, want prompt body row", rows)
	}
	if transcriptIndex < 0 {
		t.Fatalf("rows = %#v, want transcript row", rows)
	}
	if separatorIndex < 0 {
		t.Fatalf("rows = %#v, want separator row", rows)
	}
	if promptIndex >= separatorIndex || separatorIndex >= transcriptIndex {
		t.Fatalf("row order = prompt:%d separator:%d transcript:%d, want prompt < separator < transcript", promptIndex, separatorIndex, transcriptIndex)
	}
	if got, want := stripANSI(rows[promptIndex].text), stripANSI(rows[transcriptIndex].text); got != want {
		t.Fatalf("prompt body text = %q, transcript text = %q, want same plain text", got, want)
	}
	if !strings.Contains(rows[promptIndex].text, "\x1b[3;") {
		t.Fatalf("prompt body row = %q, want italic ANSI", rows[promptIndex].text)
	}
	if strings.Contains(rows[transcriptIndex].text, "\x1b[3;") {
		t.Fatalf("transcript row = %q, want non-italic ANSI", rows[transcriptIndex].text)
	}
}

func TestDelegationRowsOmitSeparatorWhenCollapsed(t *testing.T) {
	b := newTestBuffer(t)
	cases := []struct {
		name string
		dd   *delegationDisplayState
	}{
		{
			name: "prompt collapsed",
			dd: &delegationDisplayState{
				promptText:      "same text",
				promptCollapsed: true,
				entries: []delegationTranscriptEntry{
					{kind: delegationTranscriptEntryAssistant, body: "same text"},
				},
			},
		},
		{
			name: "delegation collapsed",
			dd: &delegationDisplayState{
				promptText:      "same text",
				promptCollapsed: false,
				collapsed:       true,
				entries: []delegationTranscriptEntry{
					{kind: delegationTranscriptEntryAssistant, body: "same text"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := b.delegationRows(tc.dd, 80)
			for _, row := range rows {
				if row.kind == delegationRowSeparator {
					t.Fatalf("rows = %#v, want no separator row", rows)
				}
			}
		})
	}
}

func TestRenderClosingSeparatorHasBlankLineMargin(t *testing.T) {
	b := newTestBuffer(t)
	b.appendLabeledBlock("Compaction", "summary text")

	out := b.String(80)
	plain := stripANSI(out)

	// The closing delimiter should be preceded by a blank line.
	lines := strings.Split(plain, "\n")
	var bodyIndex int
	for i, line := range lines {
		if strings.TrimSpace(line) == "summary text" {
			bodyIndex = i
			break
		}
	}
	if bodyIndex == 0 {
		t.Fatalf("body line not found in output:\n%s", plain)
	}
	if bodyIndex+1 >= len(lines) || strings.TrimSpace(lines[bodyIndex+1]) != "" {
		t.Errorf("expected blank line immediately after body, got %q:\n%s", lines[bodyIndex+1], plain)
	}
	if bodyIndex+2 >= len(lines) || !strings.Contains(lines[bodyIndex+2], "End of Compaction") {
		t.Errorf("expected closing delimiter after blank line:\n%s", plain)
	}
}
