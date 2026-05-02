package agent

import (
	"fmt"
	"strings"
)

// ContextState holds durable agent intent that must survive compaction.
// Later stages can render these fields into bounded prompt blocks without
// depending on transcript-only history.
type ContextState struct {
	ActiveConstraints  []ActiveConstraint
	UnresolvedWork     []UnresolvedWorkItem
	ActiveFocus        *ActiveFocus
	RetainedSummaries  []RetainedSummary
	FileTrackerSummary []string
	RecentToolCalls    []string
	TurnCount          int
	CompactionCount    int
	Scratchpad         string
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
		ActiveConstraints:  cloneActiveConstraints(s.ActiveConstraints),
		UnresolvedWork:     cloneUnresolvedWorkItems(s.UnresolvedWork),
		RetainedSummaries:  cloneRetainedSummaries(s.RetainedSummaries),
		FileTrackerSummary: cloneStrings(s.FileTrackerSummary),
		RecentToolCalls:    cloneStrings(s.RecentToolCalls),
		TurnCount:          s.TurnCount,
		CompactionCount:    s.CompactionCount,
		Scratchpad:         s.Scratchpad,
	}
	if s.ActiveFocus != nil {
		focus := *s.ActiveFocus
		next.ActiveFocus = &focus
	}
	return next
}

// Render returns a compact text snapshot of scaffold-maintained context state.
func (s ContextState) Render() string {
	var parts []string
	if s.TurnCount > 0 || s.CompactionCount > 0 {
		parts = append(parts, compactSessionState(s.TurnCount, s.CompactionCount))
	}
	if len(s.FileTrackerSummary) > 0 {
		parts = append(parts, "tracked files: "+strings.Join(s.FileTrackerSummary, "; "))
	}
	if len(s.RecentToolCalls) > 0 {
		parts = append(parts, "recent tool calls: "+strings.Join(s.RecentToolCalls, "; "))
	}
	return strings.Join(parts, "\n")
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

func cloneStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	copy(out, items)
	return out
}

func compactSessionState(turnCount, compactionCount int) string {
	return fmt.Sprintf("session state: turn=%d compactions=%d", turnCount, compactionCount)
}
