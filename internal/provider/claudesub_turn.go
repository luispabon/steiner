package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func claudeSubSessionKey(req ChatRequest) string {
	key := req.TransportSession
	if key == "" {
		key = req.ParentTransportSession
	}
	if key == "" {
		key = "default"
	}
	if req.AdvisorCacheProfile {
		key = claudeSubAdvisorSessionKey(key)
	}
	return key
}

func claudeSubEffortFor(req ChatRequest) string {
	if req.Reasoning == nil {
		return ""
	}
	return req.Reasoning.Effort
}

// claudeSubPersists reports whether req is a parent normal call. Only parent
// calls persist their CLI session and return durable backend state (D4).
// Advisor and sub-agent processes stay ephemeral.
func claudeSubPersists(req ChatRequest) bool {
	return !req.AdvisorCacheProfile && !req.CompactionSummary && req.ParentTransportSession == ""
}

func claudeSubTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, emit func(ChatChunk) error) error {
	key := claudeSubSessionKey(req)
	spec := claudeSubStartSpec{Model: req.Model, Effort: claudeSubEffortFor(req), Persist: claudeSubPersists(req)}
	if req.AdvisorCacheProfile {
		return claudeSubAdvisorTurn(ctx, pool, req, key, spec, emit)
	}
	spec.SystemPrompt = claudeSubSystemPrompt(req.Messages)
	spec.Tools = req.Tools
	if req.CompactionSummary {
		return claudeSubSummaryTurn(ctx, pool, req, key, spec, emit)
	}
	return claudeSubNormalTurn(ctx, pool, req, key, spec, emit)
}

// claudeSubWarningChunk returns a provider diagnostic the user sees: a warning
// that does not end the turn and carries no assistant content.
func claudeSubWarningChunk(message string) ChatChunk {
	return ChatChunk{Diagnostic: message, Severity: "warning"}
}

// claudeSubReplacedProcessWarning is the warning a call reports when a cancelled
// or limited turn left a process steiner cannot reuse. It claims only what is
// certain: the discarded process served a turn that did not finish. Whether this
// request resumed a saved session, rebuilt the transcript, reused another
// process or started fresh is not reported, so a retried attempt or a reused
// process can never make the wording wrong.
const claudeSubReplacedProcessWarning = "claude_subscription discarded the CLI process behind an earlier turn that did not finish"

// claudeSubAdvisorReplacementWarning is the advisor form of the same warning.
const claudeSubAdvisorReplacementWarning = "claude_subscription discarded the advisor CLI process behind an earlier turn that did not finish"

// claudeSubFlattenedReseedWarning is the warning a call reports when a reseed
// replays the prior conversation to the CLI as one flattened user message, for
// any reseed branch. Summary requests and advisor snapshots render a transcript
// by design and report no warning.
const claudeSubFlattenedReseedWarning = "claude_subscription replayed the prior conversation as one flattened user message"

// claudeSubReplacedAndFlattenedWarning is the single warning a call reports when
// it discarded the process behind an earlier unfinished turn and also replayed
// the conversation flattened. It keeps the replacement wording intact so the
// two events stay identifiable in one message.
const claudeSubReplacedAndFlattenedWarning = claudeSubReplacedProcessWarning + " and replayed the prior conversation as one flattened user message"

// claudeSubReplacementWarning reports that a call discarded the process an
// earlier turn left behind. Advisor keys get the advisor wording.
func claudeSubReplacementWarning(key string) ChatChunk {
	if claudeSubSessionIsAdvisor(key) {
		return claudeSubWarningChunk(claudeSubAdvisorReplacementWarning)
	}
	return claudeSubWarningChunk(claudeSubReplacedProcessWarning)
}

// claudeSubReplacementReport reports a pending replacement notice for one
// delivery attempt. It emits the warning before that attempt's first streamed
// chunk, so an attempt that produces nothing reports nothing, and it does not
// clear the notice itself: the notice is cleared only by confirm after the call
// succeeded, because a warning buffered by a non-streaming fold is discarded
// when that call fails and must then stay pending for the next request.
type claudeSubReplacementReport struct {
	pool *ClaudeSubscriptionPool
	key  string
	emit func(ChatChunk) error
	// flattened marks an attempt that delivers the whole history as one rendered
	// reseed transcript. Unlike the replacement notice it is not stored on the
	// pool: it describes this request and nothing else, so the call that delivered
	// the flattened transcript reports it and no later call repeats it.
	flattened bool
	// checked is set once the notice was looked up for this attempt.
	checked bool
	// emitted is set once the warning reached the caller for this attempt.
	emitted bool
	// replaced is set when the emitted warning covered a pending replacement
	// notice, so only a notice this attempt reported can be cleared.
	replaced bool
}

// before emits the recovery warnings that apply to this attempt and returns its
// emit error. It is called before each chunk of the attempt and reports at most
// once. A pending replacement and a flattened reseed are reported as one
// combined warning, never as two.
func (r *claudeSubReplacementReport) before() error {
	if r.checked {
		return nil
	}
	r.checked = true
	replaced := r.pool.replacementPending(r.key)
	if !replaced && !r.flattened {
		return nil
	}
	if err := r.emit(r.warning(replaced)); err != nil {
		return fmt.Errorf("emit claude_subscription replacement warning: %w", err)
	}
	r.emitted = true
	r.replaced = replaced
	return nil
}

// warning returns the one warning an attempt reports for what happened to it.
func (r *claudeSubReplacementReport) warning(replaced bool) ChatChunk {
	switch {
	case replaced && r.flattened:
		return claudeSubWarningChunk(claudeSubReplacedAndFlattenedWarning)
	case r.flattened:
		return claudeSubWarningChunk(claudeSubFlattenedReseedWarning)
	default:
		return claudeSubReplacementWarning(r.key)
	}
}

// confirm clears the replacement notice this attempt reported, after the call
// succeeded. A failed call leaves the notice pending, so the replacement warning
// is either delivered on success or reported by the next request, never lost. An
// attempt that reported only the flattened replay clears nothing: that notice is
// not stored, and this call never owned a replacement notice to drop.
func (r *claudeSubReplacementReport) confirm() {
	if r == nil || !r.emitted || !r.replaced {
		return
	}
	r.pool.clearReplacement(r.key)
}

// claudeSubReportedEmit wraps emit so the attempt reports a pending replacement
// before its first chunk that carries output. An api_retry notice carries none, so
// it passes through without reporting the warning.
func claudeSubReportedEmit(report *claudeSubReplacementReport) func(ChatChunk) error {
	return func(chunk ChatChunk) error {
		if claudeSubAPIRetryNotice(chunk) {
			return report.emit(chunk)
		}
		if err := report.before(); err != nil {
			return err
		}
		return report.emit(chunk)
	}
}

// claudeSubAdvisorTurn runs an advisor request on the advisor process of its
// parent. When the parent snapshot no longer matches what the advisor holds, the
// advisor process is retired and the snapshot is rendered again on a fresh one
// (D19). A process a cancelled delivery left behind is replaced first, and the
// replacement delivery reports it.
func claudeSubAdvisorTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	s, err := pool.acquire(ctx, key, spec)
	if err != nil {
		return err
	}
	if s.retireIfUnsafe() {
		// The cancelled delivery may still be producing events on this process,
		// so it is replaced and the replacement renders the snapshot again.
		if s, err = pool.acquire(ctx, key, spec); err != nil {
			return err
		}
	}
	delta, err := claudeSubAdvisorPlan(ctx, s, req)
	if errors.Is(err, errClaudeSubHistoryChanged) {
		// The advisor's held snapshot no longer carries the request history, so
		// the replacement process reseeds from it.
		claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedPrefix)
		pool.retireAndRelease(key, s)
		if s, err = pool.acquire(ctx, key, spec); err != nil {
			return err
		}
		delta, err = claudeSubAdvisorPlan(ctx, s, req)
	}
	if err != nil {
		pool.release(s)
		return err
	}
	defer pool.release(s)
	report := &claudeSubReplacementReport{pool: pool, key: key, emit: emit}
	err = claudeSubDeliver(ctx, s, req, delta, claudeSubReportedEmit(report))
	if errors.Is(err, errClaudeSubDecodeStream) {
		pool.retire(key, s)
	}
	if err == nil {
		report.confirm()
	}
	return err
}

// claudeSubAdvisorPlan settles pending calls and plans the advisor delta.
func claudeSubAdvisorPlan(ctx context.Context, s *claudeSubSession, req ChatRequest) (claudeSubDelta, error) {
	if err := claudeSubFinishPending(ctx, s, req); err != nil {
		return claudeSubDelta{}, err
	}
	return s.sync.planAdvisor(req)
}

// claudeSubNormalTurn runs a parent or sub-agent request. A process is reused
// only while its record matches the request (see claudeSubAttachSession), and a
// process a cancelled delivery left behind is replaced before it can deliver
// anything. A resumed process that fails before producing any output is treated
// as dead: it is retired and the request is reseeded on a fresh process, once
// (D3).
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
	forceReseed := false
	var report *claudeSubReplacementReport
	for {
		attached, err := claudeSubAttachSession(ctx, pool, req, a, forceReseed)
		if err != nil {
			return err
		}
		s := attached.session
		if !attached.reseed {
			claudeSubMetricsFrom(ctx).clearReseed()
		}
		// A pending replacement notice is reported before this attempt's first
		// chunk, so an attempt that produces nothing reports nothing. A reseed
		// attempt reports the flattened replay the same way.
		report = &claudeSubReplacementReport{pool: pool, key: key, emit: emit, flattened: attached.reseed}
		emitted := false
		// cliRetried records that the CLI retried its own API request in this
		// attempt. Its retry budget is then spent, so replacing the process would
		// only repeat the wait.
		cliRetried := false
		err = claudeSubDeliverNormal(ctx, s, req, attached, func(chunk ChatChunk) error {
			if claudeSubAPIRetryNotice(chunk) {
				// A retry notice carries no output: it neither closes the pre-output
				// retry window nor lets the pending replacement warning stand in
				// for output this attempt never produced.
				cliRetried = true
				return emit(chunk)
			}
			if err := report.before(); err != nil {
				return err
			}
			emitted = true
			return emit(chunk)
		})
		// A decode error means the process may hold stale events from the failed
		// turn, so it is retired even after output and is never retried inline.
		decodeFailure := errors.Is(err, errClaudeSubDecodeStream)
		retry := !decodeFailure && !cliRetried && claudeSubShouldRetryTurn(err, emitted, ctx.Err(), s.resume.SessionID)
		if decodeFailure || retry {
			pool.retire(key, s)
		}
		if !retry {
			// Clear the notice while this call still owns the lease: after the
			// release another call for the same key could abandon its own turn and
			// record a newer notice this call must not clear.
			if err == nil {
				report.confirm()
			}
			pool.release(s)
			return err
		}
		pool.release(s)
		// The failed attempt left no usable record, so the retry reseeds. The
		// reset is defensive: the retry's attach sets the process again and the
		// noteReseed below overwrites the reason, while the gate's accumulated
		// time and polls stay.
		claudeSubMetricsFrom(ctx).resetDelivery()
		claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedRetry)
		forceReseed = true
	}
}

// claudeSubUsageGateError marks a usage-gate refusal produced after the poll
// budget, and possibly the fresh-process confirmation, was spent. The generic
// pre-output retry must not repeat that wait.
type claudeSubUsageGateError struct{ err error }

func (e *claudeSubUsageGateError) Error() string { return e.err.Error() }

func (e *claudeSubUsageGateError) Unwrap() error { return e.err }

// claudeSubShouldRetryTurn reports whether a failed delivery is retried once on
// a fresh process. Only a failure before any output, on a request with a resume
// target, and while the caller still waits qualifies. It excludes every refusal
// produced after the usage poll and the fresh confirmation, because retrying
// would repeat that wait. A usage limit is a refusal, so it is never retried on
// a fresh process. A live get_usage transport error stays retryable: it can mean
// the process is dead.
func claudeSubShouldRetryTurn(err error, emitted bool, ctxErr error, resumeSessionID string) bool {
	if err == nil || emitted || ctxErr != nil || resumeSessionID == "" {
		return false
	}
	var gateErr *claudeSubUsageGateError
	if errors.As(err, &gateErr) {
		return false
	}
	_, limited := AsUsageLimit(err)
	return !limited
}

// claudeSubSummarySuffix keys the ephemeral helper processes that answer a
// compaction summary. A helper never uses the parent's key, so the parent's
// process and its persisted state are never touched by it.
const claudeSubSummarySuffix = "|summary"

// claudeSubInPlaceSummary sends a summary request on the locked live process s
// as one marked instruction appended to its record.
func claudeSubInPlaceSummary(ctx context.Context, s *claudeSubSession, req ChatRequest, emit func(ChatChunk) error) error {
	if err := claudeSubFinishPending(ctx, s, req); err != nil {
		return err
	}
	delta, err := s.sync.planSummary(claudeSubMessages(req.Messages), req.CompactionSourceMessages)
	if err != nil {
		return err
	}
	return claudeSubDeliverTainted(ctx, s, req, delta, emit)
}

// claudeSubDeliverTainted taints s before delivering delta. The taint lands
// before the first write, so a failure at any later point leaves the record
// unusable for reuse.
func claudeSubDeliverTainted(ctx context.Context, s *claudeSubSession, req ChatRequest, delta claudeSubDelta, emit func(ChatChunk) error) error {
	s.sync.taint()
	return claudeSubDeliver(ctx, s, req, delta, emit)
}

// claudeSubClearOnDone wraps emit so the final chunk tells the issuing
// generation to drop its backend state.
func claudeSubClearOnDone(emit func(ChatChunk) error) func(ChatChunk) error {
	return func(chunk ChatChunk) error {
		if chunk.Done {
			chunk.ClearBackendState = true
		}
		return emit(chunk)
	}
}

// claudeSubReseedSendError names the payload an oversized stdin line belonged
// to. Only the CLI's limit refusal, recognised by type, gains the context, so
// every other error from the delivery passes through unchanged and errors.As
// still reaches the limit error.
func claudeSubReseedSendError(err error) error {
	var tooLarge *claudeSubLineTooLargeError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("send flattened reseed transcript: %w", err)
	}
	return err
}

// claudeSubDeliver writes a planned delta to the claude CLI and streams the
// turn. Tool results are resolved and recorded as each is delivered, and the
// user line is recorded right after it is written, so a cancelled call never
// re-sends user content (D22). A process a cancelled delivery left unsafe is
// retired here instead, and the call fails: it can never prove which call the
// events it may still produce belong to.
func claudeSubDeliver(ctx context.Context, s *claudeSubSession, req ChatRequest, delta claudeSubDelta, emit func(ChatChunk) error) error {
	if s.unsafeForReuse() {
		// A cancelled turn may still be producing events here, so nothing may be
		// written to this process. It is retired rather than reused; the caller's
		// release still follows.
		s.pool.retire(s.key, s)
		return errClaudeSubSessionUnsafe
	}
	metrics := claudeSubMetricsFrom(ctx)
	metrics.noteProcess(s)
	if len(delta.User) > 0 {
		getUsage := claudeSubQuotaObservedGetUsage(s.pool, s.control.getUsage)
		polls := 0
		countedGetUsage := func(ctx context.Context) (json.RawMessage, error) {
			polls++
			return getUsage(ctx)
		}
		gateStart := time.Now()
		liveErr := claudeSubAwaitUsageGate(ctx, countedGetUsage)
		metrics.noteUsageGate(time.Since(gateStart), polls)
		if errors.Is(liveErr, errClaudeSubUsageNotReady) {
			// The live process may be answering from the CLI's in-process usage
			// cache, so one fresh process confirms the state before refusing.
			probeErr := s.pool.confirmUsageFresh(ctx)
			liveErr = claudeSubUsageProbeOutcome(liveErr, probeErr)
			if liveErr != nil {
				// An advisor can otherwise hide a signed-out account behind the
				// usage refusal. Only a fresh, definite not-ready result may trigger
				// the independent auth check; cancellation, shutdown, and credits
				// enabled retain precedence.
				if req.AdvisorCacheProfile && errors.Is(probeErr, errClaudeSubUsageNotReady) && errors.Is(liveErr, errClaudeSubUsageNotReady) &&
					!errors.Is(liveErr, context.Canceled) && !errors.Is(liveErr, context.DeadlineExceeded) &&
					!errors.Is(liveErr, errClaudeSubPoolClosed) && !errors.Is(liveErr, errClaudeSubUsageCreditsEnabled) {
					if authErr := s.pool.confirmAuthFresh(ctx); errors.Is(authErr, errClaudeSubSignedOut) {
						// The current process was authenticated before the external
						// logout. Retire it under the active lease; the caller must issue
						// an explicit retry after logging in, never an automatic replay.
						s.pool.retryCLILookup()
						s.pool.retire(s.key, s)
						return &claudeSubUsageGateError{err: authErr}
					} else if errors.Is(authErr, context.Canceled) || errors.Is(authErr, context.DeadlineExceeded) || errors.Is(authErr, errClaudeSubPoolClosed) {
						return authErr
					}
				}
				// The poll budget, and possibly the fresh confirmation, is already
				// spent, so the generic pre-output retry must not repeat it.
				return &claudeSubUsageGateError{err: liveErr}
			}
		}
		if liveErr != nil {
			return liveErr
		}
	}
	if s.model != req.Model {
		if _, err := s.control.setModel(ctx, req.Model); err != nil {
			return err
		}
		s.model = req.Model
	}
	if effort := claudeSubEffortFor(req); effort != "" && effort != s.effort {
		if _, err := s.control.setEffort(ctx, effort); err != nil {
			return err
		}
		s.effort = effort
	}
	if s.host != nil {
		if err := s.host.setTools(req.Tools); err != nil {
			return err
		}
	}
	// Check every tool result before resolving any, so an unknown result
	// changes nothing.
	for _, result := range delta.ToolResults {
		if claudeSubPendingByID(s, result.ToolCallID) == nil {
			return fmt.Errorf("claude_subscription tool result %q has no pending call", result.ToolCallID)
		}
	}
	if !req.AdvisorCacheProfile {
		s.sync.adopt(delta)
	}
	// Everything this delivery appends from here on is its own user or tool
	// entry; a drain needs that boundary to prove nothing else touched the
	// record while the turn streamed.
	s.delivery = claudeSubDelivery{entries: len(s.sync.entries)}
	for _, result := range delta.ToolResults {
		call := claudeSubPendingByID(s, result.ToolCallID)
		if call == nil {
			return fmt.Errorf("claude_subscription tool result %q has no pending call", result.ToolCallID)
		}
		s.host.resolve(call.Handle, claudeSubToolResultFromMessage(result))
		claudeSubRemovePending(s, result.ToolCallID)
		s.sync.commitToolResult(result)
	}
	if len(delta.User) > 0 {
		if err := writeClaudeSubUser(s.conn, claudeSubUserBlocks(delta.User)); err != nil {
			return err
		}
	}
	if len(delta.ToolResults) > 0 || len(delta.User) > 0 {
		s.sync.commitSent(delta)
	}
	if req.AdvisorCacheProfile {
		messages := claudeSubMessages(req.Messages)
		s.sync.commitAdvisor(messages[:len(messages)-1])
	} else {
		s.primed = true
	}
	s.parentCall = claudeSubPersists(req)
	if err := claudeSubConsume(ctx, s, emit); err != nil {
		if ctx.Err() != nil {
			// The caller stopped reading a turn the CLI keeps producing. When the
			// CLI can be interrupted and driven to its own aborted result the
			// process stays reusable, which keeps its prompt cache warm; every
			// other outcome abandons it, so the next request for this key replaces
			// it and reports the replacement.
			claudeSubRecoverInterrupted(ctx, s)
		}
		return err
	}
	return nil
}

var _ Provider = (*ClaudeSubscriptionProvider)(nil)
var _ StatefulTranscript = (*ClaudeSubscriptionProvider)(nil)
var _ StatefulCompactor = (*ClaudeSubscriptionProvider)(nil)
