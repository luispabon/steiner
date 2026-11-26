package interactive

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// TestManualCompactionSegmentPrefixKeepsChainAndTailOrder proves manual
// compaction stores the runner's immutable segment summaries followed by the
// segment-free tail summary, in that order, in the new generation's
// SummaryPrefix, and keeps the retained turns in the new generation's
// Messages.
func TestManualCompactionSegmentPrefixKeepsChainAndTailOrder(t *testing.T) {
	t.Parallel()

	prefix := []agent.Message{
		{Role: agent.MessageRoleSummary, Content: "Segment 1. first", Segment: &agent.SummarySegment{Sequence: 1, SourceMessageCount: 2}},
		{Role: agent.MessageRoleSummary, Content: "Segment 2. second", Segment: &agent.SummarySegment{Sequence: 2, SourceMessageCount: 4}},
		{Role: agent.MessageRoleSummary, Content: "mutable tail summary"},
	}
	retained := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "new request"},
		{Role: agent.MessageRoleAssistant, Content: "new answer"},
	}
	compacted := append(cloneMessages(prefix), cloneMessages(retained)...)

	runner := &runExecutorFunc{compact: func(context.Context, []agent.Message, []provider.ToolSpec) ([]agent.Message, error) {
		return cloneMessages(compacted), nil
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner, Config: config.Config{Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{DefaultModel: "test"}, Definitions: map[string]config.ModelConfig{"test": {ID: "test"}}}}})
	conversation := twoTurnConversation()
	seedConversation(s, conversation, agent.ConversationLineage{
		Generations:      []agent.ConversationGeneration{{ID: 1, Messages: cloneMessages(conversation)}},
		NextGenerationID: 2,
	})

	compactAndWait(t, s, "")

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.lineage.Generations) != 2 {
		t.Fatalf("generations = %d, want 2 (old + new)", len(s.lineage.Generations))
	}
	if got := s.lineage.Generations[0].Messages; !reflect.DeepEqual(got, conversation) {
		t.Errorf("generation 1 messages = %+v, want the pre-compaction conversation", got)
	}
	gen2 := s.lineage.Generations[1]
	if !reflect.DeepEqual(gen2.SummaryPrefix, prefix) {
		t.Fatalf("generation 2 SummaryPrefix = %+v, want two segments then the tail %+v", gen2.SummaryPrefix, prefix)
	}
	if seg := gen2.SummaryPrefix[len(gen2.SummaryPrefix)-1].Segment; seg != nil {
		t.Errorf("SummaryPrefix tail Segment = %+v, want nil for the mutable tail", seg)
	}
	if !reflect.DeepEqual(gen2.Messages, retained) {
		t.Errorf("generation 2 messages = %+v, want the retained turns %+v", gen2.Messages, retained)
	}
	if !reflect.DeepEqual(s.conversation, compacted) {
		t.Errorf("conversation = %+v, want the compacted %+v", s.conversation, compacted)
	}
}
