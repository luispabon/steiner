package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// BenchmarkDelegationSegmentRender: one active, expanded delegation card with a
// collapsed ~4 KiB prompt and a 100-entry child transcript whose rows far exceed
// maxDelegationTranscriptRows, so every render exercises the transcript cut and
// the prompt preview. Width 100, no body-line cap.
func BenchmarkDelegationSegmentRender(b *testing.B) {
	buf := &contentBuffer{styles: testStyles(theme.AccentAmber), collapseState: make(map[int]bool)}
	seg := contentSegment{kind: segmentDelegation, delegData: newDelegationRenderBenchState()}

	out := buf.renderDelegationSegment(seg, 100)
	if !strings.Contains(out, "[old child events hidden]") {
		b.Fatalf("rendered card missing transcript marker: %q", out)
	}
	if !strings.Contains(out, "▸ prompt") || !strings.Contains(out, "Investigate the delegation render path") {
		b.Fatalf("rendered card missing collapsed prompt preview: %q", out)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = buf.renderDelegationSegment(seg, 100)
	}
}

func newDelegationRenderBenchState() *delegationDisplayState {
	return &delegationDisplayState{
		agentID:         "child-bench",
		agentType:       "explore",
		toolLabel:       "explore",
		taskPreview:     "render delegation transcript",
		promptText:      delegationRenderBenchPrompt(),
		promptCollapsed: true,
		status:          "active",
		startTime:       time.Now().UnixNano(),
		entries:         delegationRenderBenchEntries(),
	}
}

func delegationRenderBenchPrompt() string {
	return strings.Repeat("Investigate the delegation render path: read the transcript builder, trace each entry, and report ordering problems with file and line references.\n", 24)
}

// delegationRenderBenchEntries mixes thinking entries, tool entries, and
// multi-line assistant messages of 5 to 20 lines. Some assistant lines are
// non-ASCII and some carry ANSI escapes.
func delegationRenderBenchEntries() []delegationTranscriptEntry {
	entries := make([]delegationTranscriptEntry, 0, 100)
	for i := range 100 {
		switch {
		case i%5 == 3:
			entries = append(entries, delegationTranscriptEntry{
				kind: delegationTranscriptEntryThinking,
				body: "considering option " + strconv.Itoa(i) + ": weigh the trade-offs before acting.",
			})
		case i%4 == 1:
			entries = append(entries, delegationTranscriptEntry{
				kind:   delegationTranscriptEntryTool,
				tool:   "read",
				args:   fmt.Sprintf("/src/pkg%d/file.go", i),
				status: "complete",
			})
		default:
			entries = append(entries, delegationTranscriptEntry{
				kind: delegationTranscriptEntryAssistant,
				body: delegationRenderBenchAssistantBody(i, 5+i%16),
			})
		}
	}
	return entries
}

func delegationRenderBenchAssistantBody(i, lines int) string {
	var sb strings.Builder
	for l := range lines {
		line := fmt.Sprintf("finding %d.%d: the parser handles nested blocks and returns early on empty input, see the caller.", i, l)
		if i%9 == 0 {
			line = fmt.Sprintf("ü é ñ finding %d.%d: résumé handling checks naïve edge cases in the parser.", i, l)
		}
		if i%11 == 0 && l == 0 {
			line = "\x1b[1m" + line + "\x1b[0m"
		}
		if l > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)
	}
	return sb.String()
}
