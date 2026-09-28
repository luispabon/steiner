package agent

import "math"

var turnBudgetNoticeFractions = []float64{0.5, 0.75, 0.9}

// injectTurnBudgetNoticeIfDue appends one notice when state crosses one or
// more turn-budget thresholds. It does nothing when notices are disabled, the
// turn budget is non-positive, or every threshold has fired.
func injectTurnBudgetNoticeIfDue(state RunState, req RunRequest) RunState {
	if req.TurnBudgetNotice == nil || req.Limits.MaxTurns <= 0 || state.BudgetNoticesIssued >= len(turnBudgetNoticeFractions) {
		return state
	}

	crossed := 0
	for _, fraction := range turnBudgetNoticeFractions {
		if state.TurnCount >= int(math.Ceil(fraction*float64(req.Limits.MaxTurns))) {
			crossed++
		}
	}
	if crossed <= state.BudgetNoticesIssued {
		return state
	}

	message := Message{
		Role:    MessageRoleUser,
		Content: req.TurnBudgetNotice(state.TurnCount, req.Limits.MaxTurns),
		Turn:    state.TurnCount,
	}
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{message})
	state.Conversation = state.Lineage.FullMessages()
	state.BudgetNoticesIssued = crossed
	return state
}
