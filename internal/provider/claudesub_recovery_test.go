package provider

// Characterization tests for claude_subscription turn recovery and
// cancellation. Every assertion in this file describes the provider's CURRENT
// behaviour, verified against the code as it stands. Several of them record
// defects: a live failure after delivery forces a full transcript reseed, a
// cancellation leaves the aborted turn's late events unfenced, and a restart
// after an interrupted turn cannot resume. Treat these as a regression baseline
// for the recovery design, not as desired behaviour. Each affected test says so
// in its own doc comment.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// claudeSubRecoveryNoteText is the interruption notice the agent appends to the
// provider projection of an incomplete assistant message
// (internal/agent/message_convert.go). A request history built after an
// interrupted turn carries it.
const claudeSubRecoveryNoteText = "[response interrupted: this assistant message ended before it was complete]"

// claudeSubRecoveryNote is that notice as the projection appends it.
const claudeSubRecoveryNote = "\n\n" + claudeSubRecoveryNoteText

// claudeSubRecoveryTurnFailure is the error a turn whose CLI result reports
// error_during_execution carries for the result_error.jsonl fixture.
const claudeSubRecoveryTurnFailure = "claude_subscription turn failed: error_during_execution: [Request interrupted by user for tool use]"

// claudeSubRecoveryPartial returns the provider projection of the partial
// assistant message steiner keeps at an interrupt: the text streamed so far,
// the interruption notice, and the nonzero turn the agent recorded.
func claudeSubRecoveryPartial(text string) Message {
	return Message{Role: MessageRoleAssistant, Content: text + claudeSubRecoveryNote, Turn: 1}
}

func claudeSubRecoveryNoop(ChatChunk) error { return nil }

// claudeSubRecoveryRunStream runs one request through the streaming wrapper and
// returns its terminal chunk.
func claudeSubRecoveryRunStream(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) ChatChunk {
	t.Helper()
	stream, err := claudeSubStream(context.Background(), pool, req, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var final ChatChunk
	for chunk := range stream {
		if chunk.Done {
			final = chunk
		}
	}
	if !final.Done {
		t.Fatal("stream emitted no Done chunk")
	}
	return final
}

// claudeSubRecoveryReseedReason returns the reseed reason the terminal chunk's
// diagnostics recorded, or the empty string when the stream carried none.
func claudeSubRecoveryReseedReason(final ChatChunk) string {
	if final.Diagnostics == nil || final.Diagnostics.ClaudeSub == nil {
		return ""
	}
	return final.Diagnostics.ClaudeSub.ReseedReason
}

// claudeSubRecoveryRecordLen returns how many entries the live process record
// for key holds. It reads the pool's session map instead of acquiring, so it can
// never start a process; turns run synchronously in the test goroutine, so the
// record is not being written when it is read.
func claudeSubRecoveryRecordLen(t *testing.T, pool *ClaudeSubscriptionPool, key string) int {
	t.Helper()
	pool.mu.Lock()
	s := pool.sessions[key]
	pool.mu.Unlock()
	if s == nil {
		t.Fatalf("no live session for %q", key)
	}
	return len(s.sync.entries)
}

// TestClaudeSubRecoveryLiveFailureReseedsCharacterization proves a failure
// streamed after the user line was committed leaves the live record ahead of the
// stored checkpoint, so the next request retires the process and reseeds the
// rendered transcript on a fresh one.
//
// CHARACTERIZATION OF A CURRENT BUG: the delivered user line is already
// checkpointed when the turn streams, so a failed turn makes the parent's stored
// state stale and throws away the live CLI session. Both subtests assert the
// reseed, and the second carries the real projection of an interrupted partial
// to show it does not change the outcome.
func TestClaudeSubRecoveryLiveFailureReseedsCharacterization(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name        string
		withPartial bool
	}{
		{name: "user line delivered"},
		{name: "real partial projection in the next request", withPartial: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := &claudeSubScript{batches: [][]claudeSubEvent{
				claudeSubDecodeEvents(t, "turn_text.jsonl"),
				claudeSubDecodeEvents(t, "result_error.jsonl"),
			}}
			// The second spawn answers the reseed of the next request.
			pool, spawner := claudeSubPoolWithScripts(t, script, claudeSubTextTurns(t, 1))
			first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
			stored := claudeSubDecodeState(t, first.BackendState)

			history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
			var streamed []ChatChunk
			err := claudeSubTurn(context.Background(), pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     claudeSubEncodeState(t, stored),
				Messages:         history,
			}, func(chunk ChatChunk) error {
				streamed = append(streamed, chunk)
				return nil
			})
			if err == nil || err.Error() != claudeSubRecoveryTurnFailure {
				t.Fatalf("failure turn error = %v, want %q", err, claudeSubRecoveryTurnFailure)
			}
			for _, chunk := range streamed {
				if chunk.BackendState != nil {
					t.Fatalf("failed turn emitted backend state %s", chunk.BackendState)
				}
			}

			live := claudeSubRecoveryRecordLen(t, pool, "parent")
			if live != stored.SyncEntries+1 {
				t.Fatalf("live record entries = %d, want %d (the delivered user line)", live, stored.SyncEntries+1)
			}

			next := append([]Message(nil), history...)
			if tt.withPartial {
				next = append(next, claudeSubRecoveryPartial("half a report"))
			}
			next = append(next, claudeSubUserText("again"))
			final := claudeSubRecoveryRunStream(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     claudeSubEncodeState(t, stored),
				Messages:         next,
			})
			if final.Error != "" {
				t.Fatalf("next turn error = %s", final.Error)
			}
			if got := spawner.callCount(); got != 2 {
				t.Fatalf("spawn count = %d, want 2 (retired live process plus fresh reseed)", got)
			}
			serving := spawner.call(t, 1)
			if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume != "" {
				t.Errorf("reseed spawn --resume = %q, want none", resume)
			}
			lines := claudeSubUserLines(serving.Conn)
			if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
				t.Fatalf("reseed user lines = %q, want one rendered transcript", lines)
			}
			if tt.withPartial && (!strings.Contains(lines[0], "half a report") || !strings.Contains(lines[0], claudeSubRecoveryNoteText)) {
				t.Errorf("reseed transcript = %s, want the kept partial and its notice", lines[0])
			}
			if got := claudeSubRecoveryReseedReason(final); got != claudeSubReseedCheckpoint {
				t.Errorf("reseed reason = %q, want %q", got, claudeSubReseedCheckpoint)
			}
		})
	}
}

// TestClaudeSubRecoveryToolResultLiveFailureReseedsCharacterization covers the
// same live-record divergence when the delivered message is a tool result
// instead of a user line: the result is committed as it is resolved, so the
// streamed failure after it makes the stored checkpoint stale.
//
// CHARACTERIZATION OF A CURRENT BUG: the tool result is durable in the provider
// record but the failed turn returns no backend state, so the next request
// reseeds a transcript instead of appending the new user message to the live
// process. The fake CLI is driven directly for the failed turn because a tool
// result reaches it through the MCP host, not a user line on stdin.
func TestClaudeSubRecoveryToolResultLiveFailureReseedsCharacterization(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	// Truncated at the assistant message that carries the tool call: the fixture's
	// trailing user tool-result echo and result event stay out of the queue so the
	// first turn ends at the call with no pending output.
	script := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
	}}
	pool, spawner := claudeSubPoolWithScripts(t, script, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	stored := claudeSubDecodeState(t, first.BackendState)
	if len(first.Delta.ToolCalls) != 1 {
		t.Fatalf("first turn tool calls = %+v, want one", first.Delta.ToolCalls)
	}
	result := Message{Role: MessageRoleTool, ToolCallID: first.Delta.ToolCalls[0].ID, Content: "notes body"}
	history := []Message{claudeSubUserText("read notes"), first.Delta, result}

	// The CLI continues on its own after the MCP host resolves the call, so the
	// turn's events are queued before the tool-result turn reads them.
	conn := spawner.call(t, 0).Conn
	for _, ev := range claudeSubDecodeEvents(t, "result_error.jsonl") {
		conn.push(ev)
	}
	var streamed []ChatChunk
	err := claudeSubTurn(context.Background(), pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         history,
	}, func(chunk ChatChunk) error {
		streamed = append(streamed, chunk)
		return nil
	})
	if err == nil || err.Error() != claudeSubRecoveryTurnFailure {
		t.Fatalf("tool-result turn error = %v, want %q", err, claudeSubRecoveryTurnFailure)
	}
	for _, chunk := range streamed {
		if chunk.BackendState != nil {
			t.Fatalf("failed turn emitted backend state %s", chunk.BackendState)
		}
	}

	live := claudeSubRecoveryRecordLen(t, pool, "parent")
	if live != stored.SyncEntries+1 {
		t.Fatalf("live record entries = %d, want %d (the delivered tool result)", live, stored.SyncEntries+1)
	}

	next := append(append([]Message(nil), history...), claudeSubUserText("again"))
	final := claudeSubRecoveryRunStream(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         next,
	})
	if final.Error != "" {
		t.Fatalf("next turn error = %s", final.Error)
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn count = %d, want 2 (retired live process plus fresh reseed)", got)
	}
	serving := spawner.call(t, 1)
	if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume != "" {
		t.Errorf("reseed spawn --resume = %q, want none", resume)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("reseed user lines = %q, want one rendered transcript", lines)
	}
	if got := claudeSubRecoveryReseedReason(final); got != claudeSubReseedCheckpoint {
		t.Errorf("reseed reason = %q, want %q", got, claudeSubReseedCheckpoint)
	}
}

// TestClaudeSubRecoveryPreDeliveryFailureReusesProcess proves a failure before
// the user line is written leaves the live record equal to the stored
// checkpoint, so the retry reuses the process and sends only the undelivered
// suffix.
func TestClaudeSubRecoveryPreDeliveryFailureReusesProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	other := "claude-other-model"
	script := claudeSubTextTurns(t, 2)
	script.setModelErr = true
	pool, spawner := claudeSubPoolWithScripts(t, script)
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	stored := claudeSubDecodeState(t, first.BackendState)

	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
	err := claudeSubTurn(context.Background(), pool, ChatRequest{
		TransportSession: "parent",
		Model:            other,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         history,
	}, claudeSubRecoveryNoop)
	if err == nil || err.Error() != "claude CLI set_model: model unavailable" {
		t.Fatalf("set_model turn error = %v, want the fixture's control failure", err)
	}
	conn := spawner.call(t, 0).Conn
	for _, line := range claudeSubUserLines(conn) {
		if strings.Contains(line, "more") {
			t.Fatalf("pre-delivery failure still wrote the user line: %s", line)
		}
	}
	if live := claudeSubRecoveryRecordLen(t, pool, "parent"); live != stored.SyncEntries {
		t.Fatalf("live record entries = %d, want the stored %d (nothing delivered)", live, stored.SyncEntries)
	}

	script.setModelErr = false
	next := append(append([]Message(nil), history...), claudeSubUserText("again"))
	final := claudeSubRecoveryRunStream(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            other,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         next,
	})
	if final.Error != "" {
		t.Fatalf("retry turn error = %s", final.Error)
	}
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawn count = %d, want 1 (live process reused, no reseed)", got)
	}
	lines := claudeSubUserLines(conn)
	if len(lines) != 2 {
		t.Fatalf("user lines = %q, want the first turn plus the retried suffix", lines)
	}
	last := lines[1]
	if strings.Contains(last, "Conversation so far") {
		t.Errorf("reused process received a reseed transcript: %s", last)
	}
	if !strings.Contains(last, "more") || !strings.Contains(last, "again") {
		t.Errorf("retried user line = %s, want the undelivered suffix and the new message", last)
	}
	if got := claudeSubRecoveryReseedReason(final); got != "" {
		t.Errorf("reseed reason = %q, want none", got)
	}
	if live := claudeSubRecoveryRecordLen(t, pool, "parent"); live != stored.SyncEntries+3 {
		t.Errorf("live record entries = %d, want %d (both retried user messages and the reply)", live, stored.SyncEntries+3)
	}
}

// TestClaudeSubRecoveryCancellationLeavesLateEventsUnfencedCharacterization
// proves a cancelled turn neither interrupts the CLI nor fences the events the
// CLI produces afterwards: the next request on the same process consumes the
// abandoned turn's late events as its own response. No queue drain is exercised
// here, because the events are delivered only after the cancelled turn returned.
//
// CHARACTERIZATION OF A CURRENT BUG: after cancellation the CLI keeps
// generating, so the next request adopts the abandoned turn's assistant text and
// result instead of its own.
func TestClaudeSubRecoveryCancellationLeavesLateEventsUnfencedCharacterization(t *testing.T) {
	model := claudeSubTestSpec().Model
	script := &claudeSubScript{batches: [][]claudeSubEvent{nil, nil}}
	// Spawning seed: the script is installed by the pool's spawner, so the
	// process answers control requests and pushes an empty batch per user line.
	pool, spawner := claudeSubPoolWithScripts(t, script)
	seed, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- claudeSubTurn(ctx, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("first")},
		}, claudeSubRecoveryNoop)
	}()
	// The turn writes the usage-gate control and then its user line before it
	// waits for CLI events, so two sent lines prove the user content is already
	// committed when the test cancels.
	lines := conn.waitSent(t, 2)
	if !strings.Contains(lines[1], `"type":"user"`) {
		t.Fatalf("second sent line = %s, want the user line", lines[1])
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled turn did not return")
	}
	// Cancellation makes the queue pop return !ok, so consume reports its generic
	// connection-closed error. That message says nothing about the process state;
	// the checks below do.
	if err == nil || err.Error() != "claude CLI connection closed" {
		t.Fatalf("cancelled turn error = %v, want \"claude CLI connection closed\"", err)
	}
	if conn.isClosed() {
		t.Error("cancellation closed the CLI process")
	}

	// The CLI keeps generating after steiner stops consuming, so deliver the
	// late events of the abandoned turn as they would arrive on the connection.
	for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl") {
		conn.push(ev)
	}
	final := claudeSubRecoveryRunStream(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if final.Error != "" {
		t.Fatalf("second turn error = %s", final.Error)
	}
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawn count = %d, want 1 (cancelled process reused)", got)
	}
	if got := final.Delta.Content; got != "Hello world" {
		t.Fatalf("second turn text = %q, want the abandoned turn's late text %q", got, "Hello world")
	}
	if got := claudeSubRecoveryReseedReason(final); got != "" {
		t.Errorf("reseed reason = %q, want none (same process reused)", got)
	}
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"subtype":"interrupt"`) {
			t.Errorf("cancellation path sent a control interrupt: %s", line)
		}
	}
}

// TestClaudeSubRecoveryRestartAfterInterruptedTurn proves the restore path
// independently of any live process: without a committed partial the stored
// checkpoint resumes at its saved uuid, and the committed partial the agent
// keeps is what makes the restore refuse and reseed.
//
// CHARACTERIZATION OF A CURRENT BUG (the reseeding subtest): steiner holds the
// interrupted assistant text locally, the CLI record does not, and the restore
// treats it as an unappendable message, so a restart renders the whole
// transcript instead of resuming the saved session.
func TestClaudeSubRecoveryRestartAfterInterruptedTurn(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name       string
		partial    bool
		wantResume bool
		wantReason string
	}{
		{name: "absent partial resumes at the saved uuid", wantResume: true},
		{name: "committed partial reseeds", partial: true, wantReason: claudeSubReseedPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			firstPool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			first := claudeSubRunTurn(t, firstPool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
			stored := claudeSubDecodeState(t, first.BackendState)

			// Restart: a new pool has no live process for the key.
			pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			msgs := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
			if tt.partial {
				msgs = append(msgs, claudeSubRecoveryPartial("half a report"))
			}
			msgs = append(msgs, claudeSubUserText("again"))
			final := claudeSubRecoveryRunStream(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     claudeSubEncodeState(t, stored),
				Messages:         msgs,
			})
			if final.Error != "" {
				t.Fatalf("turn error = %s", final.Error)
			}
			if got := spawner.callCount(); got != 1 {
				t.Fatalf("spawn count = %d, want 1", got)
			}
			serving := spawner.call(t, 0)
			resume, _ := claudeSubArgValue(serving.Args, "--resume")
			at, _ := claudeSubArgValue(serving.Args, "--resume-session-at")
			if (resume != "") != tt.wantResume {
				t.Errorf("resumed = %v, want %v (args %q)", resume != "", tt.wantResume, serving.Args)
			}
			if tt.wantResume && (resume != stored.CLISessionID || at != stored.FinalUUID) {
				t.Errorf("resume target = %q at %q, want %q at %q", resume, at, stored.CLISessionID, stored.FinalUUID)
			}
			if got := claudeSubRecoveryReseedReason(final); got != tt.wantReason {
				t.Errorf("reseed reason = %q, want %q", got, tt.wantReason)
			}
			lines := claudeSubUserLines(serving.Conn)
			if len(lines) != 1 {
				t.Fatalf("user lines = %q, want one", lines)
			}
			reseeded := strings.Contains(lines[0], "Conversation so far")
			if reseeded == tt.wantResume {
				t.Errorf("transcript reseed = %v, want %v: %s", reseeded, !tt.wantResume, lines[0])
			}
			if !strings.Contains(lines[0], "again") {
				t.Errorf("user line = %s, want it to carry the new message", lines[0])
			}
			if reseeded && (!strings.Contains(lines[0], "half a report") || !strings.Contains(lines[0], claudeSubRecoveryNoteText)) {
				t.Errorf("reseeded transcript = %s, want the kept partial and its notice", lines[0])
			}
		})
	}
}
