package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// groupDiffStep applies one scripted event or state change to the model.
type groupDiffStep struct {
	name  string
	apply func(m *Model) *Model
}

// setMixedEntryCollapsed sets one card's collapse state through the same
// dirtying path the toggle uses.
func setMixedEntryCollapsed(m *Model, agentID string, collapsed bool) {
	for _, dd := range mixedEntries(m) {
		if dd.agentID == agentID {
			dd.collapsed = collapsed
			m.content.dirtyDelegationCard(dd)
		}
	}
}

// groupDiffScript covers tick, progress, transcript, completion, failure,
// expand and collapse, width, theme, jump flash and a late group
// label. Each step is replayed from scratch to build the reference render.
func groupDiffScript() []groupDiffStep {
	complete := func(m *Model, callID, agentID, status string) *Model {
		return updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
			DelegationOccurrence: callOcc(callID, agentID), Status: status, TurnCount: 3, TokenCount: 900, ToolCallCount: 4,
			Output: "finding about the module",
		})})
	}
	return []groupDiffStep{
		{"transcript", func(m *Model) *Model { return populateLongTranscript(m, 6) }},
		{"group start", func(m *Model) *Model {
			for j := 0; j < 3; j++ {
				m = appendMixedChild(m, j, false)
			}
			return m
		}},
		{"progress", func(m *Model) *Model { return appendMixedChildWork(m, "child-1", "call_child-1") }},
		{"tick", func(m *Model) *Model {
			m.content.AdvanceDelegationSpinners()
			m.content.AdvanceDelegationSpinners()
			return m
		}},
		{"complete", func(m *Model) *Model { return complete(m, "call_child-0", "child-0", "complete") }},
		{"failed", func(m *Model) *Model { return complete(m, "call_child-2", "child-2", "failed") }},
		{"expand settled", func(m *Model) *Model {
			setMixedEntryCollapsed(m, "child-0", false)
			return m
		}},
		{"collapse settled", func(m *Model) *Model {
			setMixedEntryCollapsed(m, "child-0", true)
			return m
		}},
		{"append settled member", func(m *Model) *Model { return appendMixedChild(m, 3, true) }},
		{"theme", func(m *Model) *Model {
			// Re-render every segment under the new styles. Without this the
			// untouched non-group segments keep the old colours (pre-existing).
			m.content.styles = testStyles("#ff8800")
			for i := range m.content.segments {
				m.content.segments[i].renderDirty = true
			}
			m.content.gen++
			return m
		}},
		{"jump flash", func(m *Model) *Model {
			dd := mixedEntries(m)[0]
			m.content.jumpFlash = jumpFlashState{dd: dd, on: true}
			m.content.dirtyDelegationCard(dd)
			return m
		}},
		{"jump flash off", func(m *Model) *Model {
			dd := mixedEntries(m)[0]
			m.content.jumpFlash = jumpFlashState{}
			m.content.dirtyDelegationCard(dd)
			return m
		}},
		{"late label child", func(m *Model) *Model {
			callID := "call_child-4"
			m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "sub_agent", callID, subAgentArgs(""))})
			return updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewDelegationStartedEvent(callOcc(callID, "child-4"), "explore part child-4", "", "explore"), "child-4")})
		}},
		{"late label bind", func(m *Model) *Model {
			return updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationAcceptedEvent(callOcc("call_child-4", "child-4"), mixedGroupName)})
		}},
	}
}

// groupDiffReplay builds the reference render: the script replayed from a fresh
// model, so no cache survives from an earlier step.
func groupDiffReplay(steps []groupDiffStep, width int) string {
	m := newContentBenchModel()
	for _, s := range steps {
		m = s.apply(m)
	}
	return m.content.String(width)
}

// TestGroupRenderCacheMatchesFreshReplay compares the incrementally cached
// transcript with a fresh replay after every step, at each listed width. A stale
// cache entry shows up as a difference at the step that should have invalidated
// it. The narrow width is listed first because it wraps each entry over the most
// rows, which exercises the row split hardest.
func TestGroupRenderCacheMatchesFreshReplay(t *testing.T) {
	freezeDelegationClockAt(t)

	steps := groupDiffScript()
	for _, width := range raceSample([]int{60, 100}) {
		// One model per width. The settled rows cache is keyed by width, so
		// alternating widths would evict it before each comparison.
		m := newContentBenchModel()
		for i, step := range steps {
			m = step.apply(m)
			got := m.content.String(width)
			want := groupDiffReplay(steps[:i+1], width)
			if got != want {
				t.Fatalf("step %d %q at width %d: cached transcript differs from fresh replay (got %d bytes, want %d)",
					i, step.name, width, len(got), len(want))
			}
		}
	}
}

// mixedGroupSegment returns the group segment of the transcript.
func mixedGroupSegment(t *testing.T, m *Model) *delegationGroupSegment {
	t.Helper()
	for i := range m.content.segments {
		if g := m.content.segments[i].delegGroupData; g != nil {
			return g
		}
	}
	t.Fatal("no group segment in transcript")
	return nil
}

// TestSpinnerTickReusesSettledRows checks the tick-vs-mutation split: a spinner
// tick re-renders the active card's header but keeps the settled rows cache.
func TestSpinnerTickReusesSettledRows(t *testing.T) {
	freezeDelegationClockAt(t)

	m := mixedGroupModelWithMessages(2, true, mixedGroupTestMessages())
	m.content.String(80)
	g := mixedGroupSegment(t, m)
	cached := reflect.ValueOf(g.settled.lines).Pointer()
	if cached == 0 {
		t.Fatal("settled rows not cached after the first render")
	}
	m.content.AdvanceDelegationSpinners()
	out := m.content.String(80)
	if got := reflect.ValueOf(g.settled.lines).Pointer(); got != cached {
		t.Errorf("spinner tick replaced the settled rows cache")
	}
	active := g.entries[len(g.entries)-1]
	if want := spinnerFrames[active.spinnerFrame%len(spinnerFrames)]; !strings.Contains(out, want) {
		t.Errorf("active header not re-rendered: spinner frame %q missing after tick", want)
	}
}
