package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/luispabon/steiner/internal/output"
)

func newGroupTestBuffer() *contentBuffer {
	return &contentBuffer{segments: make([]contentSegment, 0), collapseState: make(map[int]bool), pendingDelegateParents: make([]delegationLocator, 0), activeDelegations: make(map[string]delegationLocator)}
}

func subAgentArgs(group string) map[string]any {
	args := map[string]any{"type": "explore", "task": "find files"}
	if group != "" {
		args["group"] = group
	}
	return args
}

type groupStep struct {
	callID, group, batch string
	accepted             bool
}

func countDelegationSegments(b *contentBuffer) (segments, boxes int) {
	for _, seg := range b.segments {
		switch seg.kind {
		case segmentDelegation:
			segments++
			boxes++
		case segmentDelegationGroup:
			segments++
			boxes += len(seg.delegGroupData.entries)
		}
	}
	return
}

func TestDelegationGrouping(t *testing.T) {
	cases := []struct {
		name         string
		steps        []groupStep
		wantSegments int
	}{
		{"accepted matching tuple", []groupStep{{"a", "g", "batch", true}, {"b", "g", "batch", true}}, 1},
		{"different batch", []groupStep{{"a", "g", "batch-1", true}, {"b", "g", "batch-2", true}}, 2},
		{"different group", []groupStep{{"a", "g1", "batch", true}, {"b", "g2", "batch", true}}, 2},
		{"unknown membership", []groupStep{{"a", "g", "batch", false}, {"b", "g", "batch", false}}, 2},
		{"empty group membership", []groupStep{{"a", "", "batch", true}, {"b", "", "batch", true}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newGroupTestBuffer()
			for i, st := range tc.steps {
				b.AppendEvent(output.NewToolCallQueuedEvent(i, "sub_agent", st.callID, subAgentArgs(st.group)))
				b.AppendEvent(output.NewToolCallStartedEvent(i, "sub_agent", st.callID, subAgentArgs(st.group)))
				if st.accepted {
					b.AppendEvent(output.NewDelegationAcceptedEvent(st.callID, "", st.batch, st.group))
				}
			}
			segments, boxes := countDelegationSegments(b)
			if segments != tc.wantSegments || boxes != len(tc.steps) {
				t.Fatalf("segments/boxes = %d/%d, want %d/%d", segments, boxes, tc.wantSegments, len(tc.steps))
			}
		})
	}
}

func TestDelegationGroupingRequiresAcceptedMembership(t *testing.T) {
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "a", subAgentArgs("g")))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "b", subAgentArgs("g")))
	if segments, boxes := countDelegationSegments(b); segments != 2 || boxes != 2 {
		t.Fatalf("raw args grouped cards: %d/%d", segments, boxes)
	}
}

func TestDelegationRegroupPreservesInterveningSegments(t *testing.T) {
	b := newGroupTestBuffer()
	for _, id := range []string{"a", "b"} {
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", id, subAgentArgs("g")))
	}
	b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "middle"))
	b.AppendEvent(output.NewDelegationAcceptedEvent("a", "", "batch", "g"))
	b.AppendEvent(output.NewDelegationAcceptedEvent("b", "", "batch", "g"))
	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || b.segments[1].kind == segmentDelegation {
		t.Fatalf("segment order/kinds = %#v", b.segments)
	}
	if got := b.segments[0].delegGroupData.entries[0].parentCallID; got != "a" {
		t.Fatalf("first group member = %q, want a", got)
	}
}

func TestDelegationDrawOrderWithInterleavedStream(t *testing.T) {
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "a", subAgentArgs("")))
	b.AppendEvent(output.NewThinkingChunkEventWithSource(1, "pondering", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "some text", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "b", subAgentArgs("")))
	if segments, _ := countDelegationSegments(b); segments != 2 {
		t.Fatalf("unaccepted adjacent cards merged, count %d", segments)
	}
}

func TestDelegationGroupingAcrossAssistantMessageBoundaries(t *testing.T) {
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-1", subAgentArgs("research")))
	b.AppendEvent(output.NewDelegationAcceptedEvent("call-1", "", "batch", "research"))
	b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", ""))
	b.AppendEvent(output.NewToolCallStartedEvent(2, "sub_agent", "call-2", subAgentArgs("research")))
	b.AppendEvent(output.NewDelegationAcceptedEvent("call-2", "", "batch-2", "research"))
	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || b.segments[1].kind != segmentDelegationGroup {
		t.Fatalf("different accepted batches should remain separate singleton frames, got kinds %v", segmentKinds(b.segments))
	}
	if len(b.segments[0].delegGroupData.entries) != 1 || len(b.segments[1].delegGroupData.entries) != 1 { t.Fatal("different batch cards were combined") }
}

func segmentKinds(segments []contentSegment) []contentSegmentKind {
	out := make([]contentSegmentKind, len(segments))
	for i := range segments {
		out[i] = segments[i].kind
	}
	return out
}

func TestDelegationGroupNameOnlyWithAcceptedLabel(t *testing.T) {
	b := newGroupTestBuffer()
	b.styles = testStyles("#5599ff")
	for _, id := range []string{"a", "b"} {
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", id, subAgentArgs("final-review")))
		b.AppendEvent(output.NewDelegationAcceptedEvent(id, "", "batch", "final-review"))
	}
	if len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("want accepted group, got %v", segmentKinds(b.segments))
	}
	out := ansi.Strip(b.renderDelegationGroupSegment(b.segments[0], 60))
	if !strings.Contains(out, "2 agents") || !strings.Contains(out, "final-review") {
		t.Fatalf("footer missing accepted group identity: %q", out)
	}
}
