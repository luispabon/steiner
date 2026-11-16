package provider

import (
	"context"
	"errors"
)

// errClaudeSubEmptyDelivery reports a request that adds nothing to the CLI
// record it matched: with no user message and no tool result to write, a
// delivery would wait on an idle CLI for a turn that was never started.
var errClaudeSubEmptyDelivery = errors.New("claude_subscription request adds nothing to the conversation")

// claudeSubAttach is the per-call input to the attach state machine.
type claudeSubAttach struct {
	key         string
	spec        claudeSubStartSpec // spawn spec for a new process, without resume
	fingerprint string             // prompt and tools fingerprint of this request
	msgs        []Message          // non-system request messages
	stored      claudeSubBackendState
	hasState    bool
	// resume is the restore target for a new process; it is valid only when
	// restorable is true.
	resume     claudeSubResume
	restorable bool
	// partial is the index in msgs of the assistant partial the agent kept from a
	// stopped turn, or -1 when the request suffix carries none (see
	// claudeSubInterruptedTail).
	partial int
	// retired is set once a live process for this key was retired in this call.
	retired bool
	// stopped is set when the retired process was abandoned by a stopped turn,
	// which the recovery measurement reports as a stop recovery.
	stopped bool
}

// claudeSubAttachResult is how claudeSubAttachSession settled a request: the
// locked process to deliver on, whether the request must go to it as a rendered
// transcript, and the folded delivery a resume with a kept partial uses.
type claudeSubAttachResult struct {
	session *claudeSubSession
	reseed  bool
	// fold is set when a resume sends the stopped turn's partial as a note
	// instead of plan's plain delta. It already carries the record the delivery
	// leaves behind.
	fold *claudeSubDelta
}

// claudeSubAttachSession returns the process req runs on, locked (the caller
// releases it) and how req must be delivered.
//
//   - A process a cancelled delivery left unsafe is retired before anything is
//     planned on it, because the CLI may still produce that turn's events (see
//     claudeSubSession.abandon). Its replacement resumes the stored checkpoint
//     when req can restore it and that process is provably gone.
//   - A live process is reused only when it accepts req as an append (see
//     claudeSubLiveAccepts); the drained record of a proven stop is one such
//     shape. Otherwise it is retired: a request that can restore the stored
//     state resumes it, forking the session because a process for this key was
//     writing it, and any other mismatch reseeds.
//   - A new process resumes the stored state when that state is restorable for
//     req. Otherwise it starts fresh, and a request with history is reseeded.
//   - A process that exited is evicted, and its successor resumes the stored
//     state when that state is restorable (D3).
func claudeSubAttachSession(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, a claudeSubAttach, forceReseed bool) (claudeSubAttachResult, error) {
	reseedPending := pool.reseedPending(a.key)
	if reseedPending {
		// An earlier call could not prove its process gone. Retire whatever is
		// still pooled (usually nothing, because that call already evicted it),
		// then reseed on a new session instead of resuming the stored one.
		claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedUnconfirmed)
		forceReseed = true
		pool.retirePooled(a.key)
	}
	if evicted, confirmed := pool.evictDead(a.key); evicted {
		claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedExited)
		// The stored session resumes on a new process, which is safe only once
		// the dead one was provably taken down.
		if !confirmed {
			claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedUnconfirmed)
			forceReseed = true
		}
	}
	spawn := a.spec
	if a.restorable && !forceReseed {
		// Every resume forks: a fork never appends to the transcript file an old,
		// orphaned or just-retired process may still hold open.
		spawn.Resume = a.resume
	}
	s, err := pool.acquire(ctx, a.key, spawn)
	if err != nil {
		// The record stays: a failed spawn has not reseeded anything yet.
		return claudeSubAttachResult{}, err
	}
	if reseedPending {
		pool.clearReseedKey(a.key)
	}
	if s.unsafeForReuse() {
		// A cancelled delivery may still be producing events on this process, so
		// nothing is planned on it. Its stored checkpoint stays resumable, but only
		// when teardown confirms the process is gone: a deadline that expires first
		// reseeds with a new session instead.
		resume := a.restorable && !forceReseed
		confirmed := pool.retireConfirmed(a.key, s)
		pool.release(s)
		if !resume {
			return claudeSubAttachSession(ctx, pool, req, a, forceReseed)
		}
		if !confirmed {
			claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedUnconfirmed)
			pool.noteReseedKey(a.key)
			return claudeSubAttachSession(ctx, pool, req, a, true)
		}
		next := a
		next.retired = true
		next.stopped = true
		return claudeSubAttachSession(ctx, pool, req, next, forceReseed)
	}
	if s.primed {
		if err := claudeSubFinishPending(ctx, s, req); err != nil {
			if s.unsafeForReuse() {
				// A stale-pending interrupt that could not be drained leaves the CLI
				// possibly still producing that turn's events, so the process is
				// retired here and the request re-attaches on a fresh one. The retry
				// always reseeds: the stored checkpoint may end on an assistant
				// message whose tool calls the request abandoned, and a new user line
				// straight after a dangling tool_use is not a transcript steiner has
				// proven. The replacement notice reports it to the user.
				pool.retireAndRelease(a.key, s)
				return claudeSubAttachSession(ctx, pool, req, a, true)
			}
			pool.release(s)
			return claudeSubAttachResult{}, err
		}
		accepted, rejectReason, err := claudeSubLiveAccepts(ctx, s, req, a)
		if err != nil {
			pool.release(s)
			return claudeSubAttachResult{}, err
		}
		if accepted {
			claudeSubMetricsFrom(ctx).setProcess(claudeSubProcessReuse)
			return claudeSubAttachResult{session: s}, nil
		}
		owner := a.hasState && a.stored.TransportSession != a.key
		claudeSubMetricsFrom(ctx).noteReseed(rejectReason)
		if owner {
			// The stored state belongs to another transport session, so its restore
			// forks instead of reseeding. The fork never writes the live process's
			// file, but its teardown is still confirmed first: an unconfirmed
			// process records that the next attach must reseed.
			if !pool.retireConfirmed(a.key, s) {
				claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedUnconfirmed)
				pool.noteReseedKey(a.key)
			}
			pool.release(s)
			return claudeSubAttachSession(ctx, pool, req, a, false)
		}
		if a.restorable {
			// The request can restore the stored checkpoint, so the process is
			// retired and the request resumes at the stored uuid in a fork, which
			// hides the turn that stopped. The live process was discarded
			// unexpectedly, so the recovery reports the replacement.
			pool.noteReplacement(a.key)
			if !pool.retireConfirmed(a.key, s) {
				claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedUnconfirmed)
				pool.noteReseedKey(a.key)
				pool.release(s)
				return claudeSubAttachSession(ctx, pool, req, a, true)
			}
			pool.release(s)
			next := a
			next.retired = true
			return claudeSubAttachSession(ctx, pool, req, next, false)
		}
		pool.retireAndRelease(a.key, s)
		return claudeSubAttachSession(ctx, pool, req, a, true)
	}
	if s.resume != spawn.Resume || (s.sync.fingerprint != "" && s.sync.fingerprint != a.fingerprint) {
		// An unprimed process left by an earlier attempt was started with a
		// different resume target or prompt, so it cannot serve this request.
		// Persist is not compared: a key always maps to one process kind.
		claudeSubMetricsFrom(ctx).noteReseed(claudeSubReseedRetry)
		pool.retireAndRelease(a.key, s)
		return claudeSubAttachSession(ctx, pool, req, a, true)
	}
	s.sync.fingerprint = a.fingerprint
	if spawn.Resume.SessionID == "" {
		claudeSubMetricsFrom(ctx).setProcess(claudeSubProcessSpawn)
		return claudeSubAttachResult{session: s, reseed: claudeSubHasHistory(a.msgs)}, nil
	}
	entries, ok := claudeSubResumeEntries(a.msgs, a.stored)
	if !ok {
		pool.release(s)
		return claudeSubAttachResult{}, errors.New("claude_subscription resume record does not match the stored state")
	}
	// The resumed process holds the stored history; the request appends to it.
	s.sync.entries = entries
	s.sync.started = true
	s.sync.interruptible = -1
	s.primed = true
	metrics := claudeSubMetricsFrom(ctx)
	metrics.setProcess(claudeSubProcessResume)
	if a.retired || a.partial >= 0 {
		metrics.noteRecovery(claudeSubRecoveryResumeAfterMismatch)
	}
	if a.stopped || a.partial >= 0 {
		metrics.noteRecovery(claudeSubRecoveryResumeAfterStop)
	}
	result := claudeSubAttachResult{session: s}
	if a.partial >= 0 {
		tail := a.msgs[a.stored.SyncEntries:]
		keep := append(append([]claudeSubEntry(nil), entries...), claudeSubEntriesFromMessages(tail)...)
		delta := claudeSubResumeDelta(keep, tail, a.partial-a.stored.SyncEntries, claudeSubStoppedReplyNote(a.msgs[a.partial].IncompleteText))
		result.fold = &delta
	}
	return result, nil
}

// claudeSubLiveAccepts reports whether a live process can take req as an
// append, and the branch that refused it. When a state is given, the process
// record must equal its checkpoint, or be exactly the drained record of one
// stopped delivery ahead of it, and the state must be owned by this key. The
// request must also be a valid append to the record (D8, D22). A history the
// record cannot take is a reseed trigger, not an error, so it reports false with
// a nil error.
func claudeSubLiveAccepts(ctx context.Context, s *claudeSubSession, req ChatRequest, a claudeSubAttach) (bool, string, error) {
	drained := false
	if a.hasState {
		if a.stored.TransportSession != a.key {
			return false, claudeSubReseedOwnerFork, nil
		}
		live, ok, err := s.sync.checkpoint(a.key)
		if err != nil {
			return false, "", err
		}
		if !ok || !claudeSubStateMatches(live, a.stored) {
			// A stored checkpoint one stopped delivery behind is exactly the record
			// a proven drain leaves. plan below still enforces that delivery's
			// entries against the request and accepts one relaxed partial. A request
			// without a stored state (a sub-agent) never reaches this check: it has
			// no checkpoint to be behind, so its drained record is matched by plan
			// alone, which is the sub-agent reuse this provider already gave it.
			if !s.sync.drainedMatches(a.stored, a.key) {
				return false, claudeSubReseedCheckpoint, nil
			}
			drained = true
		}
	}
	delta, err := s.sync.plan(req)
	if err != nil {
		return false, claudeSubReseedPlan, nil
	}
	if len(delta.User) == 0 && len(delta.ToolResults) == 0 {
		// The request adds nothing to the record the process already holds, so the
		// delivery would write nothing and wait on an idle CLI for events that
		// never come. It is refused here instead.
		return false, "", errClaudeSubEmptyDelivery
	}
	if drained {
		claudeSubMetricsFrom(ctx).noteRecovery(claudeSubRecoveryDrainedReuse)
	}
	return true, "", nil
}

// claudeSubDeliverNormal plans the request against a locked parent or
// sub-agent process and delivers it. A reseeded process receives the rendered
// transcript instead of a delta, and a resume that restates a stopped turn
// sends the folded delta attach built.
func claudeSubDeliverNormal(ctx context.Context, s *claudeSubSession, req ChatRequest, attached claudeSubAttachResult, emit func(ChatChunk) error) error {
	if attached.reseed {
		delta := claudeSubReseedDelta(claudeSubMessages(req.Messages))
		return claudeSubReseedSendError(claudeSubDeliver(ctx, s, req, delta, emit))
	}
	if attached.fold != nil {
		return claudeSubDeliver(ctx, s, req, *attached.fold, emit)
	}
	delta, err := s.sync.plan(req)
	if err != nil {
		return err
	}
	return claudeSubDeliver(ctx, s, req, delta, emit)
}
