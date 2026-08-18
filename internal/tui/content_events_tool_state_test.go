package tui

import "testing"

func TestAppendAdjacentToolCallGroupsAdjacentTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		firstTool  string
		secondTool string
		wantMixed  bool
	}{
		{name: "different tools", firstTool: "read", secondTool: "bash", wantMixed: true},
		{name: "same tool", firstTool: "read", secondTool: "read", wantMixed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			first := &toolCallSegment{tool: tt.firstTool}
			second := &toolCallSegment{tool: tt.secondTool}
			b := &contentBuffer{
				segments: []contentSegment{{kind: segmentToolCall, toolData: first}},
			}

			if !b.appendAdjacentToolCall(second) {
				t.Fatal("appendAdjacentToolCall() = false, want true")
			}
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			group := b.segments[0].toolGroupData
			if b.segments[0].kind != segmentToolCallGroup || group == nil {
				t.Fatalf("segment = %#v, want tool call group", b.segments[0])
			}
			if group.mixed != tt.wantMixed {
				t.Fatalf("group.mixed = %v, want %v", group.mixed, tt.wantMixed)
			}
			if len(group.entries) != 2 || group.entries[0] != first || group.entries[1] != second {
				t.Fatalf("group.entries = %#v, want both calls in order", group.entries)
			}
		})
	}
}

func TestAppendAdjacentToolCallKeepsMixedGroupMixed(t *testing.T) {
	t.Parallel()

	first := &toolCallSegment{tool: "read"}
	second := &toolCallSegment{tool: "bash"}
	third := &toolCallSegment{tool: "mutate"}
	b := &contentBuffer{
		segments: []contentSegment{{kind: segmentToolCall, toolData: first}},
	}

	if !b.appendAdjacentToolCall(second) || !b.appendAdjacentToolCall(third) {
		t.Fatal("appendAdjacentToolCall() = false, want both calls grouped")
	}
	group := b.segments[0].toolGroupData
	if group == nil || !group.mixed {
		t.Fatalf("group = %#v, want mixed group", group)
	}
	if len(group.entries) != 3 {
		t.Fatalf("group entries = %d, want 3", len(group.entries))
	}
}
