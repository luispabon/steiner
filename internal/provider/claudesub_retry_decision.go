package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Recovery of failed claude_subscription turns. A delivery attempt that fails
// before any output and without a user stop is classified here, so one set of
// rules decides whether a parent call replaces the process and delivers the
// request again. A failure after the caller already saw output is never
// recovered: the call returns the error with the partial kept, and the next
// request resumes the stored checkpoint (see the stopped-turn recovery in
// claudesub_attach.go). Live probe runs showed the CLI does not join a continued
// reply reliably, losing a line break when the cut fell after a whole line, so
// automatic continuation is rejected. Advisor calls, in-place summaries and
// summary helpers never recover.

// claudeSubRetryAction is what a call does after a failed delivery attempt.
type claudeSubRetryAction int

const (
	// claudeSubRetryNone ends the call with the failure.
	claudeSubRetryNone claudeSubRetryAction = iota
	// claudeSubRetryPreOutput replaces the process and delivers the request
	// again, because the failure happened before any output reached the caller.
	claudeSubRetryPreOutput
)

// String names the action for logs and diagnostics. It is the recovery label a
// call reports, or "none".
func (a claudeSubRetryAction) String() string {
	if label := claudeSubActionLabel(a); label != "" {
		return label
	}
	return "none"
}

// claudeSubActionLabel names a recovery action in the call diagnostics, or
// returns the empty string when the action recovers nothing.
func claudeSubActionLabel(action claudeSubRetryAction) string {
	if action == claudeSubRetryPreOutput {
		return claudeSubRecoveryRetryPreOutput
	}
	return ""
}

// claudeSubRetryMaxRecoveries bounds the recovery attempts one provider call
// spends.
const claudeSubRetryMaxRecoveries = 2

// claudeSubRetryFacts are the per-attempt facts the retry decision reads. Every
// field describes one failed delivery attempt, so the decision is pure and can
// be tested without a process.
type claudeSubRetryFacts struct {
	// CallKind is claudeSubCallKind of the request. Only parent and sub-agent
	// calls recover.
	CallKind string
	// ContextErr is ctx.Err() of the call. A stopped or expired caller is never
	// recovered: the user asked for the turn to end.
	ContextErr error
	// Visible reports that assistant text or thinking of this attempt already
	// reached the caller. A visible failure is never recovered, and thinking
	// alone counts: no partial is preserved for it, so a retry would lose it.
	Visible bool
	// CLIRetried reports that the CLI retried its own API request in this
	// attempt. Its retry budget is then spent, so a new attempt would only
	// repeat the wait.
	CLIRetried bool
	// Pending reports that a tool call of this attempt is still in flight: a
	// streamed tool_use registration without a result, or a host call the CLI
	// opened. A recovery never re-runs or re-requests a tool call.
	Pending bool
	// Restorable reports that the request carries a stored checkpoint the retry
	// can fork-resume. A retry never reseeds: a fresh process or a sub-agent has
	// nothing to resume, so a failure there ends the call.
	Restorable bool
	// Recoveries is the number of recovery attempts this call already spent.
	Recoveries int
}

// claudeSubRetryDecision returns what the call does after err failed one
// delivery attempt.
//
// A retry needs every one of these: an error, a live caller, a parent call whose
// request carries a restorable checkpoint, no output already streamed, no CLI
// retry already spent, no tool call in flight, fewer than
// claudeSubRetryMaxRecoveries attempts, and typed evidence that the CLI
// connection ended, either the closed-connection error or the crash result the
// CLI synthesises. Everything else ends the call with the failure: a user stop, a
// visible failure, an ambiguous error from a process that may still be alive, a
// mid-response API error (the CLI already exhausted its own retries), an aborted
// result, an error subtype the CLI reported on purpose, a usage or gate refusal,
// a permanent refusal, a decode or envelope failure, an oversized line, and an
// unsafe session.
func claudeSubRetryDecision(err error, facts claudeSubRetryFacts) claudeSubRetryAction {
	if err == nil || facts.Recoveries >= claudeSubRetryMaxRecoveries {
		return claudeSubRetryNone
	}
	if facts.ContextErr != nil || !claudeSubRecoversCallKind(facts.CallKind) || !facts.Restorable {
		return claudeSubRetryNone
	}
	if facts.Visible {
		// The caller already holds part of the reply, so a second attempt would
		// discard it or duplicate it. The error is surfaced instead and the next
		// request resumes the stored checkpoint.
		return claudeSubRetryNone
	}
	if facts.CLIRetried || facts.Pending || claudeSubRetryRefused(err) {
		return claudeSubRetryNone
	}
	if failure, ok := claudeSubFailedTurnResult(err); ok {
		if failure.subtype != claudeSubCrashResultSubtype {
			// Only a crash takes this path. An aborted result, error_max_turns and
			// every other error subtype is the CLI ending the turn on purpose.
			return claudeSubRetryNone
		}
		if failure.terminalReason == "" {
			// A crash the CLI reported as a result: it carries no terminal reason,
			// because the turn did not end in any state it could name.
			return claudeSubRetryPreOutput
		}
		// api_error means the CLI already exhausted its own retries for this
		// request, so a new attempt would only repeat the wait. Every other named
		// end is deliberate.
		return claudeSubRetryNone
	}
	if errors.Is(err, errClaudeSubConnClosed) {
		// Typed evidence that the connection ended: the event queue closed without
		// a terminal result, or a write to the CLI failed on a closed connection.
		return claudeSubRetryPreOutput
	}
	// Any other error may come from a process that is still alive (a control
	// request timeout, a gate transport failure, a writer error), so it is not
	// safe to send the request again.
	return claudeSubRetryNone
}

// claudeSubCrashResultSubtype is the result subtype a crashed CLI reports. It is
// also the subtype an interrupted turn reports, which the terminal reason or the
// interruption marker tells apart (see claudeSubDrainAborted).
const claudeSubCrashResultSubtype = "error_during_execution"

// claudeSubInterruptedResultText is the marker the CLI writes into the result of
// a turn it interrupted. A CLI version that publishes no terminal reason still
// writes it, so an interrupted result is never mistaken for a crash.
const claudeSubInterruptedResultText = "[Request interrupted by user"

// claudeSubRecoversCallKind reports whether a call of this kind recovers a failed
// turn. An advisor request is out-of-band and has no reply the caller is waiting
// for, and a summary is auxiliary: neither retries anything. A summary helper is
// ephemeral and holds no conversation.
func claudeSubRecoversCallKind(kind string) bool {
	switch kind {
	case claudeSubCallKindNormal, claudeSubCallKindSubAgent:
		return true
	default:
		return false
	}
}

// claudeSubRetryRefused reports whether err is a refusal no recovery can fix.
// The refusal has to stay distinguishable from a crash: a usage gate spent its
// poll budget before refusing, a usage limit stops the turn, paid extra usage and
// an unrecognised rate-limit status fail the session closed, an unsafe session
// cannot prove which call its events belong to, a decode or envelope failure may
// leave stale events queued, an oversized stdin line is rejected on every
// attempt, an oversized stdout line did not reach steiner and fails closed, and a
// permanent refusal is the answer the server gave.
func claudeSubRetryRefused(err error) bool {
	var gate *claudeSubUsageGateError
	if errors.As(err, &gate) {
		return true
	}
	if _, limited := AsUsageLimit(err); limited {
		return true
	}
	if failure, ok := claudeSubFailedTurnResult(err); ok && strings.Contains(failure.text, claudeSubInterruptedResultText) {
		// The CLI reported a turn someone interrupted. A user stop is never
		// recovered, even on a CLI version that names no terminal reason for it.
		return true
	}
	if failure, ok := claudeSubFailedTurnResult(err); ok && failure.terminalReason == "" && claudeSubAPIFailureTextMatch(failure.text) {
		// A result with no terminal reason can be a crash or an API failure the
		// CLI reported without naming it. Its own API failure wording is ambiguous,
		// so the turn fails closed instead of being sent again.
		return true
	}
	if errors.Is(err, errClaudeSubPaidExtraUsage) || errors.Is(err, errClaudeSubUnrecognisedStatus) {
		return true
	}
	if errors.Is(err, errClaudeSubUsageNotReady) || errors.Is(err, errClaudeSubUsageCreditsEnabled) {
		return true
	}
	if errors.Is(err, errClaudeSubSessionUnsafe) || errors.Is(err, errClaudeSubUnconsumedTurn) || errors.Is(err, errClaudeSubPoolClosed) {
		return true
	}
	if errors.Is(err, errClaudeSubDecodeStream) || errors.Is(err, errClaudeSubResultEnvelope) {
		return true
	}
	if errors.Is(err, bufio.ErrTooLong) {
		// An oversized stdout line never reached Steiner, so the turn cannot be
		// replayed from what it saw. Fail closed instead of retrying a framing
		// failure the CLI will reproduce.
		return true
	}
	var tooLarge *claudeSubLineTooLargeError
	if errors.As(err, &tooLarge) {
		return true
	}
	return claudeSubPermanentFailureTextMatch(err)
}

// claudeSubPermanentFailureText lists the failure text of a permanent refusal.
// A matching failure is never recovered: replacing the process cannot change the
// answer.
var claudeSubPermanentFailureText = []string{
	"401", "403", "authentication", "billing", "credit", "permission", "invalid_request",
}

// claudeSubPermanentFailureTextMatch reports whether err's structured text names
// a permanent refusal. A failed turn contributes only its result text, which the
// CLI wrote into the result envelope; every other error contributes only its safe
// form, which excludes the stderr tail and control-response text. An incidental
// word in stderr therefore never blocks a legitimate retry. The match is
// case-insensitive and only ever stops a recovery.
func claudeSubPermanentFailureTextMatch(err error) bool {
	var text string
	if failure, ok := claudeSubFailedTurnResult(err); ok {
		text = failure.text
	} else {
		text = claudeSubDiagnosticError(err).Error()
	}
	text = strings.ToLower(text)
	for _, want := range claudeSubPermanentFailureText {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

// claudeSubAPIFailureMarkers lists the CLI's own API failure wording. A turn that
// reports it without a terminal reason may have exhausted the CLI's own retries,
// so it is never sent again.
var claudeSubAPIFailureMarkers = []string{"api error", "server error mid-response"}

// claudeSubAPIFailureTextMatch reports whether text carries the CLI's API
// failure wording. The match is case-insensitive.
func claudeSubAPIFailureTextMatch(text string) bool {
	lower := strings.ToLower(text)
	for _, want := range claudeSubAPIFailureMarkers {
		if strings.Contains(lower, want) {
			return true
		}
	}
	return false
}

// claudeSubConnError types a connection failure exactly once: an error that
// already carries errClaudeSubConnClosed is returned unchanged, a line the CLI
// refused keeps its own type, and anything else is wrapped as a closed
// connection.
func claudeSubConnError(err error) error {
	if errors.Is(err, errClaudeSubConnClosed) {
		return err
	}
	var tooLarge *claudeSubLineTooLargeError
	if errors.As(err, &tooLarge) {
		return err
	}
	return fmt.Errorf("%w: %w", errClaudeSubConnClosed, err)
}

// claudeSubFailedTurnResult returns the CLI result facts of a failed turn.
func claudeSubFailedTurnResult(err error) (*claudeSubFailedTurnError, bool) {
	var failure *claudeSubFailedTurnError
	if !errors.As(err, &failure) {
		return nil, false
	}
	return failure, true
}

// Recovery backoff. Recovery i waits claudeSubRetryBackoff[i] (the last entry
// repeats) before its attempt. Tests shorten it.
var claudeSubRetryBackoff = []time.Duration{time.Second, 3 * time.Second}

// claudeSubRecoveryDelay returns the wait before one recovery attempt.
func claudeSubRecoveryDelay(attempt int) time.Duration {
	if attempt < 1 || len(claudeSubRetryBackoff) == 0 {
		return 0
	}
	if attempt > len(claudeSubRetryBackoff) {
		attempt = len(claudeSubRetryBackoff)
	}
	return claudeSubRetryBackoff[attempt-1]
}

// claudeSubAwaitRecovery waits d, ending early when the caller stops so a user
// stop during a recovery wait returns promptly.
func claudeSubAwaitRecovery(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// claudeSubRecoveryNotice reports one recovery attempt to the caller as a
// warning diagnostic with the attempt, the bound and the wait that preceded it.
// It carries no assistant content, so it is never visible output.
func claudeSubRecoveryNotice(err error, attempt int, delay time.Duration) ChatChunk {
	return ChatChunk{
		Diagnostic: fmt.Sprintf("claude_subscription turn failed (%s); retrying, attempt %d/%d",
			claudeSubRecoveryReason(err), attempt, claudeSubRetryMaxRecoveries),
		Severity:         "warning",
		RetryAttempt:     attempt,
		RetryMaxAttempts: claudeSubRetryMaxRecoveries,
		RetryDelay:       delay,
	}
}

// claudeSubRecoveryReason names a failure in one short phrase for the recovery
// notice. It names the CLI's own end state when the turn reported one, so the
// notice never guesses.
func claudeSubRecoveryReason(err error) string {
	if failure, ok := claudeSubFailedTurnResult(err); ok {
		if failure.terminalReason != "" {
			return failure.terminalReason
		}
		if failure.subtype != "" {
			return failure.subtype
		}
	}
	if errors.Is(err, errClaudeSubConnClosed) {
		return "connection closed"
	}
	return "process exit"
}
