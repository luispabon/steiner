package agent

import "strings"

const turnBudgetNoticeFraction = 0.7

// turnBudgetNoticeMarker prefixes the injected notice's content so a later
// call can find and replace it by content match. This is NOT done via
// Message.Source (an existing tagging field) because Source does not survive
// the agent.Message <-> provider.Message round trip (see toProviderMessage /
// fromProviderMessage in message_convert.go — Source is silently dropped),
// and the delegation extension loop in internal/delegation/task.go rebuilds
// state.Conversation from a provider.Message round trip between runs
// (req.Prompt.Conversation = agent.ToProviderMessages(state.Conversation),
// then the next Run() call's initializeRunState reconstructs via
// fromProviderMessages). A Source-tagged message would silently lose its tag
// at exactly the extension boundary this mechanism has to work across.
// Content is the only field guaranteed to survive that round trip, hence the
// marker-in-content approach.
const turnBudgetNoticeMarker = "[turn-budget-checkpoint]"

// injectTurnBudgetNoticeIfDue checks whether state has crossed
// turnBudgetNoticeFraction of req.Limits.MaxTurns and, if so and it hasn't
// already fired this run, injects (or — across an extension boundary, where
// the previous run's notice message is still present in the carried-over
// conversation — supersedes in place) a tagged notice message built by
// req.TurnBudgetNotice. No-op when req.TurnBudgetNotice is nil, MaxTurns <= 0,
// or the notice already fired this run (state.BudgetNoticeIssued).
func injectTurnBudgetNoticeIfDue(state RunState, req RunRequest) RunState {
	if req.TurnBudgetNotice == nil || req.Limits.MaxTurns <= 0 || state.BudgetNoticeIssued {
		return state
	}
	threshold := int(float64(req.Limits.MaxTurns) * turnBudgetNoticeFraction)
	if state.TurnCount < threshold {
		return state
	}
	text := turnBudgetNoticeMarker + " " + req.TurnBudgetNotice(state.TurnCount, req.Limits.MaxTurns)
	messages := supersedeOrAppendByContentPrefix(state.Lineage.SummaryPrefixStrippedMessages(), turnBudgetNoticeMarker, Message{
		Role:    MessageRoleUser,
		Content: text,
		Turn:    state.TurnCount,
	})
	state.Lineage = state.Lineage.WithCurrentMessages(messages)
	state.Conversation = state.Lineage.FullMessages()
	state.BudgetNoticeIssued = true
	return state
}

func supersedeOrAppendByContentPrefix(messages []Message, prefix string, replacement Message) []Message {
	for i, m := range messages {
		if strings.HasPrefix(m.Content, prefix) {
			out := append([]Message(nil), messages...)
			out[i] = replacement
			return out
		}
	}
	return append(append([]Message(nil), messages...), replacement)
}
