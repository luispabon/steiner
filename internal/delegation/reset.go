package delegation

// ResetForNewConversation resets all conversation-scoped delegation state:
// child-session bookkeeping and advisor budgets. The agent ID counter is
// deliberately NOT reset: IDs derive kept worktree paths and branches, so they
// must stay unique process-wide.
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
}
