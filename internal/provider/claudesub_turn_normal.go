package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// claudeSubNormalTurn runs a parent or sub-agent request. A process is reused
// only while its record matches the request (see claudeSubAttachSession), and a
// process a cancelled delivery left behind is replaced before it can deliver
// anything.
//
// A failed attempt is retried at most claudeSubRetryMaxRecoveries times, as
// claudeSubRetryDecision decides: only before any output reached the caller, and
// only when the request carries a stored checkpoint the retry can fork-resume. A
// retry never reseeds a fresh conversation, and its metric, log line and notice
// are recorded only after its attach forked the stored checkpoint. A failure
// after output, and every failure the decision does not qualify, ends the call
// with its error, so the caller keeps the partial it already streamed and the
// next request resumes the stored checkpoint.
func claudeSubNormalTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	fingerprint, err := claudeSubFingerprint(spec.SystemPrompt, spec.Tools)
	if err != nil {
		return err
	}
	a := claudeSubAttach{key: key, spec: spec, fingerprint: fingerprint, msgs: claudeSubMessages(req.Messages)}
	a.stored, a.hasState = claudeSubParseState(req.BackendState)
	a.partial = -1
	var restoreReason string
	a.resume, a.restorable, restoreReason, err = claudeSubRestoreFor(a.msgs, a.stored, a.hasState, key, fingerprint)
	if err != nil {
		return err
	}
	if a.hasState {
		// A stopped turn leaves its kept partial in the request suffix, after the
		// stored checkpoint the stopped delivery was meant to extend.
		if partial, ok := claudeSubInterruptedTail(a.msgs, a.stored.SyncEntries); ok {
			a.partial = partial
		}
	}
	if !a.restorable {
		claudeSubMetricsFrom(ctx).noteReseed(restoreReason)
	}
	// warned records that an attempt of this call already reported a recovery
	// warning, so a retry never repeats it. noticeReported records that the
	// warning covered the pool's pending replacement notice, which this call then
	// clears when it succeeds even if the attempt that succeeded reported nothing.
	warned := false
	noticeReported := false
	recoveries := 0
	// recoveryErr is the failure a scheduled retry is recovering from. A retry
	// that cannot fork the stored checkpoint ends the call with it.
	var recoveryErr error
	// pendingAction and pendingDelay are the retry this call is waiting on: the
	// action it takes and the wait it spent. They are reported only once the
	// retry's attach forked the stored checkpoint, so a denied attach never
	// claims a retry happened.
	pendingAction := claudeSubRetryNone
	var pendingDelay time.Duration
	for {
		attached, err := claudeSubAttachSession(ctx, pool, req, a, false, recoveries > 0)
		if err != nil {
			if recoveries > 0 && errors.Is(err, errClaudeSubRetryNeedsReseed) {
				return recoveryErr
			}
			return err
		}
		s := attached.session
		if pendingAction != claudeSubRetryNone {
			// The retry forked the stored checkpoint, so it is reported now: the
			// action in the call diagnostics, the log line, and the notice that
			// tells the caller a retry is running. An attach that refused fork-only
			// recovery returned above, so nothing here claims a retry it did not do.
			metrics := claudeSubMetricsFrom(ctx)
			metrics.noteRecovery(claudeSubActionLabel(pendingAction))
			claudeSubLogRecovery(recoveryErr, pendingAction, recoveries)
			if noticeErr := emit(claudeSubRecoveryNotice(recoveryErr, recoveries, pendingDelay)); noticeErr != nil {
				// The attempt never wrote to the CLI, so the forked process stays
				// reusable. The lease is released before the error ends the call: a
				// held lease would block every later call on this key.
				pool.release(s)
				return fmt.Errorf("emit claude_subscription recovery notice: %w", noticeErr)
			}
			pendingAction = claudeSubRetryNone
		}
		if !attached.reseed {
			claudeSubMetricsFrom(ctx).clearReseed()
		}
		// A pending replacement notice is reported before this attempt's first
		// chunk, so an attempt that produces nothing reports nothing. A reseed
		// attempt reports the flattened replay the same way.
		report := &claudeSubReplacementReport{pool: pool, key: key, emit: emit, flattened: attached.reseed}
		visible := false
		// cliRetried records that the CLI retried its own API request in this
		// attempt. Its retry budget is then spent, so a recovery would only repeat
		// the wait.
		cliRetried := false
		deliver := func(chunk ChatChunk) error {
			if claudeSubAPIRetryNotice(chunk) {
				// A retry notice carries no output: it neither closes the pre-output
				// recovery window nor lets the pending replacement warning stand in
				// for output this attempt never produced.
				cliRetried = true
				return emit(chunk)
			}
			if !warned {
				if err := report.before(); err != nil {
					return err
				}
			}
			if claudeSubVisibleChunk(chunk) {
				visible = true
			}
			return emit(chunk)
		}
		err = claudeSubDeliverNormal(ctx, s, req, attached, deliver)
		if report.emitted {
			warned = true
			noticeReported = noticeReported || report.replaced
		}
		if err == nil {
			// Clear the notice while this call still owns the lease: after the
			// release another call for the same key could abandon its own turn and
			// record a newer notice this call must not clear.
			report.confirm()
			if noticeReported {
				pool.clearReplacement(key)
			}
			pool.release(s)
			return nil
		}
		if errors.Is(err, errClaudeSubDecodeStream) {
			// The process may hold stale events from the failed turn, so it is
			// retired even after output and is never recovered inline. A teardown
			// that cannot confirm the exit records that the next attach must reseed.
			if !pool.retireConfirmed(key, s) {
				pool.noteReseedKey(key)
			}
			pool.release(s)
			return err
		}
		action := claudeSubRetryDecision(err, claudeSubRetryFacts{
			CallKind:   claudeSubCallKind(req),
			ContextErr: ctx.Err(),
			Visible:    visible,
			CLIRetried: cliRetried,
			Pending:    len(s.pending) > 0 || (s.host != nil && s.host.hasOpenCalls()),
			Restorable: a.restorable,
			Recoveries: recoveries,
		})
		if action == claudeSubRetryNone {
			pool.release(s)
			return err
		}
		// The failed process is replaced before any retry, so a retry never
		// reseeds: a resume must never append to the transcript file the failed
		// process may still hold open. A teardown that cannot confirm the exit
		// therefore ends the call with the original failure and keeps the reseed
		// marker, so the next user request reseeds instead of resuming a process
		// that may still be alive.
		if !pool.retireConfirmed(key, s) {
			pool.noteReseedKey(key)
			pool.release(s)
			return err
		}
		pool.release(s)
		recoveryErr = err
		recoveries++
		// Nothing is reported here: the attempt is only a retry once its attach
		// forked the stored checkpoint, which the next loop iteration decides.
		claudeSubMetricsFrom(ctx).resetDelivery()
		pendingAction = action
		pendingDelay = claudeSubRecoveryDelay(recoveries)
		if waitErr := claudeSubAwaitRecovery(ctx, pendingDelay); waitErr != nil {
			return waitErr
		}
	}
}

// claudeSubVisibleChunk reports whether chunk carries assistant output the caller
// sees. A usage chunk, a diagnostic, and the terminal chunk carry none, so only
// text and thinking close the pre-output recovery window. Thinking counts even
// though the caller's kept partial is text only: a retry must not lose it.
func claudeSubVisibleChunk(chunk ChatChunk) bool {
	if chunk.Done {
		return false
	}
	return chunk.Delta.Content != "" || chunk.Thinking != ""
}

// claudeSubLogRecovery records one recovery decision: the attempt, what the CLI
// reported, and what the call does next. It logs the safe error form, which names
// the failure without quoting CLI output.
func claudeSubLogRecovery(err error, action claudeSubRetryAction, attempt int) {
	subtype, terminalReason := "", ""
	if failure, ok := claudeSubFailedTurnResult(err); ok {
		subtype, terminalReason = failure.subtype, failure.terminalReason
	}
	slog.Warn("claude_subscription turn recovery",
		"attempt", attempt,
		"subtype", subtype,
		"terminal_reason", terminalReason,
		"action", action.String(),
		"error", claudeSubDiagnosticError(err))
}
