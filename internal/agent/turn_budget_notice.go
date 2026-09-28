package agent

import "math"

var turnBudgetNoticeFractions = []float64{0.5, 0.75, 0.9}

// injectTurnBudgetNoticeIfDue appends one notice when state crosses one or
// more turn-budget thresholds. Thresholds are measured from the run's start
// turn (BudgetStartTurn), so a follow-up run that resumes with turns already
// used checkpoints against its own fresh budget rather than the cumulative
// MaxTurns cap. It does nothing when notices are disabled, the run's fresh
// budget is non-positive, or every threshold has fired.
func injectTurnBudgetNoticeIfDue(state RunState, req RunRequest) RunState {
	start := state.BudgetStartTurn
	budget := req.Limits.MaxTurns - start
	if req.TurnBudgetNotice == nil || budget <= 0 || state.BudgetNoticesIssued >= len(turnBudgetNoticeFractions) {
		return state
	}

	crossed := 0
	for _, fraction := range turnBudgetNoticeFractions {
		if state.TurnCount >= start+int(math.Ceil(fraction*float64(budget))) {
			crossed++
		}
	}
	if crossed <= state.BudgetNoticesIssued {
		return state
	}

	message := Message{
		Role:    MessageRoleUser,
		Content: req.TurnBudgetNotice(state.TurnCount-start, budget),
		Turn:    state.TurnCount,
	}
	state.Lineage = state.Lineage.WithAppendedMessages([]Message{message})
	state.Conversation = state.Lineage.FullMessages()
	state.BudgetNoticesIssued = crossed
	return state
}
