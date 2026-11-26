package interactive

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// seamSegmentChain is the [segment 1][segment 2][tail] chain a segmented run
// hands interactive as leading summary messages.
func seamSegmentChain() []agent.Message {
	return []agent.Message{
		{Role: agent.MessageRoleSummary, Content: "Segment 1. Later segments override earlier conflicting state.\n\nfirst span", Segment: &agent.SummarySegment{Sequence: 1, SourceMessageCount: 4}},
		{Role: agent.MessageRoleSummary, Content: "Segment 2. Later segments override earlier conflicting state.\n\nsecond span", Segment: &agent.SummarySegment{Sequence: 2, SourceMessageCount: 3}},
		{Role: agent.MessageRoleSummary, Content: "mutable tail summary"},
	}
}

// seamExtendedSegmentChain is seamSegmentChain with segment 3 inserted before
// the mutable tail.
func seamExtendedSegmentChain() []agent.Message {
	chain := seamSegmentChain()
	extended := make([]agent.Message, 0, len(chain)+1)
	extended = append(extended, chain[:len(chain)-1]...)
	extended = append(extended, agent.Message{
		Role:    agent.MessageRoleSummary,
		Content: "Segment 3. Later segments override earlier conflicting state.\n\nthird span",
		Segment: &agent.SummarySegment{Sequence: 3, SourceMessageCount: 5},
	})
	return append(extended, chain[len(chain)-1])
}

// seamAssertSegmentChain checks got is want with every content byte-identical,
// every segment coordinate intact, and the tail still segment-free.
func seamAssertSegmentChain(t *testing.T, label string, got, want []agent.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v, want %d summaries %+v", label, got, len(want), want)
	}
	for i := range want {
		if got[i].Role != agent.MessageRoleSummary {
			t.Fatalf("%s[%d] role = %q, want %q", label, i, got[i].Role, agent.MessageRoleSummary)
		}
		if got[i].Content != want[i].Content {
			t.Fatalf("%s[%d] content = %q, want byte-identical %q", label, i, got[i].Content, want[i].Content)
		}
		switch {
		case want[i].Segment == nil:
			if got[i].Segment != nil {
				t.Fatalf("%s[%d] Segment = %+v, want nil for the mutable tail", label, i, got[i].Segment)
			}
		case got[i].Segment == nil:
			t.Fatalf("%s[%d] Segment = nil, want %+v", label, i, *want[i].Segment)
		case *got[i].Segment != *want[i].Segment:
			t.Fatalf("%s[%d] Segment = %+v, want %+v", label, i, *got[i].Segment, *want[i].Segment)
		}
	}
}

// TestSegmentSeamMergeRunLineageKeepsChainAndSeedsNextRun proves a finished
// run whose messages start with [segment 1][segment 2][tail] merges into the
// session lineage with the chain intact, and that the next run is seeded from
// that merged lineage with the same three summaries first, unchanged.
func TestSegmentSeamMergeRunLineageKeepsChainAndSeedsNextRun(t *testing.T) {
	t.Parallel()

	chain := seamSegmentChain()
	ordinary := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "carry on"},
		{Role: agent.MessageRoleAssistant, Content: "done"},
	}
	runMessages := append(cloneMessages(chain), cloneMessages(ordinary)...)

	s := testNewSession(t, Dependencies{Config: guardTestConfig()})

	var seeds []RunInput
	runs := 0
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		seeds = append(seeds, in)
		runs++
		if runs == 1 {
			return RunResult{Conversation: cloneMessages(runMessages), Lineage: lineageOf(cloneMessages(runMessages)...)}, nil
		}
		return withAnswer(in, "third answer"), nil
	}})
	prior := twoTurnConversation()
	seedConversation(s, prior, lineageOf(prior...))

	submitAndWait(t, s, "run after the segmented compaction", nil)

	s.mu.RLock()
	generations := s.lineage.Generations
	conversation := cloneMessages(s.conversation)
	s.mu.RUnlock()
	if len(generations) != 1 {
		t.Fatalf("generations after merge = %d, want 1 (the run folded into the existing generation)", len(generations))
	}
	seamAssertSegmentChain(t, "merged SummaryPrefix", generations[0].SummaryPrefix, chain)
	if !reflect.DeepEqual(generations[0].Messages, ordinary) {
		t.Fatalf("merged Messages = %+v, want the ordinary turns %+v", generations[0].Messages, ordinary)
	}
	if len(conversation) < len(chain) {
		t.Fatalf("conversation = %+v, want it to start with the segment chain %+v", conversation, chain)
	}
	if got := conversation[:len(chain)]; !reflect.DeepEqual(got, chain) {
		t.Fatalf("conversation starts with %+v, want the segment chain %+v", got, chain)
	}

	submitAndWait(t, s, "next run", nil)
	if len(seeds) != 2 {
		t.Fatalf("run seeds recorded = %d, want 2", len(seeds))
	}
	seedGenerations := seeds[1].Lineage.Generations
	if len(seedGenerations) == 0 {
		t.Fatal("next run seed lineage has no generations")
	}
	seamAssertSegmentChain(t, "next run seed SummaryPrefix", seedGenerations[len(seedGenerations)-1].SummaryPrefix, chain)
	seedConv := seeds[1].Conversation
	if len(seedConv) < len(chain) {
		t.Fatalf("next run conversation = %+v, want it to start with the segment chain %+v", seedConv, chain)
	}
	if got := seedConv[:len(chain)]; !reflect.DeepEqual(got, chain) {
		t.Fatalf("next run conversation starts with %+v, want the segment chain %+v", got, chain)
	}
}

// TestSegmentSeamMergeRunLineageKeepsAppendedSegment proves a later run that
// appends segment 3 keeps [segment 1][segment 2][segment 3][tail] in order when
// merged over the prior chain.
func TestSegmentSeamMergeRunLineageKeepsAppendedSegment(t *testing.T) {
	t.Parallel()

	prior := append(cloneMessages(seamSegmentChain()),
		agent.Message{Role: agent.MessageRoleUser, Content: "earlier turn"},
		agent.Message{Role: agent.MessageRoleAssistant, Content: "earlier answer"})
	chain := seamExtendedSegmentChain()
	runMessages := append(cloneMessages(chain), agent.Message{Role: agent.MessageRoleUser, Content: "latest turn"})

	merged := mergeRunLineage(lineageOf(prior...), lineageOf(runMessages...))
	if len(merged.Generations) != 1 {
		t.Fatalf("generations = %d, want 1", len(merged.Generations))
	}
	seamAssertSegmentChain(t, "merged SummaryPrefix", merged.Generations[0].SummaryPrefix, chain)
	if got := merged.Generations[0].Messages; len(got) != 1 || got[0].Content != "latest turn" {
		t.Fatalf("merged Messages = %+v, want the latest turn only", got)
	}
}
