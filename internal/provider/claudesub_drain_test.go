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

// claudeSubWantOneExactWarning asserts the turn emitted exactly one warning and
// that it is the expected text.
func claudeSubWantOneExactWarning(t *testing.T, got claudeSubCollected, wantText string) {
	t.Helper()
	warnings := got.warnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one", warnings)
	}
	if want := claudeSubWarningChunk(wantText).Diagnostic; warnings[0] != want {
		t.Errorf("warning = %q, want %q", warnings[0], want)
	}
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
// the new message and never the text the cancelled turn streamed or the content
// this delivery already sent through another channel.
func claudeSubWantOnlyFollowUpUserLine(t *testing.T, conn *claudeSubFakeConn, absent ...string) {
	t.Helper()
	lines := claudeSubUserLines(conn)
	if len(lines) != 2 {
		t.Fatalf("user lines = %q, want the cancelled turn plus one follow-up", lines)
	}
	last := lines[1]
	if !strings.Contains(last, "keep going") {
		t.Errorf("follow-up user line = %s, want the new message", last)
	}
	for _, text := range append(absent, claudeSubRecoveryNoteText) {
		if text == "" {
			continue
		}
		if strings.Contains(last, text) {
			t.Errorf("follow-up user line = %s, want it to carry %q again", last, text)
		}
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
		{
			// The CLI queued its own aborted result and a trailing status before
			// it acknowledged the interrupt. The fake keeps both ahead of the
			// control response in one channel, so they are already in the session
			// queue when the drain's first pop runs and the guard sees the status
			// behind the result without depending on router timing.
			name: "event queued after the aborted result",
			reply: &claudeSubInterruptReply{beforeAck: []claudeSubEvent{
				claudeSubDrainResultEvent(t, claudeSubDrainAbortedSubtype, "aborted_streaming", true),
				claudeSubDecodeEvents(t, "interrupt_window.jsonl")[1],
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
			// The unproven process is replaced and the recovery reseeds the whole
			// history, so the one warning covers both.
			claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
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
	drainTimeout := claudeSubDrainTimeout
	claudeSubDrainTimeout = 10 * time.Second
	t.Cleanup(func() { claudeSubDrainTimeout = drainTimeout })

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
	claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
}

// TestClaudeSubDrainParentReusesAfterStop proves a parent stop reuses the
// drained process: the stored checkpoint is exactly one stopped delivery behind
// the live record, so the request appends to the same pid with no reseed and no
// warning, and the Done checkpoint it returns round-trips through a fresh
// process.
func TestClaudeSubDrainParentReusesAfterStop(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name    string
		partial string
	}{
		{name: "kept partial", partial: "half a report"},
		{name: "no partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := &claudeSubScript{
				batches: [][]claudeSubEvent{
					claudeSubDecodeEvents(t, "turn_text.jsonl"),
					claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
					claudeSubDecodeEvents(t, "turn_text.jsonl"),
				},
				interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
			}
			pool, spawner := claudeSubPoolWithScripts(t, parent, claudeSubTextTurns(t, 1))
			first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
			storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
			history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("write the report")}
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

			next := append([]Message(nil), history...)
			if tt.partial != "" {
				next = append(next, claudeSubRecoveryPartial(tt.partial))
			}
			next = append(next, claudeSubUserText("again"))
			got := claudeSubRunStreamCollected(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     storedState,
				Messages:         next,
			})
			final := got.final(t)
			if final.Error != "" {
				t.Fatalf("next turn chunk error = %s", final.Error)
			}
			if text := final.Delta.Content; text != "Hello world" {
				t.Fatalf("next turn text = %q, want %q", text, "Hello world")
			}
			if n := spawner.callCount(); n != 1 {
				t.Fatalf("spawn count = %d, want 1 (the drained process reused)", n)
			}
			if conn.isClosed() {
				t.Error("the drained parent process was retired instead of reused")
			}
			if warnings := got.warnings(); len(warnings) != 0 {
				t.Errorf("warnings = %q, want none for a drained reuse", warnings)
			}
			if reason := claudeSubRecoveryReseedReason(final); reason != "" {
				t.Errorf("reseed reason = %q, want none", reason)
			}
			if recovery := claudeSubRecoveryLabel(final); recovery != claudeSubRecoveryDrainedReuse {
				t.Errorf("recovery = %q, want %q", recovery, claudeSubRecoveryDrainedReuse)
			}
			lines := claudeSubUserLines(conn)
			if len(lines) != 3 {
				t.Fatalf("user lines = %q, want the first turn, the stopped turn and this one", lines)
			}
			if !strings.Contains(lines[2], "again") {
				t.Errorf("next user line = %s, want the new message", lines[2])
			}
			if strings.Contains(lines[2], "Conversation so far") {
				t.Errorf("next user line = %s, want a delta, not a reseeded transcript", lines[2])
			}
			if tt.partial != "" && strings.Contains(lines[2], tt.partial) {
				t.Errorf("next user line = %s, want the kept partial never sent", lines[2])
			}

			// The Done checkpoint covers the recorded-not-sent partial, so a fresh
			// process resumes it instead of rejecting the request.
			historyAfter := append(append([]Message(nil), next...), final.Delta)
			restarted, restartedSpawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			restart := claudeSubRunStreamCollected(t, restarted, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     final.BackendState,
				Messages:         append(append([]Message(nil), historyAfter...), claudeSubUserText("keep going")),
			})
			if text := restart.final(t).Delta.Content; text != "Hello world" {
				t.Fatalf("restarted text = %q, want %q", text, "Hello world")
			}
			if n := restartedSpawner.callCount(); n != 1 {
				t.Fatalf("restart spawn count = %d, want 1", n)
			}
			args := restartedSpawner.call(t, 0).Args
			resume, _ := claudeSubArgValue(args, "--resume")
			at, _ := claudeSubArgValue(args, "--resume-session-at")
			state := claudeSubDecodeState(t, final.BackendState)
			if resume != state.CLISessionID || at != state.FinalUUID {
				t.Errorf("restart resume = %q at %q, want %q at %q", resume, at, state.CLISessionID, state.FinalUUID)
			}
			if !claudeSubHasArg(args, "--fork-session") {
				t.Errorf("restart args %q, want --fork-session for every resume", args)
			}
			restartLines := claudeSubUserLines(restartedSpawner.call(t, 0).Conn)
			if len(restartLines) != 1 || !strings.Contains(restartLines[0], "keep going") || strings.Contains(restartLines[0], "Conversation so far") {
				t.Errorf("restart user lines = %q, want the plain new message", restartLines)
			}
		})
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
	drainTimeout := claudeSubDrainTimeout
	claudeSubDrainTimeout = 30 * time.Second
	t.Cleanup(func() { claudeSubDrainTimeout = drainTimeout })

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

// claudeSubToolLoopSetup runs the first sub-agent turn, which streams one
// tool_use and ends, and returns the request history up to its tool result. more
// supplies the scripts of any later process.
func claudeSubToolLoopSetup(t *testing.T, sc *claudeSubScript, more ...*claudeSubScript) (*ClaudeSubscriptionPool, *claudeSubTestSpawner, string, string, []ToolSpec, []Message) {
	t.Helper()
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	pool, spawner := claudeSubPoolWithScripts(t, append([]*claudeSubScript{sc}, more...)...)
	req := claudeSubDrainRequest("child-1", model, claudeSubUserText("read notes"))
	req.Tools = tools
	done := claudeSubRunTurn(t, pool, req)
	if len(done.Delta.ToolCalls) != 1 {
		t.Fatalf("first turn tool calls = %+v, want one", done.Delta.ToolCalls)
	}
	result := Message{Role: MessageRoleTool, ToolCallID: done.Delta.ToolCalls[0].ID, Content: "notes body"}
	history := []Message{claudeSubUserText("read notes"), done.Delta, result}
	return pool, spawner, "child-1", model, tools, history
}

// TestClaudeSubDrainReusesProcessAfterToolLoopStop proves a stop while the model
// streams after a tool result keeps the process: the CLI continues the same turn
// after the result, so the record's trailing entry is that tool result.
func TestClaudeSubDrainReusesProcessAfterToolLoopStop(t *testing.T) {
	tests := []struct {
		name string
		// partial is the text the agent kept from the stopped stream; empty means
		// only a tool_use block streamed after the result.
		partial string
	}{
		{name: "kept partial", partial: "half a report"},
		{name: "no partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &claudeSubScript{
				batches: [][]claudeSubEvent{
					claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
					claudeSubDecodeEvents(t, "turn_text.jsonl"),
				},
				interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
			}
			pool, spawner, key, model, tools, history := claudeSubToolLoopSetup(t, sc)
			conn := spawner.call(t, 0).Conn

			// The tool result reaches the CLI through the MCP host, so the events it
			// streams next are queued directly, as the fake CLI cannot see the call.
			for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl")[:8] {
				conn.push(ev)
			}
			req := claudeSubDrainRequest(key, model, history...)
			req.Tools = tools
			if err := claudeSubCancelOnText(t, pool, req); err == nil {
				t.Fatal("cancelled tool-loop turn returned no error")
			}
			if conn.isClosed() {
				t.Fatal("the drained process was retired")
			}

			next := append([]Message(nil), history...)
			if tt.partial != "" {
				next = append(next, claudeSubRecoveryPartial(tt.partial))
			}
			next = append(next, claudeSubUserText("keep going"))
			followUp := claudeSubDrainRequest(key, model, next...)
			followUp.Tools = tools
			got := claudeSubRunCollected(t, pool, followUp)
			claudeSubWantDrainedReuse(t, spawner, got)
			claudeSubWantOnlyFollowUpUserLine(t, conn, "notes body", "Hello world", tt.partial)
		})
	}
}

// TestClaudeSubDrainFallsBackOnUnexpectedEntry proves the drain refuses a record
// that gained an entry the delivery did not write itself.
func TestClaudeSubDrainFallsBackOnUnexpectedEntry(t *testing.T) {
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner, key, model, tools, history := claudeSubToolLoopSetup(t, sc, claudeSubTextTurns(t, 1))
	conn := spawner.call(t, 0).Conn
	for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl")[:8] {
		conn.push(ev)
	}

	// The delivery records an assistant entry the drain cannot account for. It is
	// written from the emit callback, which runs on the delivery's own goroutine
	// while it holds the session lease, so the write cannot race.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := claudeSubDrainRequest(key, model, history...)
	req.Tools = tools
	err := claudeSubTurn(ctx, pool, req, func(chunk ChatChunk) error {
		if chunk.Delta.Content != "" {
			claudeSubLiveSession(pool, key).sync.commitAssistant(Message{Role: MessageRoleAssistant, Content: "recorded by someone else"}, "")
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("cancelled tool-loop turn returned no error")
	}

	next := append(append([]Message(nil), history...), claudeSubRecoveryPartial("half a report"), claudeSubUserText("keep going"))
	followUp := claudeSubDrainRequest(key, model, next...)
	followUp.Tools = tools
	got := claudeSubRunCollected(t, pool, followUp)
	if got.err != nil {
		t.Fatalf("follow-up error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the unproven process replaced)", n)
	}
	if !conn.isClosed() {
		t.Error("a record changed by another writer was reused")
	}
	claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
}

// TestClaudeSubSyncDrainedPartialAcceptance proves the drained ledger state
// accepts exactly one relaxed text-only assistant message.
func TestClaudeSubSyncDrainedPartialAcceptance(t *testing.T) {
	base := func(t *testing.T) *claudeSubSync {
		t.Helper()
		s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
		s.markDrained(0)
		return s
	}
	partial := func(fields Message) Message {
		fields.Role = MessageRoleAssistant
		if fields.Content == "" {
			fields.Content = "partial"
		}
		if fields.IncompleteText == "" {
			fields.IncompleteText = fields.Content
		}
		return fields
	}

	t.Run("turn-stamped text-only partial is recorded", func(t *testing.T) {
		s := base(t)
		delta, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), partial(Message{Content: "partial", Turn: 3}), claudeSubSyncUserMsg("again")}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, []string{"again"}) || len(delta.ToolResults) != 0 {
			t.Fatalf("delta = %+v, want only the new user message", delta)
		}
		s.adopt(delta)
		if len(s.entries) != 2 || s.entries[1].Role != MessageRoleAssistant {
			t.Fatalf("entries = %+v, want the recorded partial", s.entries)
		}
	})

	t.Run("partial without a following user is refused", func(t *testing.T) {
		s := base(t)
		if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), partial(Message{Content: "partial", Turn: 3})}}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("plan error = %v, want errClaudeSubHistoryChanged", err)
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

	t.Run("tool-result tail is accepted", func(t *testing.T) {
		s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
		s.commitAssistant(claudeSubSyncAssistantMsg("a1", "c1"), "")
		start := len(s.entries)
		s.commitToolResult(claudeSubSyncToolMsg("c1", "body"))
		s.markDrained(start)
		if !s.drained {
			t.Fatal("a delivery that only appended a tool result left the record undrained")
		}
		delta, err := s.plan(ChatRequest{Messages: []Message{
			claudeSubSyncUserMsg("hi"),
			claudeSubSyncAssistantMsg("a1", "c1"),
			claudeSubSyncToolMsg("c1", "body"),
			{Role: MessageRoleAssistant, Content: "partial", IncompleteText: "partial", Turn: 2},
			claudeSubSyncUserMsg("again"),
		}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, []string{"again"}) || len(delta.ToolResults) != 0 {
			t.Fatalf("delta = %+v, want only the new user message", delta)
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
		{
			name: "no incomplete marker",
			msgs: []Message{claudeSubSyncUserMsg("hi"), {Role: MessageRoleAssistant, Content: "partial", Turn: 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
			if tc.name != "no drained state" {
				s.markDrained(0)
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
	s.markDrained(0)
	msgs := []Message{
		claudeSubSyncUserMsg("hi"),
		{Role: MessageRoleAssistant, Content: "partial", IncompleteText: "partial", Turn: 1},
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

// claudeSubStaleToolCallSetup runs the sub-agent turn that streams a tool_use
// and returns the request history plus the Done chunk that carried the call.
func claudeSubStaleToolCallSetup(t *testing.T, sc *claudeSubScript, more ...*claudeSubScript) (*ClaudeSubscriptionPool, *claudeSubTestSpawner, *claudeSubFakeConn, string, string, []ToolSpec, []Message) {
	t.Helper()
	pool, spawner, key, model, tools, history := claudeSubToolLoopSetup(t, sc, more...)
	conn := spawner.call(t, 0).Conn
	return pool, spawner, conn, key, model, tools, history
}

// TestClaudeSubFinishPendingDrainsStaleToolCall proves a request that abandons a
// pending tool call reads the interrupted turn's own aborted result, so the turn
// it then delivers is its own and the process is reused.
func TestClaudeSubFinishPendingDrainsStaleToolCall(t *testing.T) {
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_tool_stream.jsonl")},
	}
	pool, spawner, conn, key, model, tools, history := claudeSubStaleToolCallSetup(t, sc)
	if conn.isClosed() {
		t.Fatal("the first turn's process was closed")
	}
	// The user gives up on the tool call instead of delivering a result.
	next := []Message{history[0], history[1], claudeSubUserText("never mind")}
	req := claudeSubDrainRequest(key, model, next...)
	req.Tools = tools
	got := claudeSubRunCollected(t, pool, req)
	if got.err != nil {
		t.Fatalf("follow-up error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("follow-up text = %q, want its own %q, not the interrupted turn's result", text, "Hello world")
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the drained process reused)", n)
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a drained stale-tool turn", warnings)
	}
	live := claudeSubLiveSession(pool, key)
	if live == nil {
		t.Fatal("the drained process left the pool")
	}
	if len(live.pending) != 0 {
		t.Errorf("pending registrations = %d, want none after the stale-call drain", len(live.pending))
	}
}

// TestClaudeSubFinishPendingDrainFailureRetires proves an unproven stale-call
// drain abandons the process, and the request re-attaches on a fresh one with the
// existing replacement warning.
func TestClaudeSubFinishPendingDrainFailureRetires(t *testing.T) {
	tests := []struct {
		name  string
		reply *claudeSubInterruptReply
	}{
		{name: "no result", reply: &claudeSubInterruptReply{}},
		{name: "success result", reply: &claudeSubInterruptReply{events: []claudeSubEvent{claudeSubDrainResultEvent(t, "success", "completed", false)}}},
		{name: "error control response", reply: &claudeSubInterruptReply{err: "interrupt unavailable"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &claudeSubScript{
				batches: [][]claudeSubEvent{
					claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
					claudeSubDecodeEvents(t, "turn_text.jsonl"),
				},
				interrupt: tt.reply,
			}
			pool, spawner, conn, key, model, tools, history := claudeSubStaleToolCallSetup(t, sc, claudeSubTextTurns(t, 1))
			next := []Message{history[0], history[1], claudeSubUserText("never mind")}
			req := claudeSubDrainRequest(key, model, next...)
			req.Tools = tools
			got := claudeSubRunCollected(t, pool, req)
			if got.err != nil {
				t.Fatalf("follow-up error = %v", got.err)
			}
			if text := got.final(t).Delta.Content; text != "Hello world" {
				t.Fatalf("follow-up text = %q, want its own %q", text, "Hello world")
			}
			if n := spawner.callCount(); n != 2 {
				t.Fatalf("spawn count = %d, want 2 (the unproven process replaced)", n)
			}
			if !conn.isClosed() {
				t.Error("the unproven process was reused")
			}
			claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
		})
	}
}

// TestClaudeSubFinishPendingDrainFailureOnExit proves a process that exits during
// the stale-call drain is replaced rather than reused.
func TestClaudeSubFinishPendingDrainFailureOnExit(t *testing.T) {
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
	}
	pool, spawner, conn, key, model, tools, history := claudeSubStaleToolCallSetup(t, sc, claudeSubTextTurns(t, 1))
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
		if strings.Contains(string(line), `"subtype":"interrupt"`) {
			conn.exitAfterNextSend()
		}
		return nil
	}
	conn.mu.Unlock()
	drainTimeout := claudeSubDrainTimeout
	claudeSubDrainTimeout = 10 * time.Second
	t.Cleanup(func() { claudeSubDrainTimeout = drainTimeout })

	next := []Message{history[0], history[1], claudeSubUserText("never mind")}
	req := claudeSubDrainRequest(key, model, next...)
	req.Tools = tools
	got := claudeSubRunCollected(t, pool, req)
	if got.err != nil {
		t.Fatalf("follow-up error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the exited process replaced)", n)
	}
	claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
}

// TestClaudeSubFinishPendingDrainThenCancelledDelivery proves the stale-call
// drain consumes the interrupted turn's own result, so a delivery cancelled right
// after it never accepts that result as its own and never leaves its own events
// for the next call.
func TestClaudeSubFinishPendingDrainThenCancelledDelivery(t *testing.T) {
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_tool_stream.jsonl")},
	}
	pool, spawner, conn, key, model, tools, history := claudeSubStaleToolCallSetup(t, sc)
	// The user abandons the tool call, so the stale-call drain runs first, and the
	// delivery that follows is cancelled.
	abandoned := append(append([]Message(nil), history[:2]...), claudeSubUserText("never mind"))
	req := claudeSubDrainRequest(key, model, abandoned...)
	req.Tools = tools
	if err := claudeSubCancelOnText(t, pool, req); err == nil {
		t.Fatal("cancelled delivery returned no error")
	}
	if conn.isClosed() {
		t.Fatal("the drained delivery retired the process")
	}

	// The next request must see only its own turn. A result left behind by the
	// stale-call interrupt, or events left by the cancelled delivery, would be
	// consumed here.
	next := append(append([]Message(nil), abandoned...), claudeSubUserText("keep going"))
	nextReq := claudeSubDrainRequest(key, model, next...)
	nextReq.Tools = tools
	got := claudeSubRunCollected(t, pool, nextReq)
	if got.err != nil {
		t.Fatalf("retried turn error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("retried text = %q, want its own %q", text, "Hello world")
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the process reused)", n)
	}
	interrupts := 0
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"subtype":"interrupt"`) {
			interrupts++
		}
	}
	if interrupts != 2 {
		t.Errorf("interrupt requests = %d, want the stale-call drain and the delivery drain: %q", interrupts, conn.sentLines())
	}
}

// TestClaudeSubDrainCancelsUnregisteredHostCalls proves a drained turn retires an
// MCP call the CLI opened for an id steiner never registered.
func TestClaudeSubDrainCancelsUnregisteredHostCalls(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	sc := &claudeSubScript{
		batches:   [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")[:8]},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	spec := claudeSubTestSpec()
	spec.Tools = tools
	seed, err := pool.acquire(context.Background(), "child-1", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(seed)
	// A tools/call opens the call before steiner registered the id, and the
	// cancel means no tool_use for it is ever decoded.
	if _, _, state := seed.host.openCall("toolu_queued"); state != claudeSubCallAcquired {
		t.Fatalf("openCall state = %v, want acquired", state)
	}
	req := claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))
	req.Tools = tools
	if err := claudeSubCancelOnText(t, pool, req); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	live := claudeSubLiveSession(pool, "child-1")
	if live == nil {
		t.Fatal("the drained process left the pool")
	}
	if n := claudeSubHostCallCount(live); n != 0 {
		t.Errorf("live MCP calls = %d, want the drained turn to retire the unregistered call", n)
	}
	if len(live.pending) != 0 {
		t.Errorf("pending registrations = %d, want none", len(live.pending))
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want 1 (the drained process reused)", n)
	}
}

// TestClaudeSubDrainReusesAdvisorProcess proves a drained advisor stop still
// appends only the new snapshot delta to the live process.
func TestClaudeSubDrainReusesAdvisorProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	advisor := func(msgs ...Message) ChatRequest {
		return ChatRequest{
			TransportSession:       "parent-advisor",
			ParentTransportSession: "parent",
			AdvisorCacheProfile:    true,
			Model:                  model,
			Messages:               msgs,
		}
	}
	first := []Message{claudeSubUserText("read notes"), {Role: MessageRoleAssistant, Content: "Hello world"}, claudeSubUserText("is it right?")}
	if err := claudeSubCancelOnText(t, pool, advisor(first...)); err == nil {
		t.Fatal("cancelled advisor turn returned no error")
	}
	conn := spawner.call(t, 0).Conn
	if conn.isClosed() {
		t.Fatal("the drained advisor process was retired")
	}

	second := append(append([]Message(nil), first...), claudeSubUserText("now check this"), claudeSubUserText("is it right again?"))
	got := claudeSubRunCollected(t, pool, advisor(second...))
	if got.err != nil {
		t.Fatalf("second advisor error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("second advisor text = %q, want its own %q", text, "Hello world")
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the drained advisor reused)", n)
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	lines := claudeSubUserLines(conn)
	if len(lines) != 2 {
		t.Fatalf("advisor user lines = %q, want the cancelled call plus the delta", lines)
	}
	if !strings.Contains(lines[1], "now check this") {
		t.Errorf("second advisor user line = %s, want the new snapshot part", lines[1])
	}
	if strings.Contains(lines[1], "read notes") {
		t.Errorf("second advisor user line = %s, want only the delta, not the whole snapshot", lines[1])
	}
}

// TestClaudeSubSummaryInPlaceCancelDrainsWithoutNotice proves a cancelled
// in-place summary now drains, records no replacement notice, and still retires
// the tainted parent.
func TestClaudeSubSummaryInPlaceCancelDrainsWithoutNotice(t *testing.T) {
	parent := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubTextTurnWithUUID(t, ""),
			claudeSubTextTurnWithUUID(t, ""),
			claudeSubPartialTextBatch(),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, parent)
	u1, a1, _, _, second := claudeSubTwoTurnParent(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{u1, a1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	}, func(chunk ChatChunk) error {
		if chunk.Delta.Content != "" {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("cancelled in-place summary returned no error")
	}
	if pool.replacementPending("parent") {
		t.Error("a drained in-place summary recorded a replacement notice")
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("a cancelled in-place summary left its tainted parent running")
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if live {
		t.Error("a cancelled in-place summary left its tainted parent pooled")
	}
}

// TestClaudeSubSyncDrainedRefusesSummary proves a drained record plans no
// compaction summary.
func TestClaudeSubSyncDrainedRefusesSummary(t *testing.T) {
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
	s.markDrained(0)
	if _, err := s.planSummary([]Message{claudeSubSyncUserMsg("hi"), claudeSubSyncUserMsg("summarize")}, 1); !errors.Is(err, errClaudeSubSummaryNoPlace) {
		t.Fatalf("planSummary error = %v, want errClaudeSubSummaryNoPlace", err)
	}
}

// TestClaudeSubSyncDrainedRejectsEditedUserLine proves the drained acceptance
// does not tolerate a rewrite of the user line the stopped turn delivered.
func TestClaudeSubSyncDrainedRejectsEditedUserLine(t *testing.T) {
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
	s.markDrained(0)
	msgs := []Message{claudeSubSyncUserMsg("changed"), {Role: MessageRoleAssistant, Content: "partial", IncompleteText: "partial", Turn: 1}}
	if _, err := s.plan(ChatRequest{Messages: msgs}); !errors.Is(err, errClaudeSubHistoryChanged) {
		t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
	}
}

// TestClaudeSubCancelWithQueuedResultKeepsProcess proves a cancel that lands
// after the turn's terminal result was already queued changes nothing: the call
// succeeds, sends no interrupt, and the process is reused.
func TestClaudeSubCancelWithQueuedResultKeepsProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2))
	seed, err := pool.acquire(context.Background(), "child-1", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	req := claudeSubDrainRequest("child-1", model, claudeSubUserText("read notes"))
	if err := claudeSubCancelTurnAfter(t, pool, conn, 2, true, req); err != nil {
		t.Fatalf("turn error = %v, want none for an already-queued terminal result", err)
	}
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"subtype":"interrupt"`) {
			t.Errorf("the completed turn interrupted its own process: %s", line)
		}
	}
	next := claudeSubDrainRequest("child-1", model,
		claudeSubUserText("read notes"),
		Message{Role: MessageRoleAssistant, Content: "Hello world"},
		claudeSubUserText("keep going"))
	got := claudeSubRunCollected(t, pool, next)
	claudeSubWantDrainedReuse(t, spawner, got)
}

// TestClaudeSubDrainAcceptsResultQueuedBeforeAck proves the drain accepts the
// aborted result even when the CLI queued it before its acknowledgement.
func TestClaudeSubDrainAcceptsResultQueuedBeforeAck(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{beforeAck: []claudeSubEvent{
			claudeSubDrainResultEvent(t, claudeSubDrainAbortedSubtype, "aborted_streaming", true),
		}},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	if err := claudeSubCancelOnText(t, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	got := claudeSubDrainFollowUp(t, pool, "child-1", model, "half a report")
	claudeSubWantDrainedReuse(t, spawner, got)
}

// TestClaudeSubDrainIgnoresWindowEnvelopes proves system status, a plain allowed
// rate limit and the CLI's interruption marker in the drain window leave the
// proof intact.
func TestClaudeSubDrainIgnoresWindowEnvelopes(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_window.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	if err := claudeSubCancelOnText(t, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	got := claudeSubDrainFollowUp(t, pool, "child-1", model, "half a report")
	claudeSubWantDrainedReuse(t, spawner, got)
}

// TestClaudeSubDrainFallsBackOnOverage proves paid extra usage inside the drain
// window abandons the process instead of proving it clean.
func TestClaudeSubDrainFallsBackOnOverage(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches:   [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")[:8]},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "rate_limit_overage.jsonl")},
	}
	pool, _ := claudeSubPoolWithScripts(t, sc)
	if err := claudeSubCancelOnText(t, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	if !pool.replacementPending("child-1") {
		t.Error("an overage inside the drain recorded no replacement notice")
	}
}

// TestClaudeSubDeliveryTail pins the record boundary check a drain relies on.
func TestClaudeSubDeliveryTail(t *testing.T) {
	record := []claudeSubEntry{
		{Role: MessageRoleUser},
		{Role: MessageRoleTool},
		{Role: MessageRoleAssistant},
	}
	tests := []struct {
		name    string
		entries []claudeSubEntry
		start   int
		want    bool
	}{
		{name: "user and tool tail", entries: record[:2], start: 0, want: true},
		{name: "tool-only tail", entries: record[:2], start: 1, want: true},
		{name: "assistant in the tail", entries: record, start: 1, want: false},
		{name: "assistant at the head of the tail", entries: record, start: 2, want: false},
		{name: "empty tail", entries: record, start: 3, want: true},
		{name: "start past the record", entries: record, start: 4, want: false},
		{name: "negative start", entries: record, start: -1, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubDeliveryTail(tc.entries, tc.start); got != tc.want {
				t.Fatalf("claudeSubDeliveryTail(%d entries, start %d) = %v, want %v", len(tc.entries), tc.start, got, tc.want)
			}
		})
	}
}

// TestClaudeSubFinishPendingDrainRetryDoesNotResume proves the retry after a
// failed stale-call drain reseeds even when a stored checkpoint is restorable:
// resuming would put a new user line right after an assistant message whose tool
// calls the request abandoned.
func TestClaudeSubFinishPendingDrainRetryDoesNotResume(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8]},
		// The stale-call interrupt is never proven.
		interrupt: &claudeSubInterruptReply{},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	conn := spawner.call(t, 0).Conn
	next := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("never mind")}
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		BackendState:     first.BackendState,
		Messages:         next,
	})
	if got.err != nil {
		t.Fatalf("follow-up error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("follow-up text = %q, want its own %q", text, "Hello world")
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the unproven process replaced)", n)
	}
	retry := spawner.call(t, 1)
	if resume, _ := claudeSubArgValue(retry.Args, "--resume"); resume != "" {
		t.Errorf("retry --resume = %q, want none: a dangling tool_use must not be resumed", resume)
	}
	lines := claudeSubUserLines(retry.Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("retry user lines = %q, want one flattened reseed", lines)
	}
	if !conn.isClosed() {
		t.Error("the unproven process was reused")
	}
	claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
}

// TestClaudeSubDrainFailsOnEventQueuedAfterResult proves an event already queued
// behind the aborted result fails the proof: the CLI sends nothing after its
// result, so anything there cannot be attributed to a later call. Both events
// are queued before the drain runs, so no router scheduling decides whether the
// event lands after the result.
func TestClaudeSubDrainFailsOnEventQueuedAfterResult(t *testing.T) {
	sc := &claudeSubScript{interrupt: &claudeSubInterruptReply{}}
	pool, spec := claudeSubScriptedPool(t, "child-1", sc)
	s, err := pool.acquire(context.Background(), "child-1", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	aborted := claudeSubDecodeEvents(t, "interrupt_text.jsonl")
	s.queue.push(aborted[len(aborted)-1])
	s.queue.push(claudeSubDecodeEvents(t, "interrupt_window.jsonl")[1])
	if err := claudeSubDrainToAbortedResult(context.Background(), s); !errors.Is(err, errClaudeSubDrainEventAfterResult) {
		t.Fatalf("drain error = %v, want %v", err, errClaudeSubDrainEventAfterResult)
	}
}
