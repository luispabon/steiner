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
}
