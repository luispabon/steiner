package provider

// Tests for draining an interrupted claude_subscription turn. A cancelled
// delivery interrupts the CLI, waits for its own aborted terminal result, and
// keeps the process for the next request when the ledger can accept the partial
// the agent kept. Every outcome the drain cannot prove falls back to the retire
// and replacement warning covered by claudesub_cancel_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// claudeSubDrainFirstUser is the user message the cancelled turn delivers.
const claudeSubDrainFirstUser = "write the report"

// claudeSubDrainResultEvent builds a scripted terminal result envelope.
func claudeSubDrainResultEvent(t *testing.T, subtype, terminalReason string, isError bool) claudeSubEvent {
	t.Helper()
	payload := map[string]any{
		"type":               "result",
		"subtype":            subtype,
		"is_error":           isError,
		"num_turns":          1,
		"session_id":         "sess-1",
		"permission_denials": []any{},
	}
	if terminalReason != "" {
		payload["terminal_reason"] = terminalReason
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode result event: %v", err)
	}
	return claudeSubEvent{Type: "result", Raw: raw}
}

// claudeSubDrainRequest is the request shape a sub-agent sends: its own
// transport session under the parent that spawned it.
func claudeSubDrainRequest(key, model string, msgs ...Message) ChatRequest {
	return ChatRequest{TransportSession: key, ParentTransportSession: "parent", Model: model, Messages: msgs}
}

// claudeSubCancelOnText runs one request and cancels its context on the first
// streamed text chunk, so the cancel lands mid-stream.
func claudeSubCancelOnText(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return claudeSubTurn(ctx, pool, req, func(chunk ChatChunk) error {
		if chunk.Delta.Content != "" {
			cancel()
		}
		return nil
	})
}

// claudeSubDrainFollowUp runs the request that continues a drained sub-agent
// turn with the real shape the agent keeps: the streamed partial plus its
// interruption note, then a new user message.
func claudeSubDrainFollowUp(t *testing.T, pool *ClaudeSubscriptionPool, key, model, partial string) claudeSubCollected {
	t.Helper()
	msgs := []Message{claudeSubUserText(claudeSubDrainFirstUser)}
	if partial != "" {
		msgs = append(msgs, claudeSubRecoveryPartial(partial))
	}
	msgs = append(msgs, claudeSubUserText("keep going"))
	return claudeSubRunCollected(t, pool, claudeSubDrainRequest(key, model, msgs...))
}

// claudeSubRunStreamCollected runs one request through the streaming wrapper,
// which attaches the provider diagnostics the reseed reason comes from, and
// records every chunk it emitted.
func claudeSubRunStreamCollected(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) claudeSubCollected {
	t.Helper()
	stream, err := claudeSubStream(context.Background(), pool, req, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got claudeSubCollected
	for chunk := range stream {
		got.chunks = append(got.chunks, chunk)
	}
	return got
}

// claudeSubWantDrainedReuse asserts the follow-up ran on the cancelled turn's
// own process with no replacement warning and its own answer.
func claudeSubWantDrainedReuse(t *testing.T, spawner *claudeSubTestSpawner, got claudeSubCollected) {
	t.Helper()
	if got.err != nil {
		t.Fatalf("follow-up error = %v", got.err)
	}
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("follow-up chunk error = %s", final.Error)
	}
	if final.Delta.Content != "Hello world" {
		t.Fatalf("follow-up text = %q, want its own %q with no leftover events", final.Delta.Content, "Hello world")
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the drained process reused)", n)
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a drained turn", warnings)
	}
}

// claudeSubWantOnlyFollowUpUserLine asserts the follow-up's user line carries
// the new message and never the text the cancelled turn streamed.
func claudeSubWantOnlyFollowUpUserLine(t *testing.T, conn *claudeSubFakeConn, streamed string) {
	t.Helper()
	lines := claudeSubUserLines(conn)
	if len(lines) != 2 {
		t.Fatalf("user lines = %q, want the cancelled turn plus one follow-up", lines)
	}
	last := lines[1]
	if !strings.Contains(last, "keep going") {
		t.Errorf("follow-up user line = %s, want the new message", last)
	}
	if streamed != "" && strings.Contains(last, streamed) {
		t.Errorf("follow-up user line = %s, want it to carry neither the streamed text nor the interruption note", last)
	}
	if strings.Contains(last, claudeSubRecoveryNoteText) {
		t.Errorf("follow-up user line = %s, want no interruption note", last)
	}
	if strings.Contains(last, "Conversation so far") {
		t.Errorf("follow-up user line = %s, want a delta, not a reseeded transcript", last)
	}
}

// TestClaudeSubDrainReusesProcessAfterTextStop proves a stop during text
// streaming keeps the process: the CLI's aborted result is consumed, the kept
// partial is recorded but never sent, and the follow-up gets its own answer.
func TestClaudeSubDrainReusesProcessAfterTextStop(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name   string
		reply  *claudeSubInterruptReply
		absent string
	}{
		{
			name:  "aborted streaming",
			reply: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
		},
		{
			name:  "aborted tools",
			reply: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_tool_stream.jsonl")},
		},
		{
			name: "abort subtype without a terminal reason",
			reply: &claudeSubInterruptReply{events: []claudeSubEvent{
				claudeSubDrainResultEvent(t, claudeSubDrainAbortedSubtype, "", true),
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &claudeSubScript{
				batches: [][]claudeSubEvent{
					claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
					claudeSubDecodeEvents(t, "turn_text.jsonl"),
				},
				interrupt: tt.reply,
			}
			pool, spawner := claudeSubPoolWithScripts(t, sc)
			if err := claudeSubCancelOnText(t, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))); err == nil {
				t.Fatal("cancelled turn returned no error")
			}
			conn := spawner.call(t, 0).Conn
			if conn.isClosed() {
				t.Fatal("the drained process was retired")
			}
			interrupts := 0
			for _, line := range conn.sentLines() {
				if strings.Contains(line, `"subtype":"interrupt"`) {
					interrupts++
				}
			}
			if interrupts != 1 {
				t.Errorf("interrupt requests = %d, want the drain's single one", interrupts)
			}

			got := claudeSubDrainFollowUp(t, pool, "child-1", model, "half a report")
			claudeSubWantDrainedReuse(t, spawner, got)
			claudeSubWantOnlyFollowUpUserLine(t, conn, "Hello world")
		})
	}
}

// claudeSubHostCallCount returns how many live MCP calls the session's host
// holds. The caller must not race a delivery; every use here runs after the turn
// returned.
func claudeSubHostCallCount(s *claudeSubSession) int {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	return len(s.host.calls)
}

// TestClaudeSubDrainReusesProcessAfterToolStreamStop proves a stop while only a
// tool_use block streamed keeps the process and retires the stream-time MCP
// registration the CLI never called.
func TestClaudeSubDrainReusesProcessAfterToolStreamStop(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:3],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_tool_stream.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	spec := claudeSubTestSpec()
	spec.Tools = tools
	seed, err := pool.acquire(context.Background(), "child-1", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)

	req := claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))
	req.Tools = tools
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, req); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	if conn.isClosed() {
		t.Fatal("the drained process was retired")
	}
	live := claudeSubLiveSession(pool, "child-1")
	if live == nil {
		t.Fatal("the drained process left the pool")
	}
	if len(live.pending) != 0 {
		t.Errorf("pending registrations = %d, want none after a drained stop", len(live.pending))
	}
	if n := claudeSubHostCallCount(live); n != 0 {
		t.Errorf("live MCP calls = %d, want none after a drained stop", n)
	}

	followUp := claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser), claudeSubUserText("keep going"))
	followUp.Tools = tools
	got := claudeSubRunCollected(t, pool, followUp)
	claudeSubWantDrainedReuse(t, spawner, got)
	interrupts := 0
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"subtype":"interrupt"`) {
			interrupts++
		}
	}
	if interrupts != 1 {
		t.Errorf("interrupt requests = %d, want the drain's single one and no stale-call interrupt", interrupts)
	}
	claudeSubWantOnlyFollowUpUserLine(t, conn, "Hello world")
}

// TestClaudeSubDrainReusesProcessAfterTextThenToolStreamStop proves text
// streamed before a tool_use block is accepted as the kept partial.
func TestClaudeSubDrainReusesProcessAfterTextThenToolStreamStop(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text_tool_partial.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	req := claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))
	req.Tools = tools
	if err := claudeSubCancelOnText(t, pool, req); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	conn := spawner.call(t, 0).Conn
	live := claudeSubLiveSession(pool, "child-1")
	if live == nil || len(live.pending) != 0 || claudeSubHostCallCount(live) != 0 {
		t.Fatalf("stream-time registrations survived the drain: pending %d host calls %d", len(live.pending), claudeSubHostCallCount(live))
	}

	followUp := claudeSubDrainRequest("child-1", model,
		claudeSubUserText(claudeSubDrainFirstUser),
		claudeSubRecoveryPartial("the report so far"),
		claudeSubUserText("keep going"))
	followUp.Tools = tools
	got := claudeSubRunCollected(t, pool, followUp)
	claudeSubWantDrainedReuse(t, spawner, got)
	claudeSubWantOnlyFollowUpUserLine(t, conn, "the report so far")
}

// TestClaudeSubDrainFallsBack proves every outcome the drain cannot prove
// retires the process on the next use and reports the existing replacement
// warning exactly once.
func TestClaudeSubDrainFallsBack(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name  string
		reply *claudeSubInterruptReply
	}{
		{
			name:  "success result",
			reply: &claudeSubInterruptReply{events: []claudeSubEvent{claudeSubDrainResultEvent(t, "success", "completed", false)}},
		},
		{
			name:  "error result with another terminal reason",
			reply: &claudeSubInterruptReply{events: []claudeSubEvent{claudeSubDrainResultEvent(t, claudeSubDrainAbortedSubtype, "max_turns", true)}},
		},
		{
			name:  "error result without the interruption subtype",
			reply: &claudeSubInterruptReply{events: []claudeSubEvent{claudeSubDrainResultEvent(t, "error_max_turns", "", true)}},
		},
		{
			name:  "error control response",
			reply: &claudeSubInterruptReply{err: "interrupt unavailable"},
		},
		{
			name:  "no result",
			reply: &claudeSubInterruptReply{},
		},
		{
			name:  "usage limit during the drain",
			reply: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "usage_limit.jsonl")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &claudeSubScript{
				batches: [][]claudeSubEvent{
					claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
					claudeSubDecodeEvents(t, "turn_text.jsonl"),
				},
				interrupt: tt.reply,
			}
			pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
			if err := claudeSubCancelOnText(t, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))); err == nil {
				t.Fatal("cancelled turn returned no error")
			}
			got := claudeSubDrainFollowUp(t, pool, "child-1", model, "half a report")
			if got.err != nil {
				t.Fatalf("follow-up error = %v", got.err)
			}
			if text := got.final(t).Delta.Content; text != "Hello world" {
				t.Fatalf("follow-up text = %q, want its own %q", text, "Hello world")
			}
			if n := spawner.callCount(); n != 2 {
				t.Fatalf("spawn count = %d, want 2 (the unproven process replaced)", n)
			}
			if !spawner.call(t, 0).Conn.isClosed() {
				t.Error("the unproven process was reused")
			}
			claudeSubWantOneWarning(t, got, claudeSubReplacedProcessWarning)
		})
	}
}

// TestClaudeSubDrainFallsBackWhenProcessExits proves a process that exits while
// the drain waits is replaced, not reused.
func TestClaudeSubDrainFallsBackWhenProcessExits(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		// Acknowledged, then nothing: the process exits before any result.
		interrupt: &claudeSubInterruptReply{},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), "child-1", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	// The process acknowledges the interrupt and then exits, so no result ever
	// reaches the drain.
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
		events := sc.respond(line)
		if strings.Contains(string(line), `"subtype":"interrupt"`) {
			conn.exitAfterNextSend()
		}
		return events
	}
	conn.mu.Unlock()
	claudeSubDrainTimeout = 10 * time.Second
	t.Cleanup(func() { claudeSubDrainTimeout = 50 * time.Millisecond })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- claudeSubTurn(ctx, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser)), func(chunk ChatChunk) error {
			if chunk.Delta.Content != "" {
				cancel()
			}
			return nil
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled turn returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn outlived the process exit")
	}

	got := claudeSubDrainFollowUp(t, pool, "child-1", model, "half a report")
	if got.err != nil {
		t.Fatalf("follow-up error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the exited process replaced)", n)
	}
	claudeSubWantOneWarning(t, got, claudeSubReplacedProcessWarning)
}

// TestClaudeSubDrainParentReseedsAfterStop pins the parent outcome: draining
// leaves the live record ahead of the stored checkpoint, so the next parent
// request still reseeds from the CLI session. No replacement warning is invented
// for the drained path.
func TestClaudeSubDrainParentReseedsAfterStop(t *testing.T) {
	model := claudeSubTestSpec().Model
	parent := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, parent, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	stored := claudeSubDecodeState(t, first.BackendState)
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("write the report")}
	storedState := claudeSubEncodeState(t, stored)
	conn := spawner.call(t, 0).Conn
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     storedState,
		Messages:         history,
	}, func(chunk ChatChunk) error {
		if chunk.Delta.Content != "" {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("cancelled parent turn returned no error")
	}
	if conn.isClosed() {
		t.Fatal("the parent process was retired at cancellation instead of drained")
	}

	next := append(append([]Message(nil), history...), claudeSubRecoveryPartial("half a report"), claudeSubUserText("again"))
	got := claudeSubRunStreamCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     storedState,
		Messages:         next,
	})
	if got.err != nil {
		t.Fatalf("next turn error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("next turn text = %q, want %q", text, "Hello world")
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (drained parent still reseeded)", n)
	}
	if !conn.isClosed() {
		t.Error("the drained parent process was reused past its stored checkpoint")
	}
	if reason := claudeSubRecoveryReseedReason(got.final(t)); reason != claudeSubReseedCheckpoint {
		t.Errorf("reseed reason = %q, want %q", reason, claudeSubReseedCheckpoint)
	}
	lines := claudeSubUserLines(spawner.call(t, 1).Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("replacement user lines = %q, want one rendered transcript", lines)
	}
	if !strings.Contains(lines[0], "half a report") || !strings.Contains(lines[0], claudeSubRecoveryNoteText) {
		t.Errorf("reseed transcript = %s, want the kept partial and its notice", lines[0])
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none: a drained turn discards no unproven process", warnings)
	}
}

// TestClaudeSubDrainStopsOnPoolClose proves the drain never holds pool shutdown:
// closing the pool revokes the lease, the drain stops, and the turn returns.
func TestClaudeSubDrainStopsOnPoolClose(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")[:8]},
		// The interrupt is never acknowledged, so only the closed session can end
		// the drain's wait.
		interrupt: &claudeSubInterruptReply{silent: true},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	seed, err := pool.acquire(context.Background(), "child-1", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	claudeSubDrainTimeout = 30 * time.Second
	t.Cleanup(func() { claudeSubDrainTimeout = 50 * time.Millisecond })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- claudeSubTurn(ctx, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser)), func(chunk ChatChunk) error {
			if chunk.Delta.Content != "" {
				cancel()
			}
			return nil
		})
	}()
	conn.waitSent(t, 3) // the drain's interrupt is in flight

	closed := make(chan error, 1)
	go func() { closed <- pool.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("pool close error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pool Close blocked behind the drain")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled turn returned no error after pool close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn outlived pool close")
	}
}

// TestClaudeSubSyncDrainedPartialAcceptance proves the drained ledger state
// accepts exactly one relaxed text-only assistant message.
func TestClaudeSubSyncDrainedPartialAcceptance(t *testing.T) {
	base := func(t *testing.T) *claudeSubSync {
		t.Helper()
		s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
		s.markDrained()
		return s
	}
	partial := func(fields Message) Message {
		fields.Role = MessageRoleAssistant
		if fields.Content == "" {
			fields.Content = "partial"
		}
		return fields
	}

	t.Run("turn-stamped text-only partial is recorded", func(t *testing.T) {
		s := base(t)
		delta, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), partial(Message{Content: "partial", Turn: 3})}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(delta.User) != 0 || len(delta.ToolResults) != 0 {
			t.Fatalf("delta = %+v, want nothing sent", delta)
		}
		s.adopt(delta)
		if len(s.entries) != 2 || s.entries[1].Role != MessageRoleAssistant {
			t.Fatalf("entries = %+v, want the recorded partial", s.entries)
		}
	})

	t.Run("empty suffix is accepted", func(t *testing.T) {
		s := base(t)
		delta, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncUserMsg("again")}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, []string{"again"}) {
			t.Fatalf("user = %q, want [again]", got)
		}
	})

	tests := []struct {
		name string
		msgs []Message
	}{
		{
			name: "second partial",
			msgs: []Message{claudeSubSyncUserMsg("hi"), partial(Message{Turn: 1}), partial(Message{Content: "more", Turn: 1})},
		},
		{
			name: "tool calls",
			msgs: []Message{claudeSubSyncUserMsg("hi"), partial(Message{ToolCalls: []ToolCall{{ID: "c1"}}, Turn: 1})},
		},
		{
			name: "images",
			msgs: []Message{claudeSubSyncUserMsg("hi"), partial(Message{Images: []ImageBlock{{MediaType: "image/png", Data: "AAAA"}}, Turn: 1})},
		},
		{
			name: "reasoning",
			msgs: []Message{claudeSubSyncUserMsg("hi"), partial(Message{ReasoningContent: "thinking", Turn: 1})},
		},
		{
			name: "no drained state",
			msgs: []Message{claudeSubSyncUserMsg("hi"), partial(Message{Turn: 1})},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
			if tc.name != "no drained state" {
				s.markDrained()
			}
			if _, err := s.plan(ChatRequest{Messages: tc.msgs}); !errors.Is(err, errClaudeSubHistoryChanged) {
				t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
			}
		})
	}
}

// TestClaudeSubSyncDrainedClearedByCommitSent proves the drained state lasts one
// delivery: the next committed turn drops it, and the recorded partial is never
// sent again.
func TestClaudeSubSyncDrainedClearedByCommitSent(t *testing.T) {
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
	s.markDrained()
	msgs := []Message{
		claudeSubSyncUserMsg("hi"),
		{Role: MessageRoleAssistant, Content: "partial", Turn: 1},
		claudeSubSyncUserMsg("again"),
	}
	delta, err := s.plan(ChatRequest{Messages: msgs})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !s.drained {
		t.Fatal("plan cleared the drained state before delivery")
	}
	claudeSubSyncCommit(s, delta)
	if s.drained {
		t.Error("commitSent kept the drained state")
	}
	repeat, err := s.plan(ChatRequest{Messages: append(msgs, claudeSubSyncUserMsg("more"))})
	if err != nil {
		t.Fatalf("repeat plan: %v", err)
	}
	if got := claudeSubSyncContents(repeat.User); !reflect.DeepEqual(got, []string{"more"}) {
		t.Errorf("repeat user = %q, want only the new message", got)
	}
}
