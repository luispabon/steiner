package agent

type StopReason string

const (
	StopReasonMaxTurns  StopReason = "max_turns"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonCancelled StopReason = "cancelled"
	StopReasonComplete  StopReason = "complete"
	StopReasonError     StopReason = "error"
)

type RunState struct {
	TurnCount    int
	TokenCount   int
	StopReason   StopReason
	Conversation []Message
	Context      ContextState
}

// Clone returns a deep copy of the run state.
func (s RunState) Clone() RunState {
	return RunState{
		TurnCount:    s.TurnCount,
		TokenCount:   s.TokenCount,
		StopReason:   s.StopReason,
		Conversation: cloneMessages(s.Conversation),
		Context:      s.Context.Clone(),
	}
}

// WithConversation returns a copy of the state with the conversation replaced.
func (s RunState) WithConversation(conversation []Message) RunState {
	next := s.Clone()
	next.Conversation = cloneMessages(conversation)
	return next
}

// WithContext returns a copy of the state with the durable context replaced.
func (s RunState) WithContext(context ContextState) RunState {
	next := s.Clone()
	next.Context = context.Clone()
	return next
}
