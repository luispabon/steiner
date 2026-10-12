package agent

import (
	"fmt"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// observeStatefulUsage updates the stateful compaction guard from a finished
// model response. It does nothing for providers without stateful compaction.
//
// The first response after a successful compaction decides the guard. Usage
// below the compaction threshold arms nothing. Usage from the threshold up to
// the cap arms statefulFloor at that PromptTokens. Usage at or above the cap
// sets statefulCapReached. Later responses release an armed floor once usage
// reaches its release point.
func (p *turnProgressor) observeStatefulUsage(usage *provider.UsageStats) {
	if !provider.SupportsStatefulCompaction(p.request.Provider) {
		return
	}
	contextSize := p.request.ModelBudget.ContextSize
	if p.awaitingPostCompaction {
		p.awaitingPostCompaction = false
		switch {
		case statefulUsageReaches(usage, compactionPercentTokens(contextSize, compactionCapUsagePercent)):
			p.statefulCapReached = true
		case statefulUsageAboveThreshold(usage, contextSize):
			p.statefulFloor = usage.PromptTokens
		}
		return
	}
	if p.statefulFloor > 0 && statefulUsageReaches(usage, statefulReleaseTokens(p.statefulFloor, contextSize)) {
		p.statefulFloor = 0
	}
}

// statefulCapStop ends the run at a compaction decision after the cap was
// reached. Compacting again cannot bring usage below the cap, so the compactor
// is not called.
func (p *turnProgressor) statefulCapStop(state RunState, fit prompt.RequestTokenBudget) turnOutcome {
	emitEvent(p.request.Events, output.NewContextSessionHealthEvent("conversation", state.TurnCount+1, p.compactionCount, "critical", "likely_lossy", compactionRestartGuidanceCritical,
		"provider prompt usage stayed at or above the compaction cap after compaction; compaction stopped"))
	return turnOutcome{State: state, Error: fmt.Errorf("request exceeds context window: %s", fit.String()), Stop: true}
}

// statefulReleaseTokens returns the prompt tokens at which an armed floor
// releases the soft trigger: the floor plus the hysteresis band, capped at the
// compaction cap.
func statefulReleaseTokens(floor, contextSize int) int {
	return min(
		floor+compactionPercentTokens(contextSize, compactionHysteresisPercent),
		compactionPercentTokens(contextSize, compactionCapUsagePercent),
	)
}

// compactionPercentTokens returns percent of contextSize rounded up. Prompt
// tokens are integers, so a count reaches the rounded value exactly when it
// reaches the percentage.
func compactionPercentTokens(contextSize, percent int) int {
	return (contextSize*percent + 99) / 100
}

// statefulUsageReaches reports whether usage's PromptTokens is at least tokens.
// A nil usage or a non-positive tokens never reaches.
func statefulUsageReaches(usage *provider.UsageStats, tokens int) bool {
	return usage != nil && tokens > 0 && usage.PromptTokens >= tokens
}
