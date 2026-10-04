package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// stubBgFormatClock pins nanoNow so twin models render identical elapsed
// times; advance moves the shared clock forward.
func stubBgFormatClock(t *testing.T) (advance func(time.Duration)) {
	t.Helper()
	original := nanoNow
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).UnixNano()
	nanoNow = func() int64 { return now }
	t.Cleanup(func() { nanoNow = original })
	return func(d time.Duration) { now += int64(d) }
}

func oracleViewportFormat(m *Model) string {
	bg := m.resolvedPalette().ContentBG
	return theme.PadLines(theme.WithBg(m.fmtBgCacheInput, bg), m.viewport.Width(), bg)
}

// bgFormatScenario holds the event-sequence state shared by both twins so
// each random op is decided once and applied identically to both.
type bgFormatScenario struct {
	r         *rand.Rand
	next      int
	openTools []string
	children  []string
}

// op picks one random transcript event and returns it as an applier.
func (s *bgFormatScenario) op() func(m *Model) {
	s.next++
	id := fmt.Sprintf("op_%d", s.next)
	switch s.r.IntN(12) {
	case 0:
		text := strings.Repeat("reply words ", 1+s.r.IntN(20)) + "\n\n`code` and **bold**\n\n- item\n- 世界 🎉\tok"
		return func(m *Model) {
			updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantMessageEvent(1, "assistant", text)})
		}
	case 1:
		s.openTools = append(s.openTools, id)
		return func(m *Model) {
			updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", id, map[string]any{"file_path": "/src/" + id + ".go"})})
		}
	case 2:
		if len(s.openTools) == 0 {
			return s.op()
		}
		call := s.openTools[0]
		s.openTools = s.openTools[1:]
		return func(m *Model) {
			updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "read", call, "package a\n\nfunc A() {}\n", nil)})
		}
	case 3:
		return func(m *Model) { updateModelDirect(m, chunk(1, "")) }
	case 4:
		s.children = append(s.children, id)
		return func(m *Model) {
			updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewDelegationStartedEvent(callOcc("call_"+id, id), "explore "+id, "", "explore"), id)})
		}
	case 5:
		if len(s.children) == 0 {
			return s.op()
		}
		child := s.children[s.r.IntN(len(s.children))]
		return func(m *Model) { updateModelDirect(m, chunk(1, child)) }
	case 6:
		if len(s.children) == 0 {
			return s.op()
		}
		child := s.children[0]
		s.children = s.children[1:]
		return func(m *Model) {
			updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
				DelegationOccurrence: callOcc("call_"+child, child), Status: "complete", TurnCount: 1, Output: "done",
			})})
		}
	case 7:
		k := s.r.IntN(2) * 6
		return func(m *Model) {
			if seg := toolSegFromEnd(m, k); seg != nil {
				m.handleToolCallClick(seg)
			}
		}
	case 8:
		width := []int{90, 120, 150}[s.r.IntN(3)]
		return func(m *Model) { updateModelDirect(m, tea.WindowSizeMsg{Width: width, Height: 40}) }
	case 9:
		bg := []string{"#101010", "#1e1e2e", "#fafafa"}[s.r.IntN(3)]
		return func(m *Model) { m.palette = theme.Palette{SidebarBG: "#050505", ContentBG: bg} }
	case 10:
		a, b := s.r.IntN(200), s.r.IntN(200)
		return func(m *Model) {
			lines := strings.Count(m.fmtBgCacheInput, "\n") + 1
			startLine, startAnchor := m.viewportSelectionEndpoint(a % lines)
			endLine, endAnchor := m.viewportSelectionEndpoint(b % lines)
			m.activeRegion = regionViewport
			m.selection = selectionState{
				start:       selectionPoint{line: startLine, col: 1},
				end:         selectionPoint{line: endLine, col: 3},
				startAnchor: startAnchor,
				endAnchor:   endAnchor,
			}
		}
	default:
		return func(m *Model) { updateModelDirect(m, tickMsg{}) }
	}
}

// TestSyncViewportIncrementalBgMatchesFullFormat drives an incrementally
// formatting model and a twin whose format cache is cleared before every sync
// through the same random event sequence. Both must equal the original
// PadLines(WithBg(...)) output byte for byte, and selection state must match.
func TestSyncViewportIncrementalBgMatchesFullFormat(t *testing.T) {
	advance := stubBgFormatClock(t)
	for seed := range uint64(8) {
		s := &bgFormatScenario{r: rand.New(rand.NewPCG(seed, 3))}
		inc := populateLongTranscript(newContentBenchModel(), 6)
		full := populateLongTranscript(newContentBenchModel(), 6)
		for step := range 35 {
			advance(time.Duration(s.r.IntN(3)) * time.Second)
			apply := s.op()
			apply(inc)
			apply(full)
			inc.syncViewport()
			full.bgFormat = bgFormatCache{}
			full.syncViewport()

			if inc.bgFormat.source != inc.fmtBgCacheInput {
				t.Fatalf("seed %d step %d: format source diverged from fmtBgCacheInput", seed, step)
			}
			if got, want := inc.bgFormat.output, oracleViewportFormat(inc); got != want {
				t.Fatalf("seed %d step %d: incremental output differs from PadLines(WithBg)\n got %q\nwant %q", seed, step, got, want)
			}
			if inc.bgFormat.output != full.bgFormat.output || inc.fmtBgCacheInput != full.fmtBgCacheInput {
				t.Fatalf("seed %d step %d: incremental and full-format twins diverged", seed, step)
			}
			if inc.selection != full.selection || inc.activeRegion != full.activeRegion {
				t.Fatalf("seed %d step %d: selection %+v (region %v), full path %+v (region %v)",
					seed, step, inc.selection, inc.activeRegion, full.selection, full.activeRegion)
			}
			if !slices.Equal(inc.viewport.Lines(), full.viewport.Lines()) || inc.viewport.YOffset() != full.viewport.YOffset() {
				t.Fatalf("seed %d step %d: viewport views differ", seed, step)
			}
		}
	}
}

// TestSyncViewportBgFormatBoundsWork poisons the formatted settled lines after
// one tick and checks that the next tick of a running card keeps them: only
// the changed tail, under 10% of the transcript, is fed to the formatter.
func TestSyncViewportBgFormatBoundsWork(t *testing.T) {
	advance := stubBgFormatClock(t)
	m := populateLongTranscript(newContentBenchModel(), 25)
	startInflightChildren(m, 1)
	updateModelDirect(m, tickMsg{})
	m.syncViewport()
	settled := m.fmtBgCacheInput
	if lines := strings.Count(settled, "\n") + 1; lines < 700 {
		t.Fatalf("setup: transcript has %d lines, want >= 700", lines)
	}

	const marker = "POISONED"
	poisoned := strings.Split(m.bgFormat.output, "\n")
	for i := range poisoned {
		poisoned[i] = marker
	}
	m.bgFormat.output = strings.Join(poisoned, "\n")

	advance(2 * time.Second)
	updateModelDirect(m, tickMsg{})
	m.syncViewport()
	if m.fmtBgCacheInput == settled {
		t.Fatal("setup: second tick did not change the transcript")
	}

	got := strings.Split(m.bgFormat.output, "\n")
	want := strings.Split(oracleViewportFormat(m), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	reused := 0
	for reused < len(got) && got[reused] == marker {
		reused++
	}
	for i := reused; i < len(got); i++ {
		if got[i] != want[i] {
			t.Fatalf("line %d after the reused prefix = %q, want %q", i, got[i], want[i])
		}
	}
	source := m.fmtBgCacheInput
	reusedBytes := 0
	for range reused {
		reusedBytes += strings.IndexByte(source[reusedBytes:], '\n') + 1
	}
	if fresh := len(source) - reusedBytes; fresh*10 >= len(source) {
		t.Fatalf("formatter fed %d of %d bytes (%d of %d lines reused), want < 10%%", fresh, len(source), reused, len(got))
	}
}
