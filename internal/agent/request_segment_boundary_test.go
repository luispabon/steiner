package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// cacheBoundaryIndexes returns the positions of the messages carrying
// provider.Message.CacheBoundary.
func cacheBoundaryIndexes(messages []provider.Message) []int {
	var indexes []int
	for i, message := range messages {
		if message.CacheBoundary {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// segmentBoundaryConversation holds two immutable segments, the mutable tail
// summary and one live turn.
func segmentBoundaryConversation() []Message {
	return []Message{
		{Role: MessageRoleSummary, Content: "Segment 1. first", Segment: &SummarySegment{Sequence: 1, SourceGenerationID: 1, SourceMessageCount: 4}},
		{Role: MessageRoleSummary, Content: "Segment 2. second", Segment: &SummarySegment{Sequence: 2, SourceGenerationID: 2, SourceMessageCount: 3}},
		{Role: MessageRoleSummary, Content: "Segment 3. tail"},
		{Role: MessageRoleUser, Content: "current request"},
	}
}

// segmentBoundaryRequest is a minimal RunRequest that assembles and fits a
// prompt without a provider call.
func segmentBoundaryRequest() RunRequest {
	return RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SummaryMaxTokens:    128,
		},
		Events: output.NoopSink{},
	}
}

func TestPrepareTurnMarksImmutableSegmentBoundary(t *testing.T) {
	p := newTurnProgressor(segmentBoundaryRequest(), prompt.AssemblyOptions{}, nil)
	state := RunState{Conversation: segmentBoundaryConversation()}

	_, chatRequest, _, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	indexes := cacheBoundaryIndexes(chatRequest.Messages)
	if len(indexes) != 1 {
		t.Fatalf("cache boundaries = %v, want exactly one", indexes)
	}
	want := summaryContextPrefix + "Segment 2. second" + summaryContextSuffix
	if got := chatRequest.Messages[indexes[0]].Content; got != want {
		t.Fatalf("marked message content = %q, want %q", got, want)
	}
}

func TestPrepareTurnLeavesLegacySummaryUnmarked(t *testing.T) {
	p := newTurnProgressor(segmentBoundaryRequest(), prompt.AssemblyOptions{}, nil)
	state := RunState{Conversation: []Message{
		{Role: MessageRoleSummary, Content: "legacy summary"},
		{Role: MessageRoleUser, Content: "current request"},
	}}

	_, chatRequest, _, err := p.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	if indexes := cacheBoundaryIndexes(chatRequest.Messages); len(indexes) != 0 {
		t.Fatalf("cache boundaries = %v, want none for a legacy summary", indexes)
	}
}

func TestCompactionRequestMarksImmutableSegmentBoundary(t *testing.T) {
	conversation := segmentBoundaryConversation()
	state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
	candidate := ConversationCandidate{GenerationID: 1, View: ConversationViewFull, Messages: conversation}

	chatRequest, _, _, err := buildCompactionRequestWithMode(
		context.Background(), segmentBoundaryRequest(), state, candidate, prompt.CompactionModeNormal, 64,
	)
	if err != nil {
		t.Fatalf("buildCompactionRequestWithMode() error = %v", err)
	}
	indexes := cacheBoundaryIndexes(chatRequest.Messages)
	if len(indexes) != 1 {
		t.Fatalf("cache boundaries = %v, want exactly one", indexes)
	}
	want := summaryContextPrefix + "Segment 2. second" + summaryContextSuffix
	if got := chatRequest.Messages[indexes[0]].Content; got != want {
		t.Fatalf("marked message content = %q, want %q", got, want)
	}
	if last := chatRequest.Messages[len(chatRequest.Messages)-1]; last.Content != prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal) {
		t.Fatalf("compaction instruction changed: %q", last.Content)
	}
}
