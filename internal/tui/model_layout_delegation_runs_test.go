package tui

import "testing"

func TestJoinedDelegationDividerAndSourceClicks(t *testing.T) {
	m := newModel(Config{}, nil)
	m.viewport.SetWidth(80)
	m.content.styles = testStyles("#5599ff")

	first := &delegationDisplayState{promptText: "first prompt"}
	second := &delegationDisplayState{promptText: "second prompt"}
	joined := contentSegment{kind: segmentDelegation, delegData: second, delegationJoinedAbove: true}
	standalone := contentSegment{kind: segmentDelegation, delegData: first}

	if m.handleDelegationClick(&joined, 0) {
		t.Fatal("joined divider click triggered an action")
	}
	if second.collapsed {
		t.Fatal("joined divider click collapsed the second source")
	}
	if !m.handleDelegationClick(&standalone, 0) || !first.collapsed {
		t.Fatal("standalone top border did not toggle its source")
	}
	firstCollapsed := first.collapsed

	// Header and prompt-header rows belong to source content at rows 1 and 2.
	if !m.handleDelegationClick(&joined, 1) || !second.collapsed {
		t.Fatal("joined source header did not toggle only the second source")
	}
	second.collapsed = false
	if !m.handleDelegationClick(&joined, 2) || !second.promptCollapsed {
		t.Fatal("joined source prompt header did not toggle the second source prompt")
	}
	if first.collapsed != firstCollapsed {
		t.Fatal("second source click changed the first source")
	}
}

func TestJoinedDelegationMappingWithHiddenThinking(t *testing.T) {
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentDelegation, delegationJoinedAbove: false},
			{kind: segmentThinkingBlock},
			{kind: segmentDelegation, delegationJoinedAbove: true},
		},
		segmentHeights: []int{3, 0, 3},
		showThinking:   false,
	}

	for segIndex := range b.segments {
		for row := 0; row < b.segmentHeight(segIndex); row++ {
			line, ok := b.contentLineForSegmentRow(segIndex, row)
			if !ok {
				t.Fatalf("segment %d row %d has no content line", segIndex, row)
			}
			gotSeg, gotRow, ok := b.segmentAtContentLine(line)
			if !ok || gotSeg != segIndex || gotRow != row {
				t.Fatalf("segment %d row %d maps back to (%d, %d, %v)", segIndex, row, gotSeg, gotRow, ok)
			}
		}
	}
	if line, ok := b.contentLineForSegmentRow(2, 0); !ok || line != 3 {
		t.Fatalf("joined divider maps to line %d, %v; want line 3", line, ok)
	}
	if line, ok := b.contentLineForSegmentRow(2, 1); !ok || line != 4 {
		t.Fatalf("joined source content maps to line %d, %v; want line 4", line, ok)
	}
	if seg, row, ok := b.segmentAtContentLine(3); !ok || seg != 2 || row != 0 {
		t.Fatalf("joined divider line maps to (%d, %d, %v), want (2, 0, true)", seg, row, ok)
	}
}
