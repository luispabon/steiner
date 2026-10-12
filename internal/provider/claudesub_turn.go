package provider

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"
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

// claudeSubAdvisorTurn runs an advisor request on the advisor process of its
// parent. When the parent snapshot no longer matches what the advisor holds, the
// advisor process is retired and the snapshot is rendered again on a fresh one
// (D19).
func claudeSubAdvisorTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	s, err := pool.acquire(ctx, key, spec)
	if err != nil {
		return err
	}
	delta, err := claudeSubAdvisorPlan(ctx, s, req)
	if errors.Is(err, errClaudeSubHistoryChanged) {
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
	err = claudeSubDeliver(ctx, s, req, delta, emit)
	if errors.Is(err, errClaudeSubDecodeStream) {
		pool.retire(key, s)
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
}

// claudeSubNormalTurn runs a parent or sub-agent request. A process is reused
// only while its record matches the request (see claudeSubAttachSession). A
// resumed process that fails before producing any output is treated as dead:
// it is retired and the request is reseeded on a fresh process, once (D3).
func claudeSubNormalTurn(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, key string, spec claudeSubStartSpec, emit func(ChatChunk) error) error {
	fingerprint, err := claudeSubFingerprint(spec.SystemPrompt, spec.Tools)
	if err != nil {
		return err
	}
	a := claudeSubAttach{key: key, spec: spec, fingerprint: fingerprint, msgs: claudeSubMessages(req.Messages)}
	a.stored, a.hasState = claudeSubParseState(req.BackendState)
	a.resume, a.restorable, err = claudeSubRestoreFor(a.msgs, a.stored, a.hasState, key, fingerprint)
	if err != nil {
		return err
	}
	forceReseed := false
	for {
		s, reseed, err := claudeSubAttachSession(ctx, pool, req, a, forceReseed)
		if err != nil {
			return err
		}
		emitted := false
		err = claudeSubDeliverNormal(ctx, s, req, reseed, func(chunk ChatChunk) error {
			emitted = true
			return emit(chunk)
		})
		// A decode error means the process may hold stale events from the failed
		// turn, so it is retired even after output and is never retried inline.
		decodeFailure := errors.Is(err, errClaudeSubDecodeStream)
		retry := err != nil && !decodeFailure && !emitted && ctx.Err() == nil && s.resume.SessionID != ""
		if decodeFailure || retry {
			pool.retire(key, s)
		}
		pool.release(s)
		if !retry {
			return err
		}
		forceReseed = true
	}
}

// claudeSubSummarySuffix keys the ephemeral helper processes that answer a
// compaction summary. A helper never uses the parent's key, so the parent's
// process and its persisted state are never touched by it.
const claudeSubSummarySuffix = "|summary"

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
	if errors.Is(err, errClaudeSubSummaryNoPlace) {
		return claudeSubReseedSummary(ctx, pool, req, key, spec, emit)
	}
	return err
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

// claudeSubAttachSession returns the process req runs on, locked (the caller
// releases it), and whether req must be delivered as a reseed transcript.
//
//   - A live process is reused only when it accepts req as an append (see
//     claudeSubLiveAccepts). Otherwise it is retired: a changed owner resumes
//     with a fork when the stored state is restorable, and any other mismatch
//     reseeds.
//   - A new process resumes the stored state when that state is restorable for
//     req. Otherwise it starts fresh, and a request with history is reseeded.
//   - A process that exited is evicted and never resumed: its successor reseeds
//     (D3).
func claudeSubAttachSession(ctx context.Context, pool *ClaudeSubscriptionPool, req ChatRequest, a claudeSubAttach, forceReseed bool) (*claudeSubSession, bool, error) {
	if pool.evictDead(a.key) {
		forceReseed = true
	}
	spawn := a.spec
	if a.restorable && !forceReseed {
		spawn.Resume = a.resume
	}
	s, err := pool.acquire(ctx, a.key, spawn)
	if err != nil {
		return nil, false, err
	}
	if s.primed {
		if err := claudeSubFinishPending(ctx, s, req); err != nil {
			pool.release(s)
			return nil, false, err
		}
		accepted, err := claudeSubLiveAccepts(s, req, a)
		if err != nil {
			pool.release(s)
			return nil, false, err
		}
		if accepted {
			return s, false, nil
		}
		owner := a.hasState && a.stored.TransportSession != a.key
		pool.retireAndRelease(a.key, s)
		return claudeSubAttachSession(ctx, pool, req, a, !owner)
	}
	if s.resume != spawn.Resume || (s.sync.fingerprint != "" && s.sync.fingerprint != a.fingerprint) {
		// An unprimed process left by an earlier attempt was started with a
		// different resume target or prompt, so it cannot serve this request.
		// Persist is not compared: a key always maps to one process kind.
		pool.retireAndRelease(a.key, s)
		return claudeSubAttachSession(ctx, pool, req, a, true)
	}
	s.sync.fingerprint = a.fingerprint
	if spawn.Resume.SessionID == "" {
		return s, claudeSubHasHistory(a.msgs), nil
	}
	entries, ok := claudeSubResumeEntries(a.msgs, a.stored)
	if !ok {
		pool.release(s)
		return nil, false, errors.New("claude_subscription resume record does not match the stored state")
	}
	// The resumed process holds the stored history; the request appends to it.
	s.sync.entries = entries
	s.sync.started = true
	s.sync.interruptible = -1
	s.primed = true
	return s, false, nil
}

// claudeSubLiveAccepts reports whether a live process can take req as an
// append. When a state is given, the process record must equal its checkpoint
// and the state must be owned by this key. The request must also be a valid
// append to the record (D8, D22). A history the record cannot take is a reseed
// trigger, not an error, so it reports false with a nil error.
func claudeSubLiveAccepts(s *claudeSubSession, req ChatRequest, a claudeSubAttach) (bool, error) {
	if a.hasState {
		if a.stored.TransportSession != a.key {
			return false, nil
		}
		live, ok, err := s.sync.checkpoint(a.key)
		if err != nil {
			return false, err
		}
		if !ok || !claudeSubStateMatches(live, a.stored) {
			return false, nil
		}
	}
	if _, err := s.sync.plan(req); err != nil {
		return false, nil
	}
	return true, nil
}

// claudeSubDeliverNormal plans the request against a locked parent or
// sub-agent process and delivers it. A reseeded process receives the rendered
// transcript instead of a delta.
func claudeSubDeliverNormal(ctx context.Context, s *claudeSubSession, req ChatRequest, reseed bool, emit func(ChatChunk) error) error {
	var delta claudeSubDelta
	if reseed {
		delta = claudeSubReseedDelta(claudeSubMessages(req.Messages))
	} else {
		var err error
		if delta, err = s.sync.plan(req); err != nil {
			return err
		}
	}
	return claudeSubDeliver(ctx, s, req, delta, emit)
}

// claudeSubDeliver writes a planned delta to the claude CLI and streams the
// turn. Tool results are resolved and recorded as each is delivered, and the
// user line is recorded right after it is written, so a cancelled call never
// re-sends user content (D22).
func claudeSubDeliver(ctx context.Context, s *claudeSubSession, req ChatRequest, delta claudeSubDelta, emit func(ChatChunk) error) error {
	if len(delta.User) > 0 {
		if usage, usageErr := s.control.getUsage(ctx); usageErr != nil {
			return usageErr
		} else if err := claudeSubUsageGate(usage); err != nil {
			return err
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
		return err
	}
	return nil
}

func claudeSubFinishPending(ctx context.Context, s *claudeSubSession, req ChatRequest) error {
	if len(s.pending) == 0 {
		return nil
	}
	provided := make(map[string]struct{})
	for _, msg := range req.Messages {
		if msg.Role == MessageRoleTool {
			provided[msg.ToolCallID] = struct{}{}
		}
	}
	var stale []claudeSubPendingCall
	for _, pending := range s.pending {
		if _, ok := provided[pending.ID]; !ok {
			stale = append(stale, pending)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if _, err := s.control.interrupt(ctx); err != nil {
		return err
	}
	s.sync.markInterrupted()
	for _, pending := range stale {
		s.host.resolve(pending.Handle, claudeSubToolResult{Text: "tool call interrupted", IsError: true})
		claudeSubRemovePending(s, pending.ID)
	}
	return nil
}

func claudeSubPendingByID(s *claudeSubSession, id string) *claudeSubPendingCall {
	for i := range s.pending {
		if s.pending[i].ID == id {
			return &s.pending[i]
		}
	}
	return nil
}

func claudeSubRemovePending(s *claudeSubSession, id string) {
	for i := range s.pending {
		if s.pending[i].ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

func claudeSubToolResultFromMessage(msg Message) claudeSubToolResult {
	return claudeSubToolResult{Text: msg.Content, Images: msg.Images, IsError: false}
}

func claudeSubConsume(ctx context.Context, s *claudeSubSession, emit func(ChatChunk) error) error {
	decoder := newClaudeSubDecoder(claudeSubDecodeHooks{
		ToolName: func(name string) string {
			if s.host == nil {
				return name
			}
			return s.host.steinerToolName(name)
		},
		ObserveToolUse: func(id, _ string) {
			s.beginPendingCall(id)
		},
	})
	var ordinary *claudeSubDecoded
	// emitFinal sends a Done chunk. Only a parent call hands back backend state,
	// as of the messages committed so far; advisor and sub-agent calls return
	// none (D4).
	emitFinal := func(chunk ChatChunk) error {
		if s.parentCall {
			state, err := s.sync.backendState(s.key)
			if err != nil {
				return err
			}
			chunk.BackendState = state
		}
		return emit(chunk)
	}
	for {
		if err := s.overage(); err != nil {
			return err
		}
		ev, ok, interrupted := s.queue.popInterruptible(ctx, s.overageAbort)
		if interrupted {
			return s.overage()
		}
		if !ok {
			if err := s.overage(); err != nil {
				return err
			}
			if err := s.conn.Err(); err != nil {
				return err
			}
			return errors.New("claude CLI connection closed")
		}
		if err := s.overage(); err != nil {
			return err
		}
		if limit := claudeSubEventUsageLimit(ev); limit != nil {
			return limit
		}
		if ev.SessionID != "" {
			s.sync.cliSessionID = ev.SessionID
		}
		decoded, err := decoder.decode(ev)
		if err != nil {
			return err
		}
		for _, item := range decoded {
			switch item.Kind {
			case claudeSubDecodeText:
				if err := emit(ChatChunk{Delta: Message{Role: MessageRoleAssistant, Content: item.Text}}); err != nil {
					return err
				}
			case claudeSubDecodeThinking:
				if err := emit(ChatChunk{Thinking: item.Thinking}); err != nil {
					return err
				}
			case claudeSubDecodeUsage:
				if err := emit(ChatChunk{Usage: item.Usage}); err != nil {
					return err
				}
			case claudeSubDecodeToolUse:
				// The observation hook registers the opaque MCP handle before the
				// later assistant message exposes the complete call.
			case claudeSubDecodeMessage:
				if len(item.Message.ToolCalls) > 0 {
					s.sync.commitAssistant(*item.Message, item.UUID)
					if s.host != nil {
						s.host.settle()
					}
					if err := emitFinal(ChatChunk{Delta: *item.Message, ContentSnapshot: true, Done: true, FinishReason: item.FinishReason, Usage: item.Usage}); err != nil {
						return err
					}
					return nil
				}
				copy := item
				ordinary = &copy
			case claudeSubDecodeResult:
				if s.host != nil {
					// The turn has no tool calls left to wait for.
					s.host.settle()
				}
				if item.Result.IsError {
					return claudeSubTurnFailure(item.Result)
				}
				// The result's usage is cumulative over the whole CLI query and is not
				// context occupancy. The final chunk reports the terminal assistant
				// message's own usage, or none when that message has no usage.
				if ordinary != nil {
					s.sync.commitAssistant(*ordinary.Message, ordinary.UUID)
					if err := emitFinal(ChatChunk{Delta: *ordinary.Message, ContentSnapshot: true, Done: true, FinishReason: normalizeAnthropicFinishReason(item.Result.StopReason), Usage: ordinary.Usage}); err != nil {
						return err
					}
				} else if err := emitFinal(ChatChunk{Done: true, FinishReason: normalizeAnthropicFinishReason(item.Result.StopReason)}); err != nil {
					return err
				}
				return nil
			}
		}
	}
}

// claudeSubFailureTextMax bounds the result text quoted in a turn failure.
const claudeSubFailureTextMax = 300

// claudeSubTurnFailure reports a failed turn, quoting the result text when the
// CLI supplied one. Long text is cut at a rune boundary and marked with "...".
func claudeSubTurnFailure(r *claudeSubResult) error {
	text := r.Text
	if len(text) > claudeSubFailureTextMax {
		cut := claudeSubFailureTextMax
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "..."
	}
	if text == "" {
		return fmt.Errorf("claude_subscription turn failed: %s", r.Subtype)
	}
	return &claudeSubDetailError{
		detail: fmt.Errorf("claude_subscription turn failed: %s: %s", r.Subtype, text),
		safe:   fmt.Errorf("claude_subscription turn failed: %s", r.Subtype),
	}
}

// SupportsStatefulCompaction reports that compaction summaries follow the
// claude_subscription transition contract (see claudeSubSummaryTurn).
func (p *ClaudeSubscriptionProvider) SupportsStatefulCompaction() bool { return true }

var _ Provider = (*ClaudeSubscriptionProvider)(nil)
var _ StatefulTranscript = (*ClaudeSubscriptionProvider)(nil)
var _ StatefulCompactor = (*ClaudeSubscriptionProvider)(nil)
