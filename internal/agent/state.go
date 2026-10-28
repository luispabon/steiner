package agent

import (
	"encoding/json"

	"github.com/luispabon/steiner/internal/tool"
)

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
	// StopReasonUsageLimit indicates the run stopped because the provider reported a usage or quota limit.
	StopReasonUsageLimit StopReason = "usage_limit"
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
//
// BackendState is opaque, provider-owned state used to resume the provider's
// transcript. It is stored per generation and never decoded by generic layers.
type ConversationGeneration struct {
	ID            int             `json:"id"`
	SummaryPrefix []Message       `json:"summary_prefix"`
	Messages      []Message       `json:"messages"`
	BackendState  json.RawMessage `json:"backend_state,omitempty"`
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
	out := newConversationGeneration(g.ID, g.SummaryPrefix, g.Messages)
	out.BackendState = cloneBackendState(g.BackendState)
	return out
}

// cloneBackendState returns a deep copy of state. Empty state normalizes to
// nil so it is omitted on marshal instead of failing it.
func cloneBackendState(state json.RawMessage) json.RawMessage {
	if len(state) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), state...)
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

// OnlyLatest returns a deep copy of the lineage that keeps only the newest
// generation. NextGenerationID is preserved.
func (l ConversationLineage) OnlyLatest() ConversationLineage {
	out := ConversationLineage{NextGenerationID: l.NextGenerationID}
	if latest, ok := l.Latest(); ok {
		out.Generations = []ConversationGeneration{latest}
	}
	return out
}

// FullMessages returns the full message view for the latest generation.
func (l ConversationLineage) FullMessages() []Message {
	if len(l.Generations) == 0 {
		return nil
	}
	return l.Generations[len(l.Generations)-1].FullMessages()
}

// SummaryPrefixStrippedMessages returns only the raw messages from the latest generation.
func (l ConversationLineage) SummaryPrefixStrippedMessages() []Message {
	if len(l.Generations) == 0 {
		return nil
	}
	return l.Generations[len(l.Generations)-1].SummaryPrefixStrippedMessages()
}

// latestMessages returns the raw messages of the newest generation without
// cloning them.
//
// The returned slice is NOT a clone: it shares backing storage with the
// lineage. Callers must not mutate it and must not retain it beyond the call;
// use FullMessages or SummaryPrefixStrippedMessages when a defensive copy is
// needed.
func (l ConversationLineage) latestMessages() []Message {
	if len(l.Generations) == 0 {
		return nil
	}
	return l.Generations[len(l.Generations)-1].Messages
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

// latestGenerationID returns the ID of the newest generation, or 0 when the
// lineage is empty.
func (l ConversationLineage) latestGenerationID() int {
	if len(l.Generations) == 0 {
		return 0
	}
	return l.Generations[len(l.Generations)-1].ID
}

// latestBackendState returns a copy of the newest generation's backend state.
func (l ConversationLineage) latestBackendState() json.RawMessage {
	if len(l.Generations) == 0 {
		return nil
	}
	return cloneBackendState(l.Generations[len(l.Generations)-1].BackendState)
}

// withBackendState returns a copy with the backend state of the generation
// identified by id updated. clearState removes the stored state and takes
// precedence over state. A nil state leaves the stored state unchanged. An
// unknown id, or an unchanged state, returns l itself rather than a copy.
func (l ConversationLineage) withBackendState(id int, state json.RawMessage, clearState bool) ConversationLineage {
	if !clearState && len(state) == 0 {
		return l
	}
	for i := range l.Generations {
		if l.Generations[i].ID != id {
			continue
		}
		next := l.Clone()
		if clearState {
			next.Generations[i].BackendState = nil
		} else {
			next.Generations[i].BackendState = cloneBackendState(state)
		}
		return next
	}
	return l
}

// WithoutLatestBackendState returns l with the latest generation's backend state
// cleared.
func (l ConversationLineage) WithoutLatestBackendState() ConversationLineage {
	return l.withBackendState(l.latestGenerationID(), nil, true)
}

// WithLatestBackendState returns a copy with the latest generation's backend
// state set to a deep copy of state. A nil or empty state clears it.
func (l ConversationLineage) WithLatestBackendState(state json.RawMessage) ConversationLineage {
	next := l.Clone()
	if len(next.Generations) == 0 {
		return next
	}
	next.Generations[len(next.Generations)-1].BackendState = cloneBackendState(state)
	return next
}

// WithNewGenerationState is WithNewGeneration with the new generation's backend
// state set to a deep copy of state. A nil or empty state adds it without
// backend state.
func (l ConversationLineage) WithNewGenerationState(summaryPrefix, messages []Message, state json.RawMessage) ConversationLineage {
	next := l.WithNewGeneration(summaryPrefix, messages)
	next.Generations[len(next.Generations)-1].BackendState = cloneBackendState(state)
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

// RunState captures the mutable state of an in-flight run.
type RunState struct {
	TurnCount           int
	TokenCount          int
	InputTokens         int // uncached prompt tokens
	CacheReadTokens     int
	CacheCreateTokens   int
	StopReason          StopReason
	Conversation        []Message
	Lineage             ConversationLineage
	Context             ContextState
	WorkflowHandoff     *tool.WorkflowHandoffTransition
	BudgetNoticesIssued int
	BudgetStartTurn     int
	// ParentUsage is the last successful parent model response's usage. It is
	// in-memory only and is not part of the persisted lineage.
	ParentUsage *ParentUsage
}

// Clone returns a deep copy of the run state.
func (s RunState) Clone() RunState {
	return RunState{
		TurnCount:           s.TurnCount,
		TokenCount:          s.TokenCount,
		InputTokens:         s.InputTokens,
		CacheReadTokens:     s.CacheReadTokens,
		CacheCreateTokens:   s.CacheCreateTokens,
		StopReason:          s.StopReason,
		Conversation:        cloneMessages(s.Conversation),
		Lineage:             s.Lineage.Clone(),
		Context:             s.Context.Clone(),
		WorkflowHandoff:     cloneWorkflowHandoffTransition(s.WorkflowHandoff),
		BudgetNoticesIssued: s.BudgetNoticesIssued,
		BudgetStartTurn:     s.BudgetStartTurn,
		ParentUsage:         s.ParentUsage.Clone(),
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

// LineageWithoutImageData returns a deep copy of lineage with every image
// payload stripped from each generation's summary prefix and messages. The
// input lineage is not modified. Callers persist the result so no image bytes
// reach disk.
func LineageWithoutImageData(lineage ConversationLineage) ConversationLineage {
	out := lineage.Clone()
	for i := range out.Generations {
		if len(out.Generations[i].SummaryPrefix) > 0 {
			out.Generations[i].SummaryPrefix = stripImagesFromMessages(out.Generations[i].SummaryPrefix, VisionUnknown, false)
		}
		if len(out.Generations[i].Messages) > 0 {
			out.Generations[i].Messages = stripImagesFromMessages(out.Generations[i].Messages, VisionUnknown, false)
		}
	}
	return out
}

// WithContext returns a copy of the state with the durable context replaced.
func (s RunState) WithContext(context ContextState) RunState {
	next := s.Clone()
	next.Context = context.Clone()
	return next
}
