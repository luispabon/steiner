package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// mixedGroupName is the group label shared by every sub-agent of the mixed fixture.
const mixedGroupName = "discovery"

// appendMixedChild starts sub-agent j of the mixed group. See appendGroupChild.
func appendMixedChild(m *Model, j int, done bool) *Model {
	return appendGroupChild(m, mixedGroupName, j, done)
}

// appendGroupChild starts sub-agent j in group (empty for unnamed) and runs its
// assistant reply and read tool call. A done child also completes with output.
func appendGroupChild(m *Model, group string, j int, done bool) *Model {
	id := fmt.Sprintf("child-%d", j)
	callID := "call_" + id
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "sub_agent", callID, subAgentArgs(group))})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationAcceptedEvent(callOcc(callID, id), group)})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewDelegationStartedEvent(callOcc(callID, id), "explore part "+id, "", "explore"), id)})
	m = appendMixedChildWork(m, id, callID)
	if done {
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
			DelegationOccurrence: callOcc(callID, id), Status: "complete", TurnCount: 3, TokenCount: 900, ToolCallCount: 4,
			Output: strings.Repeat("finding about the module. ", 20),
		})})
	}
	return m
}

// appendMixedChildWork runs one assistant reply and one read tool call inside
// the sub-agent id.
func appendMixedChildWork(m *Model, id, callID string) *Model {
	m = updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewAssistantMessageEvent(1, "assistant", benchMarkdownReply), id)})
	rid := callID + "_read"
	m = updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallStartedEvent(1, "read", rid, map[string]any{"file_path": "/src/" + id + "/main.go"}), id)})
	return updateModelDirect(m, runtimeEventMsg{Event: output.WithAgentScope(output.NewToolCallFinishedEvent(1, "read", rid, strings.Repeat("package main\n\nfunc main() {}\n", 8), nil), id)})
}

// mixedGroupModel returns a 60-message transcript holding one group of
// settled+1 sub-agents: the first `settled` children complete and the last one
// stays active. Every entry is set expanded or collapsed.
func mixedGroupModel(settled int, expanded bool) *Model {
	m := populateLongTranscript(newContentBenchModel(), 60)
	for j := 0; j <= settled; j++ {
		m = appendMixedChild(m, j, j < settled)
	}
	for _, dd := range mixedEntries(m) {
		dd.collapsed = !expanded
		m.content.dirtyDelegationCard(dd)
	}
	m.syncViewport()
	return m
}

// mixedEntries returns every delegation card in the transcript, in order.
func mixedEntries(m *Model) []*delegationDisplayState {
	var out []*delegationDisplayState
	for _, tok := range flattenDelegationSegments(m.content.segments) {
		if tok.dd != nil {
			out = append(out, tok.dd)
		}
	}
	return out
}

// benchIdleFrameMixed measures one frame of the mixed group. With advance set,
// each frame first ticks the spinners, as the animation timer does.
func benchIdleFrameMixed(b *testing.B, settled int, expanded, advance bool) {
	b.ReportAllocs()
	m := mixedGroupModel(settled, expanded)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if advance {
			m.content.AdvanceDelegationSpinners()
		}
		m.syncViewport()
	}
}

// benchFirstRenderMixed measures a frame whose group must be drawn from scratch:
// a settled card is marked dirty and every group render cache is dropped first.
func benchFirstRenderMixed(b *testing.B, settled int, expanded bool) {
	b.ReportAllocs()
	m := mixedGroupModel(settled, expanded)
	entry := mixedEntries(m)[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetGroupRenderCaches(&m.content)
		m.content.dirtyDelegationCard(entry)
		m.syncViewport()
	}
}

// spinnerAdvanceModel returns a 60-message transcript with three active,
// collapsed, unnamed sub-agents. Each frame of the spinner benchmark advances
// their spinners, then renders.
func spinnerAdvanceModel() *Model {
	m := populateLongTranscript(newContentBenchModel(), 60)
	for j := range 3 {
		m = appendGroupChild(m, "", j, false)
	}
	for _, dd := range mixedEntries(m) {
		dd.collapsed = true
		m.content.dirtyDelegationCard(dd)
	}
	m.syncViewport()
	return m
}

// mixedGroupCRModel returns the mixed group of two settled expanded children and
// one active child, as mixedGroupModel(2, true) does, except that child-0's
// settled output holds a carriage return. That CR line makes the group content
// refuse the cold line seed.
func mixedGroupCRModel() *Model {
	m := populateLongTranscript(newContentBenchModel(), 60)
	for j := range 3 {
		m = appendGroupChild(m, mixedGroupName, j, false)
	}
	for j := range 2 {
		id := fmt.Sprintf("child-%d", j)
		out := strings.Repeat("finding about the module. ", 20)
		if j == 0 {
			out = "finding head\nprogress 10%\rprogress 100% done\nfinding tail " + out
		}
		m = updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
			DelegationOccurrence: callOcc("call_"+id, id), Status: "complete", TurnCount: 3, TokenCount: 900, ToolCallCount: 4,
			Output: out,
		})})
	}
	for _, dd := range mixedEntries(m) {
		dd.collapsed = false
		m.content.dirtyDelegationCard(dd)
	}
	m.syncViewport()
	return m
}

// BenchmarkGroupMixed covers the mixed group guards: idle frames, a frame with
// spinner ticks, and a first render of the group.
//
// The origin/main baselines in bench_guard_test.go come from this same file,
// copied into a git archive of origin/main. There, the port's
// bench_group_reset_test.go defines resetGroupRenderCaches as a no-op because
// main has no group render caches.
func BenchmarkGroupMixed(b *testing.B) {
	b.Run("idle/expanded2", func(b *testing.B) { benchIdleFrameMixed(b, 2, true, false) })
	b.Run("idle/collapsed5", func(b *testing.B) { benchIdleFrameMixed(b, 5, false, false) })
	b.Run("spinner/expanded2", func(b *testing.B) { benchIdleFrameMixed(b, 2, true, true) })
	b.Run("first/expanded2", func(b *testing.B) { benchFirstRenderMixed(b, 2, true) })
}
