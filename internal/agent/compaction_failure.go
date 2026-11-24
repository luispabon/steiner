package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// Summary failure reasons. The first three classify a reply with no summary text.
// summaryFailRetryNoFit reports a tail-only retry that the budget would not send.
const (
	summaryFailToolCalls  = "tool_calls"
	summaryFailNoText     = "no_text"
	summaryFailLength     = "length"
	summaryFailRetryNoFit = "retry_does_not_fit"
)

// compactionSummaryError reports a summary stage that produced no summary text.
// Attempts counts the summary calls the stage made, and ToolCalls the tool calls
// of the last reply. It wraps no provider error, so provider.RetryableProviderError
// never matches it and the run loop does not retry the turn.
type compactionSummaryError struct {
	Reason    string
	Attempts  int
	ToolCalls int
	// Details lists every reply of the stage in order, with its classification.
	Details []output.CompactionSummaryAttempt
}

// Error describes the failure by its reason.
func (e *compactionSummaryError) Error() string {
	switch e.Reason {
	case summaryFailLength:
		return "compaction summary is empty: the model hit its output limit before writing text, and the same limit would fail the same way on retry"
	case summaryFailToolCalls:
		return fmt.Sprintf("compaction summary is empty: the model returned %d tool call(s) and no text after %d attempt(s)", e.ToolCalls, e.Attempts)
	case summaryFailRetryNoFit:
		return fmt.Sprintf("compaction summary retry does not fit the summary budget after %d attempt(s)", e.Attempts)
	default:
		return fmt.Sprintf("compaction summary is empty: the model returned no text after %d attempt(s)", e.Attempts)
	}
}

// compactionFailureReported marks an error whose failure diagnostic the agent
// already emitted, so the manual compaction path does not emit a second one.
type compactionFailureReported struct{ err error }

func (e *compactionFailureReported) Error() string { return e.err.Error() }

func (e *compactionFailureReported) Unwrap() error { return e.err }

// CompactionFailureReported reports whether the agent already emitted the failure
// diagnostic of the compaction that returned err.
func CompactionFailureReported(err error) bool {
	var reported *compactionFailureReported
	return errors.As(err, &reported)
}

// reportCompactionFailure emits the failure diagnostic of a compaction that
// adopted no state and marks err as reported. Cancellation is not reported: the
// caller closes that run with its own skipped event. With no sink nothing is
// emitted, so err is returned unmarked and the caller still closes the run.
func reportCompactionFailure(sink output.EventSink, turn int, stages []output.ContextCompactionStage, err error) error {
	if err == nil || sink == nil || errors.Is(err, context.Canceled) {
		return err
	}
	emitCompactionFailedDiagnostics(sink, turn, stages, err)
	return &compactionFailureReported{err: err}
}

// emitCompactionFailedDiagnostics emits the one failure diagnostic of a compaction
// that adopted no state. Its skipped severity clears the compacting state, and its
// compaction_result is explicit, unlike the token budget status. An empty summary
// carries its attempts, reason and tool-call count. A newest unit that does not
// fit the main budget also appends its failed emergency stage with the breakdown
// the error measured.
func emitCompactionFailedDiagnostics(sink output.EventSink, turn int, stages []output.ContextCompactionStage, err error) {
	payload := output.ContextDiagnosticsEvent{
		Kind:  "compaction",
		Scope: "conversation",
		Turn:  turn,
		// "skipped" keeps the TUI on its skipped banner: other non-compacting severities render as a finished compaction and reset the budget.
		Severity:         "skipped",
		CompactionResult: output.CompactionResultFailed,
		Notes:            []string{"compaction failed: " + err.Error()},
	}
	var tooLarge *compactionTailTooLargeError
	if errors.As(err, &tooLarge) {
		stages = append(stages, output.ContextCompactionStage{
			Stage:           string(prompt.CompactionModeEmergency),
			RetainedUnits:   emergencyCompactionRetainTurns,
			RetainedTokens:  tooLarge.RetainedTokens,
			ProtectedTokens: tooLarge.ProtectedTokens,
			Failed:          true,
		})
		payload.Notes = []string{"compaction failed: the newest unit does not fit the main budget"}
	}
	var summaryErr *compactionSummaryError
	if errors.As(err, &summaryErr) {
		payload.SummaryAttempts = summaryErr.Attempts
		payload.SummaryFailureReason = summaryErr.Reason
		payload.SummaryToolCalls = summaryErr.ToolCalls
		payload.SummaryAttemptDetails = summaryErr.Details
	}
	payload.Stages = stages
	emitEvent(sink, output.NewContextDiagnosticsEvent(payload))
}
