package delegation

// ResetForNewConversation resets all conversation-scoped delegation state:
// child-session bookkeeping, advisor budgets, and the agent ID counter.
// Call on conversation boundaries that discard the prior conversation
// (currently: /clear). Do NOT call on fork or session-picker load — those
// preserve or restore child continuity, and resetting there can collide
// with IDs that follow_ups still reference.
func ResetForNewConversation(sessions *SessionStore, budgets *AdvisorBudgetStore) {
	if sessions != nil {
		sessions.Reset()
	}
	if budgets != nil {
		budgets.Reset()
	}
	ResetAgentCounter()
}
