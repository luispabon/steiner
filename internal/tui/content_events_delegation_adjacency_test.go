package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestAdjacentUnnamedSpecialistsFormFrameBeforeAdvisor checks that two adjacent
// unnamed specialists share one unnamed frame with no footer, and that the
// following advisor stays its own card outside the frame.
func TestAdjacentUnnamedSpecialistsFormFrameBeforeAdvisor(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	b.styles = testStyles("#5599ff")
	startRegroupCards(b, "frame", "", 2)
	b.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "question", nil))

	if len(b.segments) != 2 {
		t.Fatalf("segments = %v, want unnamed frame then advisor", segmentKinds(b.segments))
	}
	frame := b.segments[0]
	if frame.kind != segmentDelegationGroup || len(frame.delegGroupData.entries) != 2 {
		t.Fatalf("first segment = %v, want unnamed frame of two specialists", segmentKinds(b.segments))
	}
	if b.segments[1].kind != segmentDelegation || !b.segments[1].delegData.isAdvisor {
		t.Fatalf("second segment = %v, want standalone advisor card", segmentKinds(b.segments))
	}
	if b.activeAdvisorSegment != 2 {
		t.Fatalf("activeAdvisorSegment = %d, want 2", b.activeAdvisorSegment)
	}
	if out := stripANSI(b.renderDelegationGroupSegment(frame, 60)); strings.Contains(out, "2 agents") {
		t.Fatalf("unnamed frame rendered a named footer: %q", out)
	}
}

// TestUnnamedPromotionInvalidatesWarmPrefix checks that promoting a settled,
// prefix-cached card into an unnamed frame drops the warm prefix and that the
// next render matches a cold render of the same buffer.
func TestUnnamedPromotionInvalidatesWarmPrefix(t *testing.T) {
	useTrueColor(t)
	b := newPrefixGenBuffer()
	b.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-1"), "do work", "", ""))
	b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: agentOcc("child-1"), Status: "completed"}))
	quietFrame(b)
	if b.prefixCacheLen != 1 || !b.prefixCacheValid(prefixGenWidth) {
		t.Fatalf("setup: prefix not warm over the settled card: len=%d", b.prefixCacheLen)
	}
	gen := b.gen

	b.AppendEvent(output.NewDelegationStartedEvent(agentOcc("child-2"), "other work", "", ""))
	if b.gen == gen || b.prefixCacheValid(prefixGenWidth) {
		t.Fatalf("promotion into the warm prefix left it valid: gen=%d want >%d", b.gen, gen)
	}
	if len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup || len(b.segments[0].delegGroupData.entries) != 2 {
		t.Fatalf("segments = %v, want one unnamed frame of two cards", segmentKinds(b.segments))
	}

	// Settle the live card so the timer text is stable across the two renders.
	b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: agentOcc("child-2"), Status: "completed"}))
	warm := b.String(prefixGenWidth)
	b.prefixCacheSet, b.prefixCacheRendered = false, ""
	b.stringCacheWidth, b.stringCacheBlocks = 0, nil
	cold := b.String(prefixGenWidth)
	if warm != cold {
		t.Fatalf("render after promotion differs from cold render:\nwarm:\n%s\ncold:\n%s", warm, cold)
	}
}

// TestStrictAdjacencyBreaksUnnamedFrame checks that any non-delegation segment
// between two unnamed specialists keeps them as separate standalone cards.
func TestStrictAdjacencyBreaksUnnamedFrame(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		insert func(b *contentBuffer)
	}{
		{"thinking", func(b *contentBuffer) {
			b.AppendEvent(output.NewThinkingChunkEventWithSource(1, "reasoning", output.ChunkSourceAssistant))
		}},
		{"assistant text", func(b *contentBuffer) {
			b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "text", output.ChunkSourceAssistant))
		}},
		{"status", func(b *contentBuffer) {
			b.appendStyled("status", segmentStatus)
		}},
		{"plain", func(b *contentBuffer) {
			b.appendStyled("plain", segmentPlain)
		}},
		{"user", func(b *contentBuffer) {
			b.AppendUser("question")
		}},
		{"tool call", func(b *contentBuffer) {
			b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "tool-call", map[string]any{"command": "true"}))
		}},
		{"delivery", func(b *contentBuffer) {
			b.AppendEvent(output.NewSubAgentsDeliveredEvent([]output.DeliveredSubAgent{{AgentID: "delivered", Status: "complete"}}))
		}},
		{"separator", func(b *contentBuffer) {
			b.AppendPhaseDivider("plan")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := groupTestBuffer()
			b.showThinking = true
			startRegroupCards(b, "before", "", 1)
			tc.insert(b)
			startRegroupCards(b, "after", "", 1)
			for _, seg := range b.segments {
				if seg.kind == segmentDelegationGroup {
					t.Fatalf("segments = %v, want no unnamed frame across %s", segmentKinds(b.segments), tc.name)
				}
			}
			if got := countDelegationCards(b.segments); got != 2 {
				t.Fatalf("delegation cards = %d, want 2 across %s", got, tc.name)
			}
		})
	}
}

// TestNamedGroupStaysDistinctFromAdjacentUnnamedFrame checks that an accepted
// named group and an adjacent unnamed frame never merge.
func TestNamedGroupStaysDistinctFromAdjacentUnnamedFrame(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	for _, id := range startRegroupCards(b, "named", "g", 2) {
		acceptRegroupCard(b, "named", "g", id)
	}
	startRegroupCards(b, "loose", "", 2)

	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || b.segments[1].kind != segmentDelegationGroup {
		t.Fatalf("segments = %v, want named group then unnamed frame", segmentKinds(b.segments))
	}
	for i, seg := range b.segments {
		for _, dd := range seg.delegGroupData.entries {
			if _, named := acceptedMembership(dd); named != (i == 0) {
				t.Fatalf("segment %d entry %q named = %v, want %v", i, dd.agentID, named, i == 0)
			}
		}
	}
}

// TestUnnamedSpecialistAfterAdvisorStaysStandalone checks that an advisor breaks
// adjacency, so a later specialist does not join the frame before the advisor.
func TestUnnamedSpecialistAfterAdvisorStaysStandalone(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	startRegroupCards(b, "frame", "", 2)
	b.AppendEvent(output.NewAdvisorStartedEvent("advisor-model", 1, 1, "question", nil))
	startRegroupCards(b, "after", "", 1)

	if len(b.segments) != 3 || b.segments[2].kind != segmentDelegation || b.segments[2].delegData.isAdvisor {
		t.Fatalf("segments = %v, want frame, advisor, then standalone specialist", segmentKinds(b.segments))
	}
}

// TestAcceptedTailCardLeavesUnnamedPrefixFrame checks that accepting the card at
// the tail of an unnamed frame leaves the remaining unnamed cards grouped and the
// accepted card as its own named group.
func TestAcceptedTailCardLeavesUnnamedPrefixFrame(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	startRegroupCards(b, "loose", "", 2)
	ids := startRegroupCards(b, "tail", "", 1)
	acceptRegroupCard(b, "tail", "g", ids[0])

	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || len(b.segments[0].delegGroupData.entries) != 2 {
		t.Fatalf("segments = %v, want unnamed frame of two then one named card", segmentKinds(b.segments))
	}
	if b.segments[1].kind != segmentDelegationGroup || len(b.segments[1].delegGroupData.entries) != 1 || b.segments[1].delegGroupData.entries[0].admission.Group != "g" {
		t.Fatalf("segment 1 = %v, want one named group entry in group g", segmentKinds(b.segments))
	}
}

// TestUnnamedPromotionRerendersCachedSettledCard checks that promoting a card
// into a frame whose first card is settled and segment-cached re-renders that
// card on the normal render path. The outer string cache is not reset, so a
// stale segment cache would show one card and differ from a cold render.
func TestUnnamedPromotionRerendersCachedSettledCard(t *testing.T) {
	useTrueColor(t)
	freezeDelegationClock(t)
	settled := func(b *contentBuffer) {
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-1", subAgentArgs("")))
		occ := callOcc("call-1", "child-1")
		b.AppendEvent(output.NewDelegationStartedEvent(occ, "first work", "", "explore"))
		b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: occ, Status: "completed"}))
	}
	appended := func(status string) func(b *contentBuffer) {
		return func(b *contentBuffer) {
			b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-2", subAgentArgs("")))
			if status == "" {
				return
			}
			occ := callOcc("call-2", "child-2")
			b.AppendEvent(output.NewDelegationStartedEvent(occ, "second work", "", "explore"))
			b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: occ, Status: status}))
		}
	}
	cases := []struct {
		name   string
		second func(b *contentBuffer)
	}{
		{"queued card", appended("")},
		{"completed card", appended("completed")},
		{"failed card", appended("failed")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warm := groupTestBuffer()
			settled(warm)
			quietFrame(warm)
			if len(warm.segments) != 1 || warm.segments[0].cachedRender == "" {
				t.Fatalf("setup: settled card not cached: segments=%v", segmentKinds(warm.segments))
			}
			tc.second(warm)
			got := warm.String(prefixGenWidth)

			cold := groupTestBuffer()
			settled(cold)
			tc.second(cold)
			if want := cold.String(prefixGenWidth); got != want {
				t.Fatalf("promoted render differs from cold render:\nwarm:\n%s\ncold:\n%s", stripANSI(got), stripANSI(want))
			}
			if len(warm.segments) != 1 || warm.segments[0].kind != segmentDelegationGroup {
				t.Fatalf("segments = %v, want one unnamed frame", segmentKinds(warm.segments))
			}
			if n := strings.Count(stripANSI(got), "explore"); n != 2 {
				t.Fatalf("explore headers = %d, want 2:\n%s", n, stripANSI(got))
			}
		})
	}
}

// TestAppendAdjacentSpecialistRerendersCachedTail checks the tail promotion in
// isolation. The joined card is hand-built and receives no later events, so the
// only write that can dirty the cached tail is appendAdjacentSpecialist. Its
// render must match a cold render of the same two cards.
func TestAppendAdjacentSpecialistRerendersCachedTail(t *testing.T) {
	useTrueColor(t)
	freezeDelegationClock(t)
	for _, status := range []string{"complete", "failed"} {
		t.Run(status, func(t *testing.T) {
			card := func(id string) *delegationDisplayState {
				return &delegationDisplayState{agentID: id, agentType: "explore", toolLabel: "explore", taskPreview: id + " work", status: status}
			}
			build := func(b *contentBuffer) {
				b.segments = append(b.segments, contentSegment{kind: segmentDelegation, delegData: card("child-1"), renderDirty: true})
			}

			warm := groupTestBuffer()
			build(warm)
			quietFrame(warm)
			if len(warm.segments) != 1 || warm.segments[0].cachedRender == "" {
				t.Fatalf("setup: tail card not cached: segments=%v", segmentKinds(warm.segments))
			}
			if !warm.appendAdjacentSpecialist(card("child-2")) {
				t.Fatal("appendAdjacentSpecialist did not join the unnamed tail card")
			}
			if len(warm.segments) != 1 || warm.segments[0].kind != segmentDelegationGroup {
				t.Fatalf("segments = %v, want one unnamed frame", segmentKinds(warm.segments))
			}
			got := warm.String(prefixGenWidth)

			cold := groupTestBuffer()
			build(cold)
			cold.appendAdjacentSpecialist(card("child-2"))
			if want := cold.String(prefixGenWidth); got != want {
				t.Fatalf("promoted render differs from cold render:\nwarm:\n%s\ncold:\n%s", stripANSI(got), stripANSI(want))
			}
			if n := strings.Count(stripANSI(got), "explore"); n != 2 {
				t.Fatalf("explore headers = %d, want 2:\n%s", n, stripANSI(got))
			}
		})
	}
}

// TestAdmittedMiddleMemberSplitsUnnamedFrame checks that admitting the middle
// card of an unnamed frame into a named group leaves its neighbours standalone,
// and that later updates land on the card they belong to.
func TestAdmittedMiddleMemberSplitsUnnamedFrame(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	ids := startRegroupCards(b, "batch", "", 3)
	tokens := flattenDelegationSegments(b.segments)
	if len(tokens) != 3 || len(b.segments) != 1 {
		t.Fatalf("setup: segments = %v, want one unnamed frame of three", segmentKinds(b.segments))
	}
	first, middle, last := tokens[0].dd, tokens[1].dd, tokens[2].dd
	firstStatus := first.status

	acceptRegroupCard(b, "batch", "g", ids[1])
	if len(b.segments) != 3 ||
		b.segments[0].kind != segmentDelegation || b.segments[0].delegData != first ||
		b.segments[1].kind != segmentDelegationGroup || len(b.segments[1].delegGroupData.entries) != 1 || b.segments[1].delegGroupData.entries[0] != middle ||
		b.segments[2].kind != segmentDelegation || b.segments[2].delegData != last {
		t.Fatalf("segments = %v, want standalone, named, standalone", segmentKinds(b.segments))
	}
	assertLocatorsResolve(t, b)

	occurrence := func(id string) output.DelegationOccurrence {
		return output.DelegationOccurrence{CallID: id, BatchID: "batch", AgentID: "agent-" + id}
	}
	b.AppendEvent(output.NewDelegationStartedEvent(occurrence(ids[1]), "middle task", "", "explore"))
	b.AppendEvent(output.NewDelegationStartedEvent(occurrence(ids[2]), "last task", "", "explore"))
	b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: occurrence(ids[2]), Status: "completed"}))
	if first.status != firstStatus || middle.status != "active" || last.status != "complete" {
		t.Fatalf("statuses first=%q (was %q) middle=%q last=%q, want first untouched, middle active, last complete", first.status, firstStatus, middle.status, last.status)
	}
	if b.segments[0].delegData != first || b.segments[1].delegGroupData.entries[0] != middle || b.segments[2].delegData != last {
		t.Fatalf("updates landed on the wrong segments: %v", segmentKinds(b.segments))
	}
	assertLocatorsResolve(t, b)
}

// assertLocatorsResolve checks that every delegation locator points at a
// segment that still holds the card it names.
func assertLocatorsResolve(t *testing.T, b *contentBuffer) {
	t.Helper()
	checkLocatorIndex(t, "delegations", b.segments, b.delegations)
	checkLocatorIndex(t, "openDelegations", b.segments, b.openDelegations)
	checkLocatorIndex(t, "activeDelegations", b.segments, b.activeDelegations)
}

func checkLocatorIndex[K comparable](t *testing.T, name string, segments []contentSegment, index map[K]delegationLocator) {
	t.Helper()
	for key, loc := range index {
		if loc.dd == nil || loc.seg < 0 || loc.seg >= len(segments) || !segmentHoldsDelegation(segments[loc.seg], loc.dd) {
			t.Errorf("%s[%v] = segment %d, which does not hold its card; segments=%v", name, key, loc.seg, segmentKinds(segments))
		}
	}
}

// TestUnnamedFrameJumpAndPickerRows checks that an unnamed frame has no picker
// group header and that jumping to its second card lands on the divider above it.
func TestUnnamedFrameJumpAndPickerRows(t *testing.T) {
	m := newJumpTestModel(t)
	addJumpFiller(m, 10)
	addJumpCard(m, 1, "a", "")
	second := addJumpCard(m, 2, "b", "")
	addJumpFiller(m, 80)
	m.syncViewport()

	frame := -1
	for i, seg := range m.content.segments {
		if seg.kind == segmentDelegationGroup {
			frame = i
		}
	}
	if frame < 0 || len(m.content.segments[frame].delegGroupData.entries) != 2 {
		t.Fatalf("segments = %v, want one unnamed frame of two", segmentKinds(m.content.segments))
	}

	cards := 0
	for _, row := range m.content.subAgentPickerRows("") {
		if row.groupHdr {
			t.Errorf("picker has group header %q for an unnamed frame", row.heading)
		}
		if row.dd != nil {
			cards++
		}
	}
	if cards != 2 {
		t.Errorf("picker card rows = %d, want 2", cards)
	}

	if m.jumpToDelegation(second) == nil {
		t.Fatal("jump to second frame card returned nil")
	}
	seg, row, dd, ok := m.content.delegationTopRow(second, m.viewport.Width())
	if !ok || dd != m.content.delegations[second].dd || seg != frame || row == 0 {
		t.Fatalf("delegationTopRow = %d,%d,%v, want divider row in frame %d", seg, row, ok, frame)
	}
	line, _ := m.content.contentLineForSegmentRow(seg, row)
	if got := m.viewport.YOffset(); got != line+m.contentTopPad {
		t.Errorf("YOffset = %d, want %d", got, line+m.contentTopPad)
	}
}

// TestMixedSpecialistFrameDividesWithoutFooter checks that an unnamed frame of
// different specialist types draws a divider between cards, falls back to the
// default border, and renders no named footer.
func TestMixedSpecialistFrameDividesWithoutFooter(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	startRegroupCards(b, "mixed", "", 2)
	frame := b.segments[0]
	if frame.kind != segmentDelegationGroup {
		t.Fatalf("segments = %v, want one unnamed frame", segmentKinds(b.segments))
	}
	frame.delegGroupData.entries[0].toolLabel = "explore"
	frame.delegGroupData.entries[1].toolLabel = "review"
	if label := delegationGroupBorderLabel(frame.delegGroupData); label != "" {
		t.Fatalf("border label = %q, want default border for mixed types", label)
	}
	out := stripANSI(b.renderDelegationGroupSegment(frame, 60))
	if strings.Contains(out, "2 agents") {
		t.Fatalf("unnamed mixed frame rendered a named footer:\n%s", out)
	}
	if divider := stripANSI(b.renderToolCallDivider(60 - 4)); !strings.Contains(out, divider) {
		t.Fatalf("frame has no divider between cards:\n%s", out)
	}
}
