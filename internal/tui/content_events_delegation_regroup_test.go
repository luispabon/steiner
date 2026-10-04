package tui

import (
	"fmt"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func regroupTestBuffer(prior int) *contentBuffer {
	b := groupTestBuffer()
	b.activeDelegations = make(map[string]delegationLocator)
	for i := range prior {
		b.appendStyled(fmt.Sprintf("prior segment %d", i), segmentStatus)
	}
	return b
}

func startRegroupCards(b *contentBuffer, batch string, group string, n int) []string {
	ids := make([]string, n)
	for i := range n {
		ids[i] = fmt.Sprintf("%s-call-%d", batch, i)
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", ids[i], subAgentArgs(group)))
	}
	return ids
}

func acceptRegroupCard(b *contentBuffer, batch, group, callID string) {
	b.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: callID, BatchID: batch, AgentID: "agent-" + callID}, group))
}

// warmRegroupBuffer renders every segment, then forces a prefix rebuild so the
// settled-prefix cache covers everything up to the first unsettled segment.
func warmRegroupBuffer(b *contentBuffer) {
	b.String(80)
	b.gen++
	b.String(80)
}

func BenchmarkRegroupLongTranscript(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		buf := regroupTestBuffer(2000)
		ids := startRegroupCards(buf, "batch", "grp", 6)
		warmRegroupBuffer(buf)
		b.StartTimer()
		for _, id := range ids {
			acceptRegroupCard(buf, "batch", "grp", id)
			buf.String(80)
		}
	}
}

func rejectRegroupCard(b *contentBuffer, callID string) {
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", callID, "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
}

// fullRegroup rebuilds every segment of b the way a whole-buffer rewrite does.
func fullRegroup(b *contentBuffer) []contentSegment {
	tokens := flattenDelegationSegments(b.segments)
	members := make(map[delegationMembership][]*delegationDisplayState)
	for _, tok := range tokens {
		if key, ok := acceptedMembership(tok.dd); ok {
			members[key] = append(members[key], tok.dd)
		}
	}
	next, _ := buildRegroupedSegments(tokens, members, 0)
	return next
}

type regroupStep struct {
	name string
	run  func(b *contentBuffer)
}

func regroupScenarios() map[string][]regroupStep {
	start := func(call, group string) regroupStep {
		return regroupStep{"start " + call, func(b *contentBuffer) {
			b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", call, subAgentArgs(group)))
		}}
	}
	accept := func(batch, call, group string) regroupStep {
		return regroupStep{"accept " + call, func(b *contentBuffer) { acceptRegroupCard(b, batch, group, call) }}
	}
	reject := func(call string) regroupStep {
		return regroupStep{"reject " + call, func(b *contentBuffer) { rejectRegroupCard(b, call) }}
	}
	text := regroupStep{"assistant text", func(b *contentBuffer) {
		b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "between cards"))
	}}
	return map[string][]regroupStep{
		"group at end of long transcript": {
			start("a1", "g"), start("a2", "g"), start("a3", "g"),
			accept("A", "a1", "g"), accept("A", "a2", "g"), accept("A", "a3", "g"),
		},
		"two batches in the tail": {
			start("a1", "g"), start("a2", "g"), start("b1", "h"), start("b2", "h"),
			accept("A", "a1", "g"), accept("B", "b1", "h"), accept("A", "a2", "g"), accept("B", "b2", "h"),
		},
		"ungrouped delegation mixed in": {
			start("a1", "g"), start("loose", ""), start("a2", "g"), text, start("a3", "g"),
			accept("A", "a1", "g"), accept("A", "a3", "g"), accept("A", "a2", "g"),
		},
		"rejection after accepted members": {
			start("a1", "g"), start("a2", "g"), start("a3", "g"),
			accept("A", "a1", "g"), accept("A", "a2", "g"), reject("a3"),
		},
	}
}

func TestRegroupTailOnlyMatchesFullRebuild(t *testing.T) {
	for name, steps := range regroupScenarios() {
		t.Run(name, func(t *testing.T) {
			const prior = 300
			warm, cold := regroupTestBuffer(prior), regroupTestBuffer(prior)
			warmRegroupBuffer(warm)
			before := slices.Clone(warm.segments)
			for _, step := range steps {
				step.run(warm)
				step.run(cold)
				warm.String(80)
				if want := fullRegroup(warm); !sameSegments(warm.segments, want) {
					t.Fatalf("after %q: segments = %v, full rebuild = %v", step.name, segmentKinds(warm.segments), segmentKinds(want))
				}
				if !slices.EqualFunc(before[:prior], warm.segments[:prior], func(a, b contentSegment) bool {
					return a.text == b.text && a.kind == b.kind && a.cachedRender == b.cachedRender && a.renderGen == b.renderGen
				}) {
					t.Fatalf("after %q: segments before the rewrite changed", step.name)
				}
			}
			if got, want := warm.String(80), cold.String(80); got != want {
				t.Fatalf("incremental render differs from cold render:\n%s\n---\n%s", got, want)
			}
			if len(warm.segmentHeights) != len(warm.segments) {
				t.Fatalf("segment heights = %d, want %d", len(warm.segmentHeights), len(warm.segments))
			}
			for i := range prior {
				if warm.segmentHeights[i] != cold.segmentHeights[i] {
					t.Fatalf("segment height %d = %d, want %d", i, warm.segmentHeights[i], cold.segmentHeights[i])
				}
			}
		})
	}
}

func TestRegroupKeepsSettledPrefixCache(t *testing.T) {
	const prior = 200
	b := regroupTestBuffer(prior)
	ids := startRegroupCards(b, "A", "g", 3)
	warmRegroupBuffer(b)
	if !b.prefixCacheValid(80) || b.prefixCacheLen != prior {
		t.Fatalf("setup prefix cache len = %d valid = %v, want %d valid", b.prefixCacheLen, b.prefixCacheValid(80), prior)
	}
	rendered := b.prefixCacheRendered
	for _, id := range ids {
		acceptRegroupCard(b, "A", "g", id)
		if !b.prefixCacheValid(80) || b.prefixCacheLen != prior || b.prefixCacheRendered != rendered {
			t.Fatalf("prefix cache not retained across regroup of %s", id)
		}
		if b.stringCacheBlocks != nil {
			t.Fatalf("string cache survived regroup of %s", id)
		}
	}
}

func TestRegroupDropsPrefixCacheCoveringRewrittenSegments(t *testing.T) {
	b := regroupTestBuffer(50)
	ids := startRegroupCards(b, "A", "g", 2)
	acceptRegroupCard(b, "A", "g", ids[0])
	b.String(80)
	b.String(80)
	b.prefixCacheLen = len(b.segments)
	b.prefixCacheGen = b.gen
	acceptRegroupCard(b, "A", "g", ids[1])
	if b.prefixCacheSet {
		t.Fatalf("prefix cache covering rewritten segments survived: len = %d, segments = %d", b.prefixCacheLen, len(b.segments))
	}
}

// An unsettled segment ahead of the batch stops the settled prefix short of the
// rewrite start. That prefix covers only unchanged segments, so it must survive.
func TestRegroupKeepsShortPrefixCacheAheadOfUnsettledSegment(t *testing.T) {
	const prior = 200
	build := func() (*contentBuffer, []string) {
		b := regroupTestBuffer(prior)
		b.segments = append(b.segments, contentSegment{kind: segmentCompactionBanner, compactionData: &compactionBannerData{label: "compacting"}, renderDirty: true})
		return b, startRegroupCards(b, "A", "g", 3)
	}
	warm, ids := build()
	cold, _ := build()
	warmRegroupBuffer(warm)
	if !warm.prefixCacheValid(80) || warm.prefixCacheLen != prior {
		t.Fatalf("setup prefix cache len = %d valid = %v, want %d valid", warm.prefixCacheLen, warm.prefixCacheValid(80), prior)
	}
	rendered := warm.prefixCacheRendered
	for _, id := range ids {
		acceptRegroupCard(warm, "A", "g", id)
		acceptRegroupCard(cold, "A", "g", id)
		if !warm.prefixCacheValid(80) || warm.prefixCacheLen != prior || warm.prefixCacheRendered != rendered {
			t.Fatalf("short prefix cache not retained across regroup of %s", id)
		}
		warm.String(80)
	}
	if got, want := warm.String(80), cold.String(80); got != want {
		t.Fatalf("incremental render differs from cold render:\n%s\n---\n%s", got, want)
	}
}
