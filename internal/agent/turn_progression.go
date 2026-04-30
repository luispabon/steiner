package agent

import "context"

// turnProgressor owns the per-turn progression lifecycle.
//
// Future stages will extract prompt assembly, model calls, tool execution,
// compaction, and event emission into this type.
type turnProgressor struct {
	runner *Runner
}

func newTurnProgressor(runner *Runner) *turnProgressor {
	return &turnProgressor{runner: runner}
}

// advance runs a single turn by delegating to the existing runner method.
//
// Stage 1: pure delegation with no behavioral changes.
func (p *turnProgressor) advance(ctx context.Context, in turnInput) turnOutcome {
	state, err := p.runner.runTurn(
		ctx,
		in.Request,
		in.State,
		in.BasePrompt,
		in.CompactionHistory,
		in.CompactionCount,
	)
	if err != nil {
		return turnOutcome{State: state, Error: err, Stop: true}
	}
	if state.StopReason != "" {
		return turnOutcome{State: state, Stop: true}
	}
	return turnOutcome{State: state, Retry: true}
}
