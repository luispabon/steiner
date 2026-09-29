package agent

import "errors"

// ErrEpisodeBudgetExhausted is returned by WaitQuiescent when the episode token
// budget ran out while sub-agents are still pending. The model is never told;
// later results are recorded quietly.
var ErrEpisodeBudgetExhausted = errors.New("episode token budget exhausted with sub-agents pending")

// An episode runs from a user prompt until the driver goes idle. Wakes caused
// by completions share the episode's completion-token budget
// (DriverOptions.MaxTokensPerEpisode, 0 = unlimited). A user prompt always
// starts a fresh episode.

// resetEpisodeLocked starts a new episode.
func (d *ConversationDriver) resetEpisodeLocked() {
	d.episodeUsed = 0
	d.exhausted = false
}

// budgetSpentLocked reports whether a limited budget has no tokens left.
func (d *ConversationDriver) budgetSpentLocked() bool {
	return d.opts.MaxTokensPerEpisode > 0 && d.episodeUsed >= d.opts.MaxTokensPerEpisode
}

// runMaxTokensLocked is the MaxTokens for the next run: the remaining budget,
// or 0 (host default) when the budget is unlimited. Callers check
// budgetSpentLocked first, so a limited budget never yields 0.
func (d *ConversationDriver) runMaxTokensLocked() int {
	if d.opts.MaxTokensPerEpisode <= 0 {
		return 0
	}
	return d.opts.MaxTokensPerEpisode - d.episodeUsed
}

// recordRunLocked charges a finished run to the episode and marks the budget
// exhausted when it ran out with sub-agent results still to come.
func (d *ConversationDriver) recordRunLocked(out DriverRunOutput) {
	d.episodeUsed += out.TokenCount
	if (out.StopReason == StopReasonMaxTokens || d.budgetSpentLocked()) && d.hasPendingLocked() {
		d.exhausted = true
	}
}

// budgetBlockedLocked reports an exhausted budget with children still running
// and no buffered result about to be settled.
func (d *ConversationDriver) budgetBlockedLocked() bool {
	return d.exhausted && d.state != DriverGenerating && len(d.completions) == 0 &&
		d.opts.Background != nil && d.opts.Background.HasPending()
}
