package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// oldPipelineLines is the pre-WI-8e viewport pipeline, kept verbatim as the
// oracle: format the joined transcript in full, prepend pad rows, then split
// as scrollModel.SetContent did ("\r\n" normalised, a single zero-width line
// collapsed). It returns the scroll model lines and the top pad.
func oldPipelineLines(rendered string, width, height int, bg, padLine string) ([]string, int) {
	rendered = theme.PadLines(theme.WithBg(rendered, bg), width, bg)
	contentLines := strings.Count(rendered, "\n") + 1
	pad := max(height-contentLines, 0)
	if pad > 0 {
		rendered = strings.Repeat(padLine+"\n", pad) + rendered
	}
	if strings.ContainsRune(rendered, '\r') {
		rendered = strings.ReplaceAll(rendered, "\r\n", "\n")
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) == 1 && ansi.StringWidth(lines[0]) == 0 {
		lines = nil
	}
	return lines, pad
}

// oracleViewportLines returns the old pipeline's lines for m's current
// transcript.
func oracleViewportLines(m *Model) ([]string, int) {
	return oldPipelineLines(m.bgFormat.source(), m.viewport.Width(), m.viewport.Height(), m.resolvedPalette().ContentBG, m.padLineCacheRendered)
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
// Event messages are constructed once, outside the applier.
func (s *bgFormatScenario) op() func(m *Model) {
	s.next++
	id := fmt.Sprintf("op_%d", s.next)
	switch s.r.IntN(15) {
	case 0:
		text := strings.Repeat("reply words ", 1+s.r.IntN(20)) + "\n\n`code` and **bold**\n\n- item\n- 世界 🎉\tok"
		return sendMsg(runtimeEventMsg{Event: output.NewAssistantMessageEvent(1, "assistant", text)})
	case 1:
		s.openTools = append(s.openTools, id)
		return sendMsg(runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", id, map[string]any{"file_path": "/src/" + id + ".go"})})
	case 2:
		if len(s.openTools) == 0 {
			return s.op()
		}
		call := s.openTools[0]
		s.openTools = s.openTools[1:]
		return sendMsg(runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "read", call, "package a\n\nfunc A() {}\n", nil)})
	case 3:
		return sendMsg(chunk(1, ""))
	case 4:
		s.children = append(s.children, id)
		return sendMsg(runtimeEventMsg{Event: output.WithAgentScope(output.NewDelegationStartedEvent(callOcc("call_"+id, id), "explore "+id, "", "explore"), id)})
	case 5:
		if len(s.children) == 0 {
			return s.op()
		}
		child := s.children[s.r.IntN(len(s.children))]
		return sendMsg(chunk(1, child))
	case 6:
		if len(s.children) == 0 {
			return s.op()
		}
		child := s.children[0]
		s.children = s.children[1:]
		return sendMsg(runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
			DelegationOccurrence: callOcc("call_"+child, child), Status: "complete", TurnCount: 1, Output: "done",
		})})
	case 7:
		k := s.r.IntN(2) * 6
		return func(m *Model) {
			if seg := toolSegFromEnd(m, k); seg != nil {
				m.handleToolCallClick(seg)
			}
		}
	case 8:
		width := []int{90, 120, 150}[s.r.IntN(3)]
		height := []int{12, 40, 120}[s.r.IntN(3)]
		return resizeNow(width, height)
	case 9:
		bg := []string{"#101010", "#1e1e2e", "#fafafa"}[s.r.IntN(3)]
		return func(m *Model) { m.palette = theme.Palette{SidebarBG: "#050505", ContentBG: bg} }
	case 10:
		a, b := s.r.IntN(200), s.r.IntN(200)
		return func(m *Model) {
			lines := strings.Count(m.bgFormat.source(), "\n") + 1
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
	case 11:
		return sendMsg(runtimeEventMsg{Event: output.NewUserInputEvent("look at "+id, "interactive", nil)})
	case 12:
		return sendMsg(runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "weighing "+id+". ", output.ChunkSourceAssistant)})
	case 13:
		return sendMsg(toggleThinkingMsg{})
	default:
		return sendMsg(tickMsg{})
	}
}

// sendMsg returns an applier that feeds msg, built once, to a model; events
// carry wall-clock timestamps, so both twins must receive the same value.
func sendMsg(msg tea.Msg) func(m *Model) {
	return func(m *Model) { updateModelDirect(m, msg) }
}

func newBgFormatTwinModel(run func(m *Model)) *Model {
	m := newModel(Config{Model: "bench-model", ModelContexts: map[string]int{"bench-model": 100000}}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	run(m)
	return m
}

// TestSyncViewportIncrementalLinesMatchOldPipeline drives an incrementally
// formatting model and an oracle twin through the same random event sequence.
// After every sync the twin's viewport lines are replaced with the old
// join -> format -> pad -> split pipeline's output for its transcript, so the
// two models must agree on lines, pad, scroll offset, selection and the whole
// rendered frame.
func TestSyncViewportIncrementalLinesMatchOldPipeline(t *testing.T) {
	advance := stubBgFormatClock(t)
	for seed := range uint64(8) {
		s := &bgFormatScenario{r: rand.New(rand.NewPCG(seed, 3))}
		run := sendMsg(runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
		inc, oracle := newBgFormatTwinModel(run), newBgFormatTwinModel(run)
		for step := range 60 {
			advance(time.Duration(s.r.IntN(3)) * time.Second)
			apply := s.op()
			apply(inc)
			apply(oracle)
			inc.syncViewport()
			oracle.syncViewport()
			want, wantPad := oracleViewportLines(oracle)
			oracle.setViewportLines(want)
			if oracle.autoScroll {
				oracle.viewport.GotoBottom()
			}

			if inc.bgFormat.source() != oracle.bgFormat.source() {
				t.Fatalf("seed %d step %d: transcripts diverged", seed, step)
			}
			if got := inc.viewport.Lines(); !slices.Equal(got, want) || (got == nil) != (want == nil) {
				t.Fatalf("seed %d step %d: lines differ from the old pipeline\n got %q\nwant %q", seed, step, got, want)
			}
			if inc.contentTopPad != wantPad || inc.viewport.YOffset() != oracle.viewport.YOffset() {
				t.Fatalf("seed %d step %d: pad %d offset %d, want pad %d offset %d",
					seed, step, inc.contentTopPad, inc.viewport.YOffset(), wantPad, oracle.viewport.YOffset())
			}
			if inc.selection != oracle.selection || inc.activeRegion != oracle.activeRegion {
				t.Fatalf("seed %d step %d: selection %+v (region %v), oracle %+v (region %v)",
					seed, step, inc.selection, inc.activeRegion, oracle.selection, oracle.activeRegion)
			}
			if got, want := inc.View().Content, oracle.View().Content; got != want {
				t.Fatalf("seed %d step %d: rendered frames differ\n got %q\nwant %q", seed, step, got, want)
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
	settled := m.bgFormat.source()
	if lines := strings.Count(settled, "\n") + 1; lines < 700 {
		t.Fatalf("setup: transcript has %d lines, want >= 700", lines)
	}

	const marker = "POISONED"
	for i := range m.bgFormat.lines {
		m.bgFormat.lines[i] = marker
	}

	advance(2 * time.Second)
	updateModelDirect(m, tickMsg{})
	m.syncViewport()
	if m.bgFormat.source() == settled {
		t.Fatal("setup: second tick did not change the transcript")
	}

	got := m.bgFormat.lines
	want, _ := oldPipelineLines(m.bgFormat.source(), m.viewport.Width(), 0, m.resolvedPalette().ContentBG, "")
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
	source := m.bgFormat.source()
	reusedBytes := 0
	for range reused {
		reusedBytes += strings.IndexByte(source[reusedBytes:], '\n') + 1
	}
	if fresh := len(source) - reusedBytes; fresh*10 >= len(source) {
		t.Fatalf("formatter fed %d of %d bytes (%d of %d lines reused), want < 10%%", fresh, len(source), reused, len(got))
	}
}
