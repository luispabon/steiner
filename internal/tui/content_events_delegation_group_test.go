package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/output"
)

func newGroupTestBuffer() *contentBuffer {
	return &contentBuffer{
		segments:               make([]contentSegment, 0),
		collapseState:          make(map[int]bool),
		pendingDelegateParents: make([]delegationLocator, 0),
		activeDelegations:      make(map[string]delegationLocator),
	}
}

func subAgentArgs(group string) map[string]any {
	args := map[string]any{"type": "explore", "task": "find files"}
	if group != "" {
		args["group"] = group
	}
	return args
}

type groupStep struct {
	callID   string
	group    string
	newBatch bool // an AssistantMessage precedes this call
	replay   bool // ToolCallStarted only, no queued event
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
	return segments, boxes
}

func TestDelegationGrouping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		steps        []groupStep
		wantSegments int
	}{
		{"ungrouped pair in one batch merges", []groupStep{{callID: "a"}, {callID: "b"}}, 1},
		{"same label in one batch merges", []groupStep{{callID: "a", group: "g"}, {callID: "b", group: "g"}}, 1},
		{"different labels split", []groupStep{{callID: "a", group: "g1"}, {callID: "b", group: "g2"}}, 2},
		{"labelled and ungrouped split", []groupStep{{callID: "a", group: "g"}, {callID: "b"}}, 2},
		{"same label across batches splits", []groupStep{{callID: "a", group: "g"}, {callID: "b", group: "g", newBatch: true}}, 2},
		{"ungrouped across batches splits", []groupStep{{callID: "a"}, {callID: "b", newBatch: true}}, 2},
		{"replay path groups the same", []groupStep{{callID: "a", group: "g", replay: true}, {callID: "b", group: "g", replay: true}}, 1},
		{"replay path splits labels", []groupStep{{callID: "a", group: "g1", replay: true}, {callID: "b", group: "g2", replay: true}}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newGroupTestBuffer()
			for i, st := range tt.steps {
				if st.newBatch {
					b.AppendEvent(output.NewAssistantMessageEvent(i, "assistant", "next turn"))
				}
				args := subAgentArgs(st.group)
				if !st.replay {
					b.AppendEvent(output.NewToolCallQueuedEvent(i, "sub_agent", st.callID, args))
				}
				b.AppendEvent(output.NewToolCallStartedEvent(i, "sub_agent", st.callID, args))
			}
			segs, boxes := countDelegationSegments(b)
			if segs != tt.wantSegments || boxes != len(tt.steps) {
				t.Fatalf("segments/boxes = %d/%d, want %d/%d", segs, boxes, tt.wantSegments, len(tt.steps))
			}
		})
	}
}

func TestDelegationDrawOrderWithInterleavedStream(t *testing.T) {
	t.Parallel()
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "a", subAgentArgs("")))
	b.AppendEvent(output.NewThinkingChunkEventWithSource(1, "pondering", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "some text", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "b", subAgentArgs("")))

	var kinds []int
	for _, seg := range b.segments {
		kinds = append(kinds, int(seg.kind))
	}
	if len(kinds) != 4 || kinds[0] != int(segmentDelegation) || kinds[3] != int(segmentDelegation) ||
		kinds[1] == int(segmentDelegation) || kinds[2] == int(segmentDelegation) ||
		kinds[1] == int(segmentDelegationGroup) || kinds[2] == int(segmentDelegationGroup) {
		t.Fatalf("segment kinds = %v, want delegation, thinking, text, delegation", kinds)
	}
}

func TestDelegationGroupNameOnlyWithLabel(t *testing.T) {
	useTrueColor(t)
	for _, tt := range []struct {
		label string
		want  bool
	}{{"final-review", true}, {"", false}} {
		b := newGroupTestBuffer()
		b.styles = testStyles("#5599ff")
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "a", subAgentArgs(tt.label)))
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "b", subAgentArgs(tt.label)))
		if len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup {
			t.Fatalf("label %q: want one group segment, got %d", tt.label, len(b.segments))
		}
		out := b.renderDelegationGroupSegment(b.segments[0], 60)
		top := ansi.Strip(strings.SplitN(out, "\n", 2)[0])
		if !strings.Contains(top, "2 agents") {
			t.Errorf("label %q: top border missing aggregate: %q", tt.label, top)
		}
		if got := strings.Contains(top, "final-review"); got != tt.want {
			t.Errorf("label %q: name in top border = %v, want %v: %q", tt.label, got, tt.want, top)
		}
	}
}
