package provider

// Regression tests for cancelling a claude_subscription turn. A cancelled turn
// that reached the CLI leaves events steiner cannot attribute to any later call,
// so the process is retired before it can serve one: no subsequent call may
// consume the abandoned turn's output.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// claudeSubStaleText is the assistant text of the abandoned-turn batch below.
// It never appears in a scripted answer, so a later call that adopts it is
// unmistakable.
const claudeSubStaleText = "abandoned turn text"

// claudeSubStaleTurnBatch is the text turn fixture with its answer text
// replaced, standing for the late events a cancelled turn leaves behind.
func claudeSubStaleTurnBatch(t *testing.T) []claudeSubEvent {
	t.Helper()
	events := claudeSubDecodeEvents(t, "turn_text.jsonl")
	for i := range events {
		raw := strings.ReplaceAll(string(events[i].Raw), "Hello world", claudeSubStaleText)
		events[i].Raw = json.RawMessage(raw)
	}
	return events
}

// claudeSubCancelTurnAfter starts req on a cancelable context, cancels it once
// conn has sent at least want lines and returns the turn's error. wantUser
// additionally requires the last line to be the delivered user line, which is
// what makes the process unsafe for reuse.
func claudeSubCancelTurnAfter(t *testing.T, pool *ClaudeSubscriptionPool, conn *claudeSubFakeConn, want int, wantUser bool, req ChatRequest) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- claudeSubTurn(ctx, pool, req, claudeSubRecoveryNoop)
	}()
	lines := conn.waitSent(t, want)
	if last := lines[want-1]; wantUser && !strings.Contains(last, `"type":"user"`) {
		t.Fatalf("sent line %d = %s, want the delivered user line", want, last)
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled turn did not return")
		return nil
	}
}

// claudeSubCancelMidTurn cancels a turn whose user line already reached the CLI.
func claudeSubCancelMidTurn(t *testing.T, pool *ClaudeSubscriptionPool, conn *claudeSubFakeConn, want int, req ChatRequest) error {
	t.Helper()
	return claudeSubCancelTurnAfter(t, pool, conn, want, true, req)
}

// claudeSubCollected records the chunks one turn emitted.
type claudeSubCollected struct {
	chunks []ChatChunk
	err    error
}

// claudeSubRunCollected runs one request on a background context and records
// every chunk it emits.
func claudeSubRunCollected(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) claudeSubCollected {
	t.Helper()
	var got claudeSubCollected
	got.err = claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
		got.chunks = append(got.chunks, chunk)
		return nil
	})
	return got
}

// final returns the last terminal chunk the turn emitted.
func (c claudeSubCollected) final(t *testing.T) ChatChunk {
	t.Helper()
	var final ChatChunk
	for _, chunk := range c.chunks {
		if chunk.Done {
			final = chunk
		}
	}
	if !final.Done {
		t.Fatal("turn emitted no Done chunk")
	}
	return final
}

// warnings returns the diagnostic warnings the turn emitted.
func (c claudeSubCollected) warnings() []string {
	var out []string
	for _, chunk := range c.chunks {
		if chunk.Diagnostic != "" {
			out = append(out, chunk.Diagnostic)
		}
	}
	return out
}

// claudeSubWantOneWarning asserts the turn emitted exactly one warning and
// returns it.
func claudeSubWantOneWarning(t *testing.T, got claudeSubCollected, wantText string) {
	t.Helper()
	warnings := got.warnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one", warnings)
	}
	if !strings.Contains(warnings[0], wantText) {
		t.Errorf("warning = %q, want it to mention %q", warnings[0], wantText)
	}
}

// claudeSubLiveSession returns the pooled session for key, or nil.
func claudeSubLiveSession(pool *ClaudeSubscriptionPool, key string) *claudeSubSession {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.sessions[key]
}

// TestClaudeSubCancelReplacesUnsafeProcess proves a cancelled turn's process
// never serves the next request: the next call retires it, starts a fresh CLI
// session, does not see the abandoned turn's late events, and says so.
func TestClaudeSubCancelReplacesUnsafeProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)

	err = claudeSubCancelMidTurn(t, pool, conn, 2, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first")},
	})
	// Cancellation makes the queue pop return !ok, so consume reports its generic
	// connection-closed error. The process state is what the checks below pin.
	if err == nil || err.Error() != "claude CLI connection closed" {
		t.Fatalf("cancelled turn error = %v, want \"claude CLI connection closed\"", err)
	}
	if conn.isClosed() {
		t.Error("cancellation tore the process down inline; want it retired at the next use")
	}

	// The CLI keeps generating after steiner stops consuming, so deliver the
	// abandoned turn's late events as they would arrive on the connection.
	for _, ev := range claudeSubStaleTurnBatch(t) {
		conn.push(ev)
	}

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if got.err != nil {
		t.Fatalf("next turn error = %v", got.err)
	}
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("next turn chunk error = %s", final.Error)
	}
	if final.Delta.Content != "Hello world" {
		t.Fatalf("next turn text = %q, want its own %q, not the cancelled turn's %q", final.Delta.Content, "Hello world", claudeSubStaleText)
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn count = %d, want 2 (the unsafe process replaced)", got)
	}
	if !conn.isClosed() {
		t.Error("the cancelled turn's process was reused instead of retired")
	}
	lines := claudeSubUserLines(spawner.call(t, 1).Conn)
	if len(lines) != 1 {
		t.Fatalf("replacement user lines = %q, want one", lines)
	}
	if strings.Contains(lines[0], claudeSubStaleText) {
		t.Errorf("replacement received the cancelled turn's text: %s", lines[0])
	}
	claudeSubWantOneWarning(t, got, claudeSubReplacedProcessWarning)
}

// TestClaudeSubCancelRecoveryWarning proves the recovery warning describes what
// actually happened: a restorable saved checkpoint is resumed, and a kept
// partial forces the transcript to be rebuilt. Both replace the process first.
func TestClaudeSubCancelRecoveryWarning(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name       string
		partial    bool
		wantResume bool
		wantText   string
	}{
		{
			name:       "saved checkpoint resumes",
			wantResume: true,
			wantText:   claudeSubReplacedProcessWarning,
		},
		{
			name:     "kept partial rebuilds the transcript",
			partial:  true,
			wantText: claudeSubReplacedProcessWarning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{
				claudeSubDecodeEvents(t, "turn_text.jsonl"),
				nil,
			}}, claudeSubTextTurns(t, 1))
			first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
			stored := claudeSubDecodeState(t, first.BackendState)
			conn := spawner.call(t, 0).Conn

			history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
			if err := claudeSubCancelMidTurn(t, pool, conn, 4, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     claudeSubEncodeState(t, stored),
				Messages:         history,
			}); err == nil {
				t.Fatal("cancelled turn returned no error")
			}
			for _, ev := range claudeSubStaleTurnBatch(t) {
				conn.push(ev)
			}

			next := append([]Message(nil), history...)
			if tt.partial {
				next = append(next, claudeSubRecoveryPartial("half a report"))
			}
			next = append(next, claudeSubUserText("again"))
			got := claudeSubRunCollected(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				BackendState:     claudeSubEncodeState(t, stored),
				Messages:         next,
			})
			if got.err != nil {
				t.Fatalf("next turn error = %v", got.err)
			}
			if text := got.final(t).Delta.Content; text != "Hello world" {
				t.Fatalf("next turn text = %q, want its own %q", text, "Hello world")
			}
			if got := spawner.callCount(); got != 2 {
				t.Fatalf("spawn count = %d, want 2 (the unsafe process replaced)", got)
			}
			serving := spawner.call(t, 1)
			resume, _ := claudeSubArgValue(serving.Args, "--resume")
			if (resume != "") != tt.wantResume {
				t.Errorf("replacement --resume = %q, want present %v", resume, tt.wantResume)
			}
			if !conn.isClosed() {
				t.Error("the cancelled turn's process was reused instead of retired")
			}
			lines := claudeSubUserLines(serving.Conn)
			if len(lines) != 1 {
				t.Fatalf("replacement user lines = %q, want one", lines)
			}
			if reseeded := strings.Contains(lines[0], "Conversation so far"); reseeded == tt.wantResume {
				t.Errorf("transcript rebuilt = %v, want %v: %s", reseeded, !tt.wantResume, lines[0])
			}
			if strings.Contains(lines[0], claudeSubStaleText) {
				t.Errorf("replacement received the cancelled turn's text: %s", lines[0])
			}
			claudeSubWantOneWarning(t, got, tt.wantText)
		})
	}
}

// TestClaudeSubCancelAfterTerminalKeepsProcess proves a cancellation that races
// a terminal result already consumed changes nothing: the turn completed, so its
// process stays reusable and no warning is reported.
func TestClaudeSubCancelAfterTerminalKeepsProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := claudeSubUserText("read notes")
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{first},
	}, func(chunk ChatChunk) error {
		if chunk.Done {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("turn error = %v, want none for a consumed terminal result", err)
	}

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{first, {Role: MessageRoleAssistant, Content: "Hello world"}, claudeSubUserText("more")},
	})
	if got.err != nil {
		t.Fatalf("next turn error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Errorf("next turn text = %q, want %q", text, "Hello world")
	}
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawn count = %d, want 1 (the completed process reused)", got)
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}

// TestClaudeSubCancelBeforeDeliveryKeepsProcess proves a cancellation the usage
// gate absorbs writes nothing to the CLI, so the live process is still safe to
// reuse and no replacement is reported.
func TestClaudeSubCancelBeforeDeliveryKeepsProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	script := claudeSubTextTurns(t, 2)
	pool, spawner := claudeSubPoolWithScripts(t, script)
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	stored := claudeSubDecodeState(t, first.BackendState)
	conn := spawner.call(t, 0).Conn

	// An unconfirmable usage reply keeps the gate polling, so no user line is
	// written before the cancellation lands.
	script.usage = json.RawMessage(`{"rate_limits_available":false}`)
	err := claudeSubCancelTurnAfter(t, pool, conn, 3, false, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")},
	})
	if err == nil {
		t.Fatal("cancelled gate returned no error")
	}
	if lines := claudeSubUserLines(conn); len(lines) != 1 {
		t.Fatalf("user lines = %q, want only the first turn's (nothing delivered)", lines)
	}
	script.usage = nil

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more"), claudeSubUserText("again")},
	})
	if got.err != nil {
		t.Fatalf("next turn error = %v", got.err)
	}
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawn count = %d, want 1 (the live process reused)", got)
	}
	if conn.isClosed() {
		t.Error("a pre-delivery cancellation retired the live process")
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}

// TestClaudeSubCancelAfterToolCallDoneKeepsProcess proves a cancellation that
// lands on a tool-call terminal chunk leaves no unfenced events: the next call
// resolves the pending call on the same process.
func TestClaudeSubCancelAfterToolCallDoneKeepsProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	user := claudeSubUserText("read notes")
	var callDone ChatChunk
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         []Message{user},
	}, func(chunk ChatChunk) error {
		if chunk.Done {
			callDone = chunk
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tool turn error = %v", err)
	}
	if len(callDone.Delta.ToolCalls) != 1 {
		t.Fatalf("tool turn chunk = %+v, want one tool call", callDone)
	}
	conn := spawner.call(t, 0).Conn
	result := Message{Role: MessageRoleTool, ToolCallID: callDone.Delta.ToolCalls[0].ID, Content: "notes body"}
	for _, ev := range claudeSubTextTurns(t, 1).batches[0] {
		conn.push(ev)
	}

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         []Message{user, callDone.Delta, result},
	})
	if got.err != nil {
		t.Fatalf("tool result turn error = %v", got.err)
	}
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawn count = %d, want 1 (the live process reused)", got)
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"subtype":"interrupt"`) {
			t.Errorf("the tool result interrupted a non-stale call: %s", line)
		}
	}
}

// TestClaudeSubCancelAdvisorReplacement proves a cancelled advisor delivery does
// not leak into the next advisor call: the advisor process is replaced, the
// snapshot is rendered again, and the replacement is reported.
func TestClaudeSubCancelAdvisorReplacement(t *testing.T) {
	model := claudeSubTestSpec().Model
	key := claudeSubAdvisorSessionKey("parent")
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), key, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)

	req := ChatRequest{
		ParentTransportSession: "parent",
		AdvisorCacheProfile:    true,
		Model:                  model,
		Messages:               []Message{claudeSubUserText("review the change")},
	}
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, req); err == nil {
		t.Fatal("cancelled advisor turn returned no error")
	}
	for _, ev := range claudeSubStaleTurnBatch(t) {
		conn.push(ev)
	}

	got := claudeSubRunCollected(t, pool, req)
	if got.err != nil {
		t.Fatalf("next advisor turn error = %v", got.err)
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("next advisor text = %q, want its own %q", text, "Hello world")
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn count = %d, want 2 (the unsafe advisor replaced)", got)
	}
	if !conn.isClosed() {
		t.Error("the cancelled advisor process was reused instead of retired")
	}
	if lines := claudeSubUserLines(spawner.call(t, 1).Conn); len(lines) != 1 || strings.Contains(lines[0], claudeSubStaleText) {
		t.Errorf("replacement advisor user lines = %q, want one own snapshot", lines)
	}
	claudeSubWantOneWarning(t, got, claudeSubAdvisorReplacementWarning)
}

// TestClaudeSubCancelSummaryReplacement proves a summary never runs on, or
// forks, a process a cancelled turn left unsafe: the parent is replaced and the
// summary is rebuilt on an ephemeral helper from the recorded transcript.
func TestClaudeSubCancelSummaryReplacement(t *testing.T) {
	model := claudeSubTestSpec().Model
	parent := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubDecodeEvents(t, "turn_text.jsonl"),
		nil,
	}}
	pool, spawner := claudeSubPoolWithScripts(t, parent, claudeSubTextTurns(t, 1), claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	stored := claudeSubDecodeState(t, first.BackendState)
	conn := spawner.call(t, 0).Conn
	if err := claudeSubCancelMidTurn(t, pool, conn, 4, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")},
	}); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	for _, ev := range claudeSubStaleTurnBatch(t) {
		conn.push(ev)
	}

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    model,
		Messages:                 []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more"), claudeSubUserText("summarize the older part")},
		BackendState:             claudeSubEncodeState(t, stored),
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got.err != nil {
		t.Fatalf("summary error = %v", got.err)
	}
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("summary chunk error = %s", final.Error)
	}
	if !final.ClearBackendState {
		t.Error("summary did not clear the parent's backend state")
	}
	if text := final.Delta.Content; text != "Hello world" {
		t.Fatalf("summary text = %q, want the helper's own %q", text, "Hello world")
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn count = %d, want 2 (the unsafe parent replaced by a helper)", got)
	}
	if !conn.isClosed() {
		t.Error("the cancelled turn's process was reused for the summary")
	}
	if live := claudeSubLiveSession(pool, "parent"); live != nil {
		t.Error("the cancelled parent process is still pooled")
	}
	for _, line := range claudeSubUserLines(conn) {
		if strings.Contains(line, "summary") {
			t.Errorf("the summary was written to the unsafe parent: %s", line)
		}
	}
	helperLines := claudeSubUserLines(spawner.call(t, 1).Conn)
	if len(helperLines) != 1 || !strings.Contains(helperLines[0], "Conversation so far") {
		t.Fatalf("helper user lines = %q, want one rendered transcript", helperLines)
	}
	if strings.Contains(helperLines[0], claudeSubStaleText) {
		t.Errorf("helper transcript carries the cancelled turn's text: %s", helperLines[0])
	}
	// The summary is an auxiliary request: it recovers nothing of the
	// conversation, so it makes no replacement claim. The notice stays pending
	// for the call that does recover it.
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("summary warnings = %q, want none", warnings)
	}
	if !pool.replacementPending("parent") {
		t.Fatal("the parent replacement notice did not survive the summary")
	}
	next := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes"), claudeSubUserText("after the summary")},
	})
	if next.err != nil {
		t.Fatalf("next parent turn error = %v", next.err)
	}
	claudeSubWantOneWarning(t, next, claudeSubReplacedProcessWarning)
	if pool.replacementPending("parent") {
		t.Error("the parent replacement notice survived being reported")
	}
}

// TestClaudeSubCancelReplacementFailureIsVisible proves a failed replacement
// leaves no unsafe process behind and reports the failure without claiming a
// recovery that did not happen; a later attempt then replaces it and warns.
func TestClaudeSubCancelReplacementFailureIsVisible(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first")},
	}); err == nil {
		t.Fatal("cancelled turn returned no error")
	}

	spawner.setErr(errors.New("spawn refused"))
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "spawn refused") {
		t.Fatalf("replacement failure = %v, want the spawn error", got.err)
	}
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a recovery that did not happen", warnings)
	}
	if !conn.isClosed() {
		t.Error("the unsafe process survived a failed replacement")
	}
	if live := claudeSubLiveSession(pool, "parent"); live != nil {
		t.Error("a session stayed pooled after the replacement failed")
	}

	spawner.setErr(nil)
	retry := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if retry.err != nil {
		t.Fatalf("retried turn error = %v", retry.err)
	}
	if text := retry.final(t).Delta.Content; text != "Hello world" {
		t.Errorf("retried turn text = %q, want %q", text, "Hello world")
	}
	if got := spawner.callCount(); got != 2 {
		t.Errorf("spawn count = %d, want 2 (the failed startup plus this one)", got)
	}
	// The failed attempt told the user why no recovery happened and left the
	// notice pending, so the retry reports the replacement it actually made.
	if warnings := retry.warnings(); len(warnings) != 1 {
		t.Fatalf("retried warnings = %q, want exactly one", warnings)
	}
	claudeSubWantOneWarning(t, retry, claudeSubReplacedProcessWarning)
	if pool.replacementPending("parent") {
		t.Error("the replacement notice survived being reported")
	}
}

// TestClaudeSubDeliverRefusesUnsafeSession proves the delivery guard: even a
// caller that misses the replacement check cannot write to, or consume from, a
// process a cancelled turn left behind.
func TestClaudeSubDeliverRefusesUnsafeSession(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	s.markUnsafe()
	err = claudeSubDeliver(context.Background(), s, ChatRequest{TransportSession: "parent", Model: claudeSubTestSpec().Model}, claudeSubDelta{
		User: []Message{claudeSubUserText("first")},
	}, claudeSubRecoveryNoop)
	if !errors.Is(err, errClaudeSubSessionUnsafe) {
		t.Fatalf("deliver error = %v, want %v", err, errClaudeSubSessionUnsafe)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn
	if !conn.isClosed() {
		t.Error("the refused process was left running")
	}
	if lines := conn.sentLines(); len(lines) != 0 {
		t.Errorf("the refused process received %q, want nothing", lines)
	}
	if live := claudeSubLiveSession(pool, "parent"); live != nil {
		t.Error("the unsafe session stayed pooled")
	}
}

// TestClaudeSubCancelNoticeSurvivesIdleReap proves the replacement notice is not
// tied to the abandoned process: reaping that process before the recovery must
// not turn the recovery silent.
func TestClaudeSubCancelNoticeSurvivesIdleReap(t *testing.T) {
	model := claudeSubTestSpec().Model
	key := claudeSubAdvisorSessionKey("parent")
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), key, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	req := ChatRequest{
		ParentTransportSession: "parent",
		AdvisorCacheProfile:    true,
		Model:                  model,
		Messages:               []Message{claudeSubUserText("review the change")},
	}
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, req); err == nil {
		t.Fatal("cancelled advisor turn returned no error")
	}
	pool.reapIdleAdvisors(time.Now().Add(2 * time.Hour))
	if live := claudeSubLiveSession(pool, key); live != nil {
		t.Fatal("the idle reap left the cancelled advisor pooled")
	}
	if !pool.replacementPending(key) {
		t.Fatal("the idle reap dropped the replacement notice")
	}

	got := claudeSubRunCollected(t, pool, req)
	if got.err != nil {
		t.Fatalf("advisor turn error = %v", got.err)
	}
	claudeSubWantOneWarning(t, got, claudeSubAdvisorReplacementWarning)
}

// TestClaudeSubCancelNoticeSurvivesEviction proves the notice survives the
// abandoned process exiting on its own and being evicted before the next call.
func TestClaudeSubCancelNoticeSurvivesEviction(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first")},
	}); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	// The abandoned CLI exiting on its own must not swallow the notice.
	if err := conn.Close(context.Background()); err != nil {
		t.Fatalf("close fake conn: %v", err)
	}
	live := claudeSubLiveSession(pool, "parent")
	if live == nil {
		t.Fatal("the cancelled session left the pool before eviction")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !live.exited() {
		if time.Now().After(deadline) {
			t.Fatal("the cancelled session never reported exit")
		}
		time.Sleep(time.Millisecond)
	}

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if got.err != nil {
		t.Fatalf("next turn error = %v", got.err)
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn count = %d, want 2 (the exited process evicted)", got)
	}
	claudeSubWantOneWarning(t, got, claudeSubReplacedProcessWarning)
}

// TestClaudeSubCancelWarningReportsRetriedOutcome proves the warning describes
// the attempt that served the user: a resumed attempt that fails before output
// and is retried as a reseed must report the rebuild, not the resume.
func TestClaudeSubCancelWarningReportsRetriedOutcome(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t,
		&claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl"), nil}},
		&claudeSubScript{batches: [][]claudeSubEvent{claudeSubFailedResumeBatch()}},
		claudeSubTextTurns(t, 1),
	)
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	stored := claudeSubDecodeState(t, first.BackendState)
	conn := spawner.call(t, 0).Conn
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
	if err := claudeSubCancelMidTurn(t, pool, conn, 4, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         history,
	}); err == nil {
		t.Fatal("cancelled turn returned no error")
	}

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         append(append([]Message(nil), history...), claudeSubUserText("again")),
	})
	if got.err != nil {
		t.Fatalf("turn error = %v", got.err)
	}
	if got := spawner.callCount(); got != 3 {
		t.Fatalf("spawn count = %d, want 3 (replacement resumed, failed, reseeded)", got)
	}
	warnings := got.warnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one", warnings)
	}
	if want := claudeSubWarningChunk(claudeSubReplacedProcessWarning).Diagnostic; warnings[0] != want {
		t.Errorf("warning = %q, want the wording that claims no resume or rebuild: %q", warnings[0], want)
	}
	if pool.replacementPending("parent") {
		t.Error("the replacement notice survived being reported")
	}
}

// TestClaudeSubFinishPendingCancelAbandonsProcess proves a cancellation during
// the stale-pending interrupt leaves the process unusable and the replacement
// reportable, because the interrupt request may already be on its way.
func TestClaudeSubFinishPendingCancelAbandonsProcess(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "parent", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	if s.beginPendingCall("toolu_a") == nil {
		t.Fatal("no MCP host handle for the pending call")
	}
	conn := spawner.call(t, 0).Conn

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = nil
	done := make(chan error, 1)
	go func() {
		done <- claudeSubFinishPending(ctx, s, ChatRequest{TransportSession: "parent", Model: spec.Model, Messages: []Message{claudeSubUserText("next")}})
	}()
	lines := conn.waitSent(t, 1)
	if !strings.Contains(lines[0], `"subtype":"interrupt"`) {
		t.Fatalf("sent line = %s, want the interrupt control request", lines[0])
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled interrupt did not return")
	}
	if err == nil {
		t.Fatal("cancelled interrupt returned no error")
	}
	if !s.unsafeForReuse() {
		t.Error("a cancelled stale-pending interrupt left the process reusable")
	}
	if !pool.replacementPending("parent") {
		t.Error("a cancelled stale-pending interrupt left no replacement notice")
	}
}

// TestClaudeSubUsageLimitMidTurnAbandonsProcess proves a usage-limit event that
// stops a turn mid-flight leaves the process unusable, because the CLI may still
// be producing the rest of that turn.
func TestClaudeSubUsageLimitMidTurnAbandonsProcess(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	conn.push(claudeSubEvent{Type: "rate_limit_event", Raw: json.RawMessage(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","resetsAt":1791928800}}`)})

	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first")},
	})
	if got.err == nil {
		t.Fatal("usage-limited turn returned no error")
	}
	if _, ok := AsUsageLimit(got.err); !ok {
		t.Fatalf("turn error = %v, want a usage limit", got.err)
	}
	live := claudeSubLiveSession(pool, "parent")
	if live == nil {
		t.Fatal("the usage-limited process left the pool before replacement")
	}
	if !live.unsafeForReuse() {
		t.Error("a usage-limited mid-turn process stayed reusable")
	}
	if !pool.replacementPending("parent") {
		t.Error("a usage-limited mid-turn process left no replacement notice")
	}

	next := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("again")},
	})
	if next.err != nil {
		t.Fatalf("next turn error = %v", next.err)
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn count = %d, want 2 (the limited process replaced)", got)
	}
	claudeSubWantOneWarning(t, next, claudeSubReplacedProcessWarning)
}

// TestClaudeSubFoldStreamKeepsWarnings proves a folded, non-streaming call still
// carries the provider warning, so the caller can surface it instead of dropping
// it with the stream.
func TestClaudeSubFoldStreamKeepsWarnings(t *testing.T) {
	stream := make(chan ChatChunk, 2)
	stream <- ChatChunk{Diagnostic: "provider replaced its process", Severity: "warning"}
	stream <- ChatChunk{Done: true, Delta: Message{Role: MessageRoleAssistant, Content: "answer"}}
	close(stream)
	response, err := claudeSubFoldStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("fold error = %v", err)
	}
	if response.Message.Content != "answer" {
		t.Errorf("content = %q, want %q", response.Message.Content, "answer")
	}
	if len(response.Warnings) != 1 || response.Warnings[0] != "provider replaced its process" {
		t.Errorf("warnings = %q, want the streamed warning", response.Warnings)
	}
}

// TestClaudeSubCancelSubAgentSummaryKeepsNotice proves a sub-agent summary does
// not consume the replacement notice of the conversation it summarizes: the
// summary recovers nothing, so the next sub-agent turn reports the replacement.
func TestClaudeSubCancelSubAgentSummaryKeepsNotice(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t,
		&claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl"), nil}},
		claudeSubTextTurns(t, 1),
		claudeSubTextTurns(t, 1),
		claudeSubTextTurns(t, 1),
	)
	u1 := claudeSubUserText("look")
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent-agent-1", ParentTransportSession: "parent", Model: model, Messages: []Message{u1}})
	conn := spawner.call(t, 0).Conn
	if err := claudeSubCancelMidTurn(t, pool, conn, 4, ChatRequest{
		TransportSession:       "parent-agent-1",
		ParentTransportSession: "parent",
		Model:                  model,
		Messages:               []Message{u1, first.Delta, claudeSubUserText("more")},
	}); err == nil {
		t.Fatal("cancelled sub-agent turn returned no error")
	}
	for _, ev := range claudeSubStaleTurnBatch(t) {
		conn.push(ev)
	}

	summary := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:         "parent-agent-1",
		ParentTransportSession:   "parent",
		Model:                    model,
		Messages:                 []Message{u1, first.Delta, claudeSubUserText("summarize")},
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if summary.err != nil {
		t.Fatalf("sub-agent summary error = %v", summary.err)
	}
	if warnings := summary.warnings(); len(warnings) != 0 {
		t.Errorf("sub-agent summary warnings = %q, want none", warnings)
	}
	if !pool.replacementPending("parent-agent-1") {
		t.Fatal("the sub-agent summary dropped the replacement notice")
	}
	if live := claudeSubLiveSession(pool, "parent-agent-1"); live != nil {
		t.Error("the cancelled sub-agent process stayed pooled after its summary")
	}

	next := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:       "parent-agent-1",
		ParentTransportSession: "parent",
		Model:                  model,
		Messages:               []Message{u1, claudeSubUserText("continue")},
	})
	if next.err != nil {
		t.Fatalf("next sub-agent turn error = %v", next.err)
	}
	if text := next.final(t).Delta.Content; text != "Hello world" {
		t.Errorf("next sub-agent text = %q, want its own %q", text, "Hello world")
	}
	claudeSubWantOneWarning(t, next, claudeSubReplacedProcessWarning)
}

// waitCallCount waits until the spawner has recorded at least n spawns.
func (s *claudeSubTestSpawner) waitCallCount(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s.callCount() >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("spawner recorded %d calls, want at least %d", s.callCount(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// claudeSubAbandonedParent seeds a pool whose parent process a cancelled turn
// abandoned, so a pending replacement notice exists for the key.
func claudeSubAbandonedParent(t *testing.T, scripts ...*claudeSubScript) (*ClaudeSubscriptionPool, *claudeSubTestSpawner, *claudeSubFakeConn) {
	t.Helper()
	pool, spawner := claudeSubPoolWithScripts(t, scripts...)
	seed, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("first")},
	}); err == nil {
		t.Fatal("cancelled turn returned no error")
	}
	if !pool.replacementPending("parent") {
		t.Fatal("the cancelled turn left no replacement notice")
	}
	return pool, spawner, conn
}

// TestClaudeSubFoldedCallReportsPendingNotice is the end-to-end non-streaming
// regression: a folded ChatCompletion reports the replacement and only then
// clears the notice.
func TestClaudeSubFoldedCallReportsPendingNotice(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, _, _ := claudeSubAbandonedParent(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	prov := NewClaudeSubscriptionProvider(pool, nil)
	response, err := prov.ChatCompletion(context.Background(), ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if err != nil {
		t.Fatalf("folded call error = %v", err)
	}
	if response.Message.Content != "Hello world" {
		t.Errorf("content = %q, want %q", response.Message.Content, "Hello world")
	}
	if len(response.Warnings) != 1 || response.Warnings[0] != claudeSubWarningChunk(claudeSubReplacedProcessWarning).Diagnostic {
		t.Fatalf("warnings = %q, want the replacement warning", response.Warnings)
	}
	if pool.replacementPending("parent") {
		t.Error("a successful folded call left the notice pending")
	}
}

// TestClaudeSubFoldedCallErrorKeepsNotice is the end-to-end failure regression:
// a folded call streams a content chunk (so the warning is already buffered) and
// then fails, which discards the response; the notice must survive and be
// reported by the next request.
func TestClaudeSubFoldedCallErrorKeepsNotice(t *testing.T) {
	model := claudeSubTestSpec().Model
	// The warning is emitted before the first chunk, so this batch proves the
	// warning was buffered before the failing result.
	bufferedWarning := append(claudeSubPartialTextBatch(), claudeSubFailedResumeBatch()...)
	pool, spawner, _ := claudeSubAbandonedParent(t,
		&claudeSubScript{batches: [][]claudeSubEvent{nil}},
		&claudeSubScript{batches: [][]claudeSubEvent{bufferedWarning, claudeSubDecodeEvents(t, "turn_text.jsonl")}},
	)
	prov := NewClaudeSubscriptionProvider(pool, nil)
	failedRequest := ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	}
	if _, err := prov.ChatCompletion(context.Background(), failedRequest); err == nil {
		t.Fatal("folded call returned no error for a failed turn")
	}
	if !pool.replacementPending("parent") {
		t.Fatal("a failed folded call dropped the replacement notice")
	}
	request := failedRequest
	request.Messages = append(append([]Message(nil), failedRequest.Messages...), claudeSubUserText("third"))
	response, err := prov.ChatCompletion(context.Background(), request)
	if err != nil {
		t.Fatalf("retried folded call error = %v", err)
	}
	if len(response.Warnings) != 1 {
		t.Fatalf("warnings = %q, want the replacement warning on the call that succeeds", response.Warnings)
	}
	if got := spawner.callCount(); got != 2 {
		t.Errorf("spawn count = %d, want 2 (unsafe process and the failed attempt)", got)
	}
	if pool.replacementPending("parent") {
		t.Error("the notice survived being reported")
	}
}

// TestClaudeSubFoldedCallCancellationKeepsNotice is the end-to-end cancellation
// regression: a folded call cancelled after the warning is buffered cannot
// deliver it, so the notice must stay pending.
func TestClaudeSubFoldedCallCancellationKeepsNotice(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner, _ := claudeSubAbandonedParent(t,
		&claudeSubScript{batches: [][]claudeSubEvent{nil}},
		&claudeSubScript{batches: [][]claudeSubEvent{nil}},
	)
	prov := NewClaudeSubscriptionProvider(pool, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := prov.ChatCompletion(ctx, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
		})
		done <- err
	}()
	spawner.waitCallCount(t, 2)
	spawner.call(t, 1).Conn.waitSent(t, 2)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled folded call returned no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled folded call did not return")
	}
	if !pool.replacementPending("parent") {
		t.Error("a cancelled folded call dropped the replacement notice")
	}
}

// TestClaudeSubCancelAdvisorReplacementFailure proves a failed advisor
// replacement reports its error, makes no recovery claim, and keeps the notice
// for the next advisor call.
func TestClaudeSubCancelAdvisorReplacementFailure(t *testing.T) {
	key := claudeSubAdvisorSessionKey("parent")
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{nil}}, claudeSubTextTurns(t, 1))
	seed, err := pool.acquire(context.Background(), key, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	pool.release(seed)
	req := ChatRequest{
		ParentTransportSession: "parent",
		AdvisorCacheProfile:    true,
		Model:                  claudeSubTestSpec().Model,
		Messages:               []Message{claudeSubUserText("review the change")},
	}
	if err := claudeSubCancelMidTurn(t, pool, conn, 2, req); err == nil {
		t.Fatal("cancelled advisor turn returned no error")
	}
	spawner.setErr(errors.New("spawn refused"))
	failed := claudeSubRunCollected(t, pool, req)
	if failed.err == nil || !strings.Contains(failed.err.Error(), "spawn refused") {
		t.Fatalf("advisor replacement failure = %v, want the spawn error", failed.err)
	}
	if warnings := failed.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a recovery that did not happen", warnings)
	}
	if !pool.replacementPending(key) {
		t.Fatal("the failed advisor replacement dropped the notice")
	}
	spawner.setErr(nil)
	got := claudeSubRunCollected(t, pool, req)
	if got.err != nil {
		t.Fatalf("retried advisor turn error = %v", got.err)
	}
	claudeSubWantOneWarning(t, got, claudeSubAdvisorReplacementWarning)
}

// TestClaudeSubCancelSummaryFailureKeepsNotice proves a summary whose helper
// startup fails reports the error, makes no replacement claim, and keeps the
// notice for the call that continues the conversation.
func TestClaudeSubCancelSummaryFailureKeepsNotice(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner, _ := claudeSubAbandonedParent(t,
		&claudeSubScript{batches: [][]claudeSubEvent{nil}},
		claudeSubTextTurns(t, 1),
	)
	spawner.setErr(errors.New("spawn refused"))
	summary := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    model,
		Messages:                 []Message{claudeSubUserText("first"), claudeSubUserText("summarize the older part")},
		CompactionSummary:        true,
		CompactionSourceMessages: 1,
	})
	if summary.err == nil || !strings.Contains(summary.err.Error(), "spawn refused") {
		t.Fatalf("summary failure = %v, want the spawn error", summary.err)
	}
	if warnings := summary.warnings(); len(warnings) != 0 {
		t.Errorf("summary warnings = %q, want none for a recovery that did not happen", warnings)
	}
	if !pool.replacementPending("parent") {
		t.Fatal("the failed summary dropped the parent replacement notice")
	}
	spawner.setErr(nil)
	next := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("first"), claudeSubUserText("second")},
	})
	if next.err != nil {
		t.Fatalf("next parent turn error = %v", next.err)
	}
	claudeSubWantOneWarning(t, next, claudeSubReplacedProcessWarning)
}
