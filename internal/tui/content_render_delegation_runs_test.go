package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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

func TestDelegationRunFragmentGeometry(t *testing.T) {
	useTrueColor(t)
	for _, width := range []int{80, 16} {
		t.Run(strings.Repeat("x", width), func(t *testing.T) {
			b := delegationRunTestBuffer()
			b.segments = []contentSegment{
				delegationRunTestSegment("first"),
				{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "thought", collapsed: true}, renderDirty: true},
				delegationRunTestSegment("second"),
			}
			output := b.String(width)
			for i, line := range strings.Split(strings.TrimSuffix(output, "\\n"), "\\n") {
				if got := lipgloss.Width(line); got != width {
					t.Errorf("line %d visual width = %d, want %d: %q", i, got, width, line)
				}
			}
			if !strings.Contains(output, "│"+strings.Repeat("─", width-2)+"│") {
				t.Fatalf("joined fragments should use framed internal divider: %q", output)
			}
		})
	}
}

func TestDelegationRunBorderDoesNotCrossAdvisorBarrier(t *testing.T) {
	b := delegationRunTestBuffer()
	b.segments = []contentSegment{
		{kind: segmentDelegation, delegData: &delegationDisplayState{isAdvisor: true, agentType: "advisor", status: "complete"}, renderDirty: true},
		delegationRunTestSegment("specialist"),
	}
	b.updateDelegationRuns(80)
	if got := delegationRunBorderLabel(b, 1); got != "explore" {
		t.Fatalf("specialist border label = %q, want explore", got)
	}
}

func TestDelegationRunDirtyPropagationAcrossActiveNeighbor(t *testing.T) {
	b := delegationRunTestBuffer()
	first := delegationRunTestSegment("first")
	first.renderDirty = false
	first.cachedRender = "settled"
	first.cachedRenderWidth = 80
	last := delegationRunTestSegment("last")
	last.renderDirty = false
	last.cachedRender = "settled"
	last.cachedRenderWidth = 80
	last.delegData.status = "active"
	b.segments = []contentSegment{first, last}
	b.updateDelegationRuns(80)
	b.segments[0].renderDirty = false
	b.segments[1].renderDirty = false
	b.segments[0].delegData.status = "active"
	b.updateDelegationRuns(80)
	if !b.segments[0].renderDirty || !b.segments[1].renderDirty {
		t.Fatalf("active neighbor must dirty every fragment: %v %v", b.segments[0].renderDirty, b.segments[1].renderDirty)
	}
}

func TestDelegationRunWarmColdMutableStateUpdates(t *testing.T) {
	useTrueColor(t)
	for _, mutate := range []struct {
		name string
		fn   func(*delegationDisplayState)
	}{
		{name: "spinner tick", fn: func(dd *delegationDisplayState) { dd.spinnerFrame++ }},
		{name: "queued", fn: func(dd *delegationDisplayState) { dd.queuedForSlot = true }},
		{name: "cache wait", fn: func(dd *delegationDisplayState) { dd.cacheWaiting = true }},
		{name: "late label", fn: func(dd *delegationDisplayState) { dd.toolLabel = "review" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			warm := delegationRunTestBuffer()
			first := delegationRunTestSegment("first")
			first.delegData.status = "active"
			last := delegationRunTestSegment("last")
			last.delegData.status = "active"
			warm.segments = []contentSegment{first, last}
			warm.String(80)
			mutate.fn(warm.segments[1].delegData)
			warm.updateDelegationRuns(80)
			if !warm.segments[0].renderDirty {
				t.Fatal("active neighbor update did not dirty preceding fragment")
			}

			cold := delegationRunTestBuffer()
			coldFirst := delegationRunTestSegment("first")
			coldFirst.delegData.status = "active"
			coldLast := delegationRunTestSegment("last")
			coldLast.delegData.status = "active"
			mutate.fn(coldLast.delegData)
			cold.segments = []contentSegment{coldFirst, coldLast}
			if got, want := warm.String(80), cold.String(80); got != want {
				t.Fatalf("warm render differs from cold after update:\\n--- warm ---\\n%s\\n--- cold ---\\n%s", got, want)
			}
		})
	}
}

func TestDelegationRunUnrelatedClosedRenderGenerationStable(t *testing.T) {
	b := delegationRunTestBuffer()
	b.segments = []contentSegment{
		delegationRunTestSegment("closed"),
		{kind: segmentPlain, text: "barrier", renderDirty: true},
		delegationRunTestSegment("other"),
	}
	b.String(80)
	before := b.segments[0].renderGen
	b.segments[2].delegData.status = "active"
	b.segments[2].renderDirty = true
	b.String(80)
	if b.segments[0].renderGen != before {
		t.Fatalf("unrelated closed run renderGen = %d, want %d", b.segments[0].renderGen, before)
	}
}

func TestDelegationRunMutableWhenActiveOrAppendable(t *testing.T) {
	b := delegationRunTestBuffer()
	b.segments = []contentSegment{
		delegationRunTestSegment("active"),
		{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "hidden", collapsed: true}, renderDirty: true},
	}
	b.segments[0].delegData.status = "active"
	b.updateDelegationRuns(80)
	if !b.segments[0].delegationRunMutable {
		t.Fatal("active run should be mutable")
	}
	b.segments[0].delegData.status = "complete"
	b.updateDelegationRuns(80)
	if !b.segments[0].delegationRunMutable {
		t.Fatal("terminal visible run before hidden thinking should remain appendable")
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
