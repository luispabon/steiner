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
