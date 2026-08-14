package agent

import (
	"context"

	"github.com/luispabon/steiner/internal/provider"
)

// ContextState holds durable agent state that must survive compaction.
type ContextState struct {
	RetainedSummaries  []RetainedSummary
	FileTrackerSummary []string
	RecentToolCalls    []string
	TurnCount          int
	CompactionCount    int
}

// RetainedSummary represents a compacted summary that should remain available
// to future turns even after the source transcript is dropped.
type RetainedSummary struct {
	Title  string
	Text   string
	Source string
	Turn   int
}

// Clone returns a deep copy of the context state.
func (s ContextState) Clone() ContextState {
	next := ContextState{
		RetainedSummaries:  cloneRetainedSummaries(s.RetainedSummaries),
		FileTrackerSummary: cloneStrings(s.FileTrackerSummary),
		RecentToolCalls:    cloneStrings(s.RecentToolCalls),
		TurnCount:          s.TurnCount,
		CompactionCount:    s.CompactionCount,
	}
	return next
}

func cloneRetainedSummaries(items []RetainedSummary) []RetainedSummary {
	if len(items) == 0 {
		return nil
	}
	out := make([]RetainedSummary, len(items))
	copy(out, items)
	return out
}

func cloneStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	copy(out, items)
	return out
}

type conversationSnapshotContextKey struct{}

// WithConversationSnapshot returns a context carrying a cloned provider-message
// snapshot for tool-adjacent consumers.
func WithConversationSnapshot(ctx context.Context, snapshot []provider.Message) context.Context {
	return context.WithValue(ctx, conversationSnapshotContextKey{}, provider.CloneMessages(snapshot))
}

// ConversationSnapshotFromContext returns a cloned conversation snapshot when present.
func ConversationSnapshotFromContext(ctx context.Context) ([]provider.Message, bool) {
	snapshot, ok := ctx.Value(conversationSnapshotContextKey{}).([]provider.Message)
	if !ok {
		return nil, false
	}
	return provider.CloneMessages(snapshot), true
}
