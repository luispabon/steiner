package agent

// ContextState holds durable agent intent that must survive compaction.
// Later stages can render these fields into bounded prompt blocks without
// depending on transcript-only history.
type ContextState struct {
	ActiveConstraints []ActiveConstraint
	UnresolvedWork    []UnresolvedWorkItem
	ActiveFocus       *ActiveFocus
	RetainedSummaries []RetainedSummary
}

// ActiveConstraint represents a durable constraint that should remain in force
// across prompt compaction.
type ActiveConstraint struct {
	Text   string
	Source string
	Turn   int
}

// UnresolvedWorkItem represents an explicit piece of work that still needs to
// be completed.
type UnresolvedWorkItem struct {
	Text   string
	Source string
	Turn   int
}

// ActiveFocus represents the current durable focus that should be preserved
// when older transcript turns are compacted away.
type ActiveFocus struct {
	Text   string
	Source string
	Turn   int
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
		ActiveConstraints: cloneActiveConstraints(s.ActiveConstraints),
		UnresolvedWork:    cloneUnresolvedWorkItems(s.UnresolvedWork),
		RetainedSummaries: cloneRetainedSummaries(s.RetainedSummaries),
	}
	if s.ActiveFocus != nil {
		focus := *s.ActiveFocus
		next.ActiveFocus = &focus
	}
	return next
}

// WithActiveFocus returns a copy of the state with the active focus replaced.
func (s ContextState) WithActiveFocus(focus ActiveFocus) ContextState {
	next := s.Clone()
	next.ActiveFocus = &focus
	return next
}

// WithAddedConstraint returns a copy of the state with one more active
// constraint appended.
func (s ContextState) WithAddedConstraint(constraint ActiveConstraint) ContextState {
	next := s.Clone()
	next.ActiveConstraints = append(next.ActiveConstraints, constraint)
	return next
}

// WithAddedUnresolvedWork returns a copy of the state with one more unresolved
// work item appended.
func (s ContextState) WithAddedUnresolvedWork(item UnresolvedWorkItem) ContextState {
	next := s.Clone()
	next.UnresolvedWork = append(next.UnresolvedWork, item)
	return next
}

// WithAddedRetainedSummary returns a copy of the state with one more retained
// summary appended.
func (s ContextState) WithAddedRetainedSummary(summary RetainedSummary) ContextState {
	next := s.Clone()
	next.RetainedSummaries = append(next.RetainedSummaries, summary)
	return next
}

func cloneActiveConstraints(items []ActiveConstraint) []ActiveConstraint {
	if len(items) == 0 {
		return nil
	}
	out := make([]ActiveConstraint, len(items))
	copy(out, items)
	return out
}

func cloneUnresolvedWorkItems(items []UnresolvedWorkItem) []UnresolvedWorkItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]UnresolvedWorkItem, len(items))
	copy(out, items)
	return out
}

func cloneRetainedSummaries(items []RetainedSummary) []RetainedSummary {
	if len(items) == 0 {
		return nil
	}
	out := make([]RetainedSummary, len(items))
	copy(out, items)
	return out
}
