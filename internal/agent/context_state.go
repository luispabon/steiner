package agent

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

// WithAddedRetainedSummary returns a copy of the state with one more retained
// summary appended.
func (s ContextState) WithAddedRetainedSummary(summary RetainedSummary) ContextState {
	next := s.Clone()
	next.RetainedSummaries = append(next.RetainedSummaries, summary)
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
