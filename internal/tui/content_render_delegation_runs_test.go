package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestDelegationRunHiddenThinkingJoinsAndRevealSplits(t *testing.T) {
	useTrueColor(t)
	b := delegationRunTestBuffer()
	b.segments = []contentSegment{
		delegationRunTestSegment("first"),
		{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "thought", collapsed: true}, renderDirty: true},
		delegationRunTestSegment("second"),
	}
	joined := b.String(80)
	if b.segmentHeights[0] == 0 || b.segmentHeights[2] == 0 {
		t.Fatalf("hidden thinking should join visible cards; heights = %v", b.segmentHeights)
	}
	if !b.segments[2].delegationJoinedAbove || b.segments[0].delegationJoinedAbove {
		t.Fatalf("joined flags = (%t, %t), want (false, true)", b.segments[0].delegationJoinedAbove, b.segments[2].delegationJoinedAbove)
	}
	if strings.Count(joined, "shared") != 1 {
		t.Fatalf("joined output should have one common footer label: %q", joined)
	}

	b.showThinking = true
	split := b.String(80)
	if b.segments[2].delegationJoinedAbove {
		t.Fatal("revealed thinking must split the delegation run")
	}
	if b.segmentHeights[1] == 0 || strings.Index(split, "thought") > strings.Index(split, "second") {
		t.Fatalf("revealed thinking should render between cards, heights %v: %q", b.segmentHeights, split)
	}
	b.showThinking = false
	b.String(80)
	if !b.segments[2].delegationJoinedAbove {
		t.Fatal("hiding thinking must rejoin the delegation run")
	}
}

func TestDelegationRunWarmPrefixMatchesColdAfterAppend(t *testing.T) {
	useTrueColor(t)
	warm := delegationRunTestBuffer()
	warm.segments = []contentSegment{delegationRunTestSegment("first")}
	warm.String(80)
	warm.streaming = true
	warm.streamBuffer = "preview"
	warm.String(80)
	warm.streaming = false
	warm.streamBuffer = ""
	warm.segments = append(warm.segments, delegationRunTestSegment("second"))
	got := warm.String(80)

	cold := delegationRunTestBuffer()
	cold.segments = []contentSegment{delegationRunTestSegment("first"), delegationRunTestSegment("second")}
	want := cold.String(80)
	if got != want {
		t.Fatalf("warm output differs from cold render:\n--- warm ---\n%s\n--- cold ---\n%s", got, want)
	}
	if !warm.segments[1].delegationJoinedAbove {
		t.Fatal("appended card did not join warm prefix card")
	}
	if strings.Count(got, "shared") != 1 || !strings.Contains(got, "2 agents") {
		t.Fatalf("footer should summarize both cards: %q", got)
	}
}

func delegationRunTestBuffer() *contentBuffer {
	return &contentBuffer{styles: testStyles(theme.AccentAmber), collapseState: make(map[int]bool), showThinking: false}
}

func delegationRunTestSegment(prompt string) contentSegment {
	return contentSegment{
		kind: segmentDelegation, renderDirty: true,
		delegData: &delegationDisplayState{agentType: "explore", group: "shared", status: "complete", promptText: prompt, promptCollapsed: false, collapsed: false},
	}
}
