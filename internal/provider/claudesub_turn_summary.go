package provider

import (
	"context"
	"errors"
)

// claudeSubSummaryTurn answers a compaction summary request. A sub-agent
// summarizes in place on its own process. A parent forks its live process when
// the stored state and a source-tail uuid allow it. Otherwise it summarizes in
// place on the live process and retires that process. Only a parent with no
// live process cold-reseeds an ephemeral helper.
func claudeSubSummaryTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	if req.ParentTransportSession != "" {
		return claudeSubSubAgentSummary(ctx, pool, req, key, spec, emit)
	}
	emitted := false
	track := func(chunk ChatChunk) error {
		emitted = true
		return emit(chunk)
	}
	attempted, err := claudeSubTryParentFork(ctx, pool, req, key, spec, track)
	switch {
	case attempted && err == nil:
		return nil
	case err != nil && (!attempted || emitted || ctx.Err() != nil):
		return err
	}
	// No fork, or a fork that failed before any output. The live parent is
	// untouched in both cases, so it answers in place or the request reseeds.
	// Either way the parent's generation loses its backend state, so the final
	// chunk clears it.
	return claudeSubParentInPlace(ctx, pool, req, key, spec, claudeSubClearOnDone(emit))
}

// claudeSubTryParentFork answers a parent summary by forking the live process,
// when the stored state and a source-tail uuid allow it. It reports whether a
// fork was attempted. The live process is released before the helper runs,
// because the fork copied the record it needs.
func claudeSubTryParentFork(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) (bool, error) {
	live, err := pool.acquireLive(ctx, key)
	if err != nil || live == nil {
		return false, err
	}
	fingerprint, err := claudeSubFingerprint(spec.SystemPrompt, spec.Tools)
	if err != nil {
		pool.release(live)
		return false, err
	}
	src, forkable, err := claudeSubForkSourceOf(live, req, key, fingerprint)
	pool.release(live)
	if err != nil || !forkable {
		return false, err
	}
	return true, claudeSubForkSummary(ctx, pool, req, key, src, spec, emit)
}

// claudeSubParentInPlace answers a parent summary on the live process, which is
// retired once it has taken the summary. Without a live process, or when the
// record cannot take the request, the request is cold-reseeded on a helper.
func claudeSubParentInPlace(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	live, err := pool.acquireLive(ctx, key)
	if err != nil {
		return err
	}
	if live == nil {
		return claudeSubReseedSummary(ctx, pool, req, key, spec, emit)
	}
	err = claudeSubInPlaceSummary(ctx, live, req, emit)
	// The in-place summary is part of the live transcript now, so the process is
	// retired. A no-place result retires it too, before the reseed.
	pool.retireAndRelease(key, live)
	// Every failure from here on leaves the generation without backend state,
	// so the caller is told to drop it rather than reuse it.
	if errors.Is(err, errClaudeSubSummaryNoPlace) {
		return markBackendStateRetired(claudeSubReseedSummary(ctx, pool, req, key, spec, emit))
	}
	return markBackendStateRetired(err)
}

// claudeSubForkSource is what a summary fork starts from: the stored CLI
// session, the uuid that ends the summarized source, and a copy of the record up
// to that uuid.
type claudeSubForkSource struct {
	sessionID   string
	atUUID      string
	entries     []claudeSubEntry
	fingerprint string
}

// claudeSubForkSourceOf reports whether the live process s can be forked for a
// summary. The stored state must be the live checkpoint, owned by key and built
// with the same prompt and tools, and the source must end at an assistant uuid.
// The caller holds s's lock.
func claudeSubForkSourceOf(s *claudeSubSession, req ChatRequest, key, fingerprint string) (claudeSubForkSource, bool, error) {
	stored, ok := claudeSubParseState(req.BackendState)
	if !ok || stored.TransportSession != key || stored.PromptToolsFingerprint != fingerprint {
		return claudeSubForkSource{}, false, nil
	}
	live, ok, err := s.sync.checkpoint(key)
	if err != nil {
		return claudeSubForkSource{}, false, err
	}
	if !ok || !claudeSubStateMatches(live, stored) {
		return claudeSubForkSource{}, false, nil
	}
	uuid, ok := s.sync.sourceTailUUID(claudeSubMessages(req.Messages), req.CompactionSourceMessages)
	if !ok {
		return claudeSubForkSource{}, false, nil
	}
	return claudeSubForkSource{
		sessionID:   stored.CLISessionID,
		atUUID:      uuid,
		entries:     append([]claudeSubEntry(nil), s.sync.entries[:req.CompactionSourceMessages]...),
		fingerprint: fingerprint,
	}, true, nil
}

// claudeSubAcquireFreshHelper acquires the summary helper under helperKey as a
// process with no record. A helper that is already primed or started is retired
// and acquired again, so the record the caller writes describes a fresh process.
func claudeSubAcquireFreshHelper(ctx context.Context, pool *ClaudeSubscriptionPool, helperKey string, spec claudeSubStartSpec) (*claudeSubSession, error) {
	h, err := pool.acquire(ctx, helperKey, spec)
	if err != nil {
		return nil, err
	}
	if !h.primed && !h.sync.started {
		return h, nil
	}
	pool.retireAndRelease(helperKey, h)
	return pool.acquire(ctx, helperKey, spec)
}

// claudeSubAcquireSummaryHelper acquires the ephemeral summary helper for key as
// a fresh process started with resume. It returns the helper and its key. The
// caller retires and releases the helper with pool.retireAndRelease.
func claudeSubAcquireSummaryHelper(ctx context.Context, pool *ClaudeSubscriptionPool, key string, spec claudeSubStartSpec, resume claudeSubResume) (*claudeSubSession, string, error) {
	helperKey := key + claudeSubSummarySuffix
	spec.Persist = false
	spec.MaxTurns = 1
	spec.Resume = resume
	h, err := claudeSubAcquireFreshHelper(ctx, pool, helperKey, spec)
	return h, helperKey, err
}

// claudeSubForkSummary answers a summary request from an ephemeral helper that
// forks the stored CLI session at the source tail. The helper holds only the
// source, runs one turn, and is retired. It returns no backend state.
func claudeSubForkSummary(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, src claudeSubForkSource, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	h, helperKey, err := claudeSubAcquireSummaryHelper(ctx, pool, key, spec, claudeSubResume{SessionID: src.sessionID, AtUUID: src.atUUID, Fork: true})
	if err != nil {
		return err
	}
	defer pool.retireAndRelease(helperKey, h)
	h.sync.entries = src.entries
	h.sync.started = true
	h.sync.interruptible = -1
	h.sync.fingerprint = src.fingerprint
	h.primed = true
	delta, err := h.sync.plan(req)
	if err != nil {
		return err
	}
	return claudeSubDeliver(ctx, h, req, delta, emit)
}

// claudeSubReseedSummary answers a summary request from an ephemeral helper that
// is cold-reseeded with the whole request. It runs only when no live parent
// process exists. The helper is retired afterwards and returns no backend state.
func claudeSubReseedSummary(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	h, helperKey, err := claudeSubAcquireSummaryHelper(ctx, pool, key, spec, claudeSubResume{})
	if err != nil {
		return err
	}
	defer pool.retireAndRelease(helperKey, h)
	return claudeSubDeliverNormal(ctx, h, req, true, emit)
}

// claudeSubSubAgentSummary answers a sub-agent summary request in place on the
// sub-agent's own process, which is then tainted. A process that cannot take the
// request is replaced, and the marked request is reseeded on the new process.
func claudeSubSubAgentSummary(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	s, err := pool.acquire(ctx, key, spec)
	if err != nil {
		return err
	}
	err = claudeSubInPlaceSummary(ctx, s, req, emit)
	// The summary tainted this process, so it is retired at once rather than
	// left pooled for lazy retirement.
	pool.retireAndRelease(key, s)
	if !errors.Is(err, errClaudeSubSummaryNoPlace) {
		return err
	}
	s, err = pool.acquire(ctx, key, spec)
	if err != nil {
		return err
	}
	defer pool.retireAndRelease(key, s)
	return claudeSubDeliverTainted(ctx, s, req, claudeSubMarkedReseedDelta(req), emit)
}

// claudeSubMarkedReseedDelta renders a whole summary request as a reseeded
// transcript. Its instruction is marked as an in-place summary with no retained
// tail.
func claudeSubMarkedReseedDelta(req ChatRequest) claudeSubDelta {
	msgs := claudeSubMessages(req.Messages)
	if n := req.CompactionSourceMessages; n == len(msgs)-1 {
		msgs[n] = claudeSubMarkSummary(msgs[n], n, 0)
	}
	return claudeSubReseedDelta(msgs)
}
