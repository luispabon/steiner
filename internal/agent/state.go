package agent

import "github.com/luispabon/steiner/internal/tool"

// StopReason records why a run terminated.
type StopReason string

const (
	// StopReasonMaxTurns indicates the run exhausted its turn budget.
	StopReasonMaxTurns StopReason = "max_turns"
	// StopReasonMaxTokens indicates the run exhausted its token budget.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonCancelled indicates the caller cancelled the run context.
	StopReasonCancelled StopReason = "cancelled"
	// StopReasonComplete indicates the run reached a normal completion state.
	StopReasonComplete StopReason = "complete"
	// StopReasonError indicates the run terminated due to an error.
	StopReasonError StopReason = "error"
	// StopReasonWorkflowHandoff indicates the run terminated because a
	// workflow handoff request was accepted.
	StopReasonWorkflowHandoff StopReason = "workflow_handoff"
)

// ConversationViewKind identifies the view used for a conversation candidate.
type ConversationViewKind string

const (
	// ConversationViewFull keeps the full generation, including any summary prefix.
	ConversationViewFull ConversationViewKind = "full"
	// ConversationViewSummaryPrefixStripped removes the summary prefix from the view.
	ConversationViewSummaryPrefixStripped ConversationViewKind = "summary_prefix_stripped"
)

// ConversationGeneration captures one canonical conversation snapshot.
//
// SummaryPrefix holds any earlier compacted content that is still part of the
// active lineage, while Messages holds the raw messages that were added in this
// generation after the summary prefix was established.
type ConversationGeneration struct {
	ID            int       `json:"id"`
	SummaryPrefix []Message `json:"summary_prefix"`
	Messages      []Message `json:"messages"`
}

func newConversationGeneration(id int, summaryPrefix, messages []Message) ConversationGeneration {
	return ConversationGeneration{
		ID:            id,
		SummaryPrefix: cloneMessages(summaryPrefix),
		Messages:      cloneMessages(messages),
	}
}

// Clone returns a deep copy of the generation.
func (g ConversationGeneration) Clone() ConversationGeneration {
	return newConversationGeneration(g.ID, g.SummaryPrefix, g.Messages)
}

// FullMessages returns the complete generation view, including the summary
// prefix and the raw messages that follow it.
func (g ConversationGeneration) FullMessages() []Message {
	if len(g.SummaryPrefix) == 0 {
		return cloneMessages(g.Messages)
	}
	out := make([]Message, 0, len(g.SummaryPrefix)+len(g.Messages))
	out = append(out, cloneMessages(g.SummaryPrefix)...)
	out = append(out, cloneMessages(g.Messages)...)
	return out
}

// SummaryPrefixStrippedMessages returns the raw messages for the generation
// without caller-side slicing.
func (g ConversationGeneration) SummaryPrefixStrippedMessages() []Message {
	return cloneMessages(g.Messages)
}

// ConversationCandidate captures one candidate view of the conversation lineage.
type ConversationCandidate struct {
	GenerationID int
	View         ConversationViewKind
	Messages     []Message
}

// ConversationLineage tracks the generation history of a conversation.
type ConversationLineage struct {
	Generations      []ConversationGeneration `json:"generations"`
	NextGenerationID int                      `json:"next_generation_id"`
}

func newConversationLineage(messages []Message) ConversationLineage {
	return ConversationLineage{
		Generations:      []ConversationGeneration{newConversationGeneration(1, nil, messages)},
		NextGenerationID: 2,
	}
}

// Clone returns a deep copy of the lineage.
func (l ConversationLineage) Clone() ConversationLineage {
	if len(l.Generations) == 0 {
		return ConversationLineage{NextGenerationID: l.NextGenerationID}
	}
	out := ConversationLineage{
		Generations:      make([]ConversationGeneration, len(l.Generations)),
		NextGenerationID: l.NextGenerationID,
	}
	for i := range l.Generations {
		out.Generations[i] = l.Generations[i].Clone()
	}
	return out
}

// Empty reports whether the lineage has any generations.
func (l ConversationLineage) Empty() bool {
	return len(l.Generations) == 0
}

// Latest returns the newest generation in the lineage.
func (l ConversationLineage) Latest() (ConversationGeneration, bool) {
	if len(l.Generations) == 0 {
		return ConversationGeneration{}, false
	}
	return l.Generations[len(l.Generations)-1].Clone(), true
}

// FullMessages returns the full message view for the latest generation.
func (l ConversationLineage) FullMessages() []Message {
	latest, ok := l.Latest()
	if !ok {
		return nil
	}
	return latest.FullMessages()
}

// SummaryPrefixStrippedMessages returns only the raw messages from the latest generation.
func (l ConversationLineage) SummaryPrefixStrippedMessages() []Message {
	latest, ok := l.Latest()
	if !ok {
		return nil
	}
	return latest.SummaryPrefixStrippedMessages()
}

// WithCurrentMessages returns a copy with the latest generation's messages replaced.
func (l ConversationLineage) WithCurrentMessages(messages []Message) ConversationLineage {
	next := l.Clone()
	if len(next.Generations) == 0 {
		return newConversationLineage(messages)
	}
	next.Generations[len(next.Generations)-1].Messages = cloneMessages(messages)
	return next
}

// WithAppendedMessages returns a copy with messages appended to the latest generation.
func (l ConversationLineage) WithAppendedMessages(messages []Message) ConversationLineage {
	if len(messages) == 0 {
		return l.Clone()
	}
	next := l.Clone()
	if len(next.Generations) == 0 {
		return newConversationLineage(messages)
	}
	current := &next.Generations[len(next.Generations)-1]
	current.Messages = append(current.Messages, cloneMessages(messages)...)
	return next
}

// WithNewGeneration returns a copy with a newly appended generation.
func (l ConversationLineage) WithNewGeneration(summaryPrefix, messages []Message) ConversationLineage {
	next := l.Clone()
	id := next.NextGenerationID
	if id <= 0 {
		id = len(next.Generations) + 1
	}
	next.Generations = append(next.Generations, newConversationGeneration(id, summaryPrefix, messages))
	next.NextGenerationID = id + 1
	return next
}

// Candidates returns lineage views ordered from highest to lower fidelity.
func (l ConversationLineage) Candidates() []ConversationCandidate {
	if len(l.Generations) == 0 {
		return nil
	}
	candidates := make([]ConversationCandidate, 0, len(l.Generations)*2)
	for i := len(l.Generations) - 1; i >= 0; i-- {
		generation := l.Generations[i]
		candidates = append(candidates, ConversationCandidate{
			GenerationID: generation.ID,
			View:         ConversationViewFull,
			Messages:     generation.FullMessages(),
		})
		if len(generation.SummaryPrefix) > 0 {
			candidates = append(candidates, ConversationCandidate{
				GenerationID: generation.ID,
				View:         ConversationViewSummaryPrefixStripped,
				Messages:     generation.SummaryPrefixStrippedMessages(),
			})
		}
	}
	return candidates
}

// HighestFidelityCandidate returns the first candidate that satisfies the
// caller's fit predicate. If none fit, it returns the highest-fidelity
// deterministic fallback.
func (l ConversationLineage) HighestFidelityCandidate(fits func([]Message) bool) (ConversationCandidate, bool) {
	candidates := l.Candidates()
	if len(candidates) == 0 {
		return ConversationCandidate{}, false
	}
	if fits == nil {
		return candidates[0], true
	}
	for _, candidate := range candidates {
		if fits(candidate.Messages) {
			return candidate, true
		}
	}
	return candidates[0], true
}

// PruneObsolete is conservative by default and keeps all generations unless a
// caller has separately established a concrete pruning rule.
func (l ConversationLineage) PruneObsolete() ConversationLineage {
	return l.Clone()
}

// PruneGenerationsBefore drops generations whose IDs are strictly older than
// the supplied cutoff. Callers should only pass a cutoff after they have proven
// those generations are no longer needed for future recompaction.
func (l ConversationLineage) PruneGenerationsBefore(cutoffGenerationID int) ConversationLineage {
	if cutoffGenerationID <= 0 || len(l.Generations) == 0 {
		return l.Clone()
	}

	next := l.Clone()
	kept := next.Generations[:0]
	for _, generation := range next.Generations {
		if generation.ID >= cutoffGenerationID {
			kept = append(kept, generation)
		}
	}
	if len(kept) == len(next.Generations) {
		return next
	}
	next.Generations = kept
	return next
}

// RunState captures the mutable state of an in-flight run.
type RunState struct {
	TurnCount         int
	TokenCount        int
	InputTokens       int // uncached prompt tokens
	CacheReadTokens   int
	CacheCreateTokens int
	StopReason        StopReason
	Conversation      []Message
	Lineage           ConversationLineage
	Context           ContextState
	WorkflowHandoff   *tool.WorkflowHandoffTransition
}

// Clone returns a deep copy of the run state.
func (s RunState) Clone() RunState {
	return RunState{
		TurnCount:         s.TurnCount,
		TokenCount:        s.TokenCount,
		InputTokens:       s.InputTokens,
		CacheReadTokens:   s.CacheReadTokens,
		CacheCreateTokens: s.CacheCreateTokens,
		StopReason:        s.StopReason,
		Conversation:      cloneMessages(s.Conversation),
		Lineage:           s.Lineage.Clone(),
		Context:           s.Context.Clone(),
		WorkflowHandoff:   cloneWorkflowHandoffTransition(s.WorkflowHandoff),
	}
}

func cloneWorkflowHandoffTransition(transition *tool.WorkflowHandoffTransition) *tool.WorkflowHandoffTransition {
	if transition == nil {
		return nil
	}
	cloned := *transition
	return &cloned
}

// WithConversation returns a copy of the state with the active conversation
// replaced and a matching single-generation lineage.
func (s RunState) WithConversation(conversation []Message) RunState {
	next := s.Clone()
	next.Conversation = cloneMessages(conversation)
	next.Lineage = newConversationLineage(conversation)
	return next
}

// WithLineage returns a copy of the state with the lineage replaced.
func (s RunState) WithLineage(lineage ConversationLineage) RunState {
	next := s.Clone()
	next.Lineage = lineage.Clone()
	next.Conversation = lineage.FullMessages()
	return next
}

// WithContext returns a copy of the state with the durable context replaced.
func (s RunState) WithContext(context ContextState) RunState {
	next := s.Clone()
	next.Context = context.Clone()
	return next
}
