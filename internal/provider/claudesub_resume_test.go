package provider

// Tests for recovering a parent claude_subscription turn a user stopped: reuse
// of the drained process while the stored checkpoint is one stopped delivery
// behind, and the resume that takes over when the live process cannot be used.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// claudeSubUserTextBlocks returns the text blocks of one CLI user line, in
// order, so a folded delivery can be checked block by block.
func claudeSubUserTextBlocks(t *testing.T, line string) []string {
	t.Helper()
	var envelope struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode user line %s: %v", line, err)
	}
	var out []string
	for _, block := range envelope.Message.Content {
		if block.Type == "text" {
			out = append(out, block.Text)
		}
	}
	return out
}

// claudeSubWantLedgerMatches asserts that the live record for key holds exactly
// msgs, message for message, so the digest chain a later checkpoint writes is
// reproducible from the request history.
func claudeSubWantLedgerMatches(t *testing.T, pool *ClaudeSubscriptionPool, key string, msgs []Message) {
	t.Helper()
	live := claudeSubLiveSession(pool, key)
	if live == nil {
		t.Fatalf("no live session for %q", key)
	}
	entries := live.sync.entries
	if len(entries) != len(msgs) {
		t.Fatalf("record entries = %d, want the %d request messages", len(entries), len(msgs))
	}
	for i, m := range msgs {
		want := claudeSubEntryFromMessage(m)
		if entries[i].Role != want.Role || entries[i].Digest != want.Digest {
			t.Fatalf("entry %d = %s/%s, want %s/%s", i, entries[i].Role, entries[i].Digest, want.Role, want.Digest)
		}
	}
}

// claudeSubStopParentTurn runs one parent request and cancels it mid-stream.
func claudeSubStopParentTurn(t *testing.T, pool *ClaudeSubscriptionPool, model string, state json.RawMessage, msgs []Message) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     state,
		Messages:         msgs,
	}, func(chunk ChatChunk) error {
		if chunk.Delta.Content != "" {
			cancel()
		}
		return nil
	})
}

// TestClaudeSubInterruptedTail pins the request suffix a stopped turn leaves.
func TestClaudeSubInterruptedTail(t *testing.T) {
	user := claudeSubSyncUserMsg
	partial := func(content string) Message {
		return Message{Role: MessageRoleAssistant, Content: content, IncompleteText: content}
	}
	tests := []struct {
		name   string
		msgs   []Message
		n      int
		want   int
		wantOK bool
	}{
		{name: "stopped user and new user", msgs: []Message{user("a"), user("b")}, n: 0, want: -1, wantOK: true},
		{name: "stopped user, partial and new user", msgs: []Message{user("a"), partial("half"), user("b")}, n: 0, want: 1, wantOK: true},
		{name: "partial only", msgs: []Message{partial("half")}, n: 0, want: -1, wantOK: false},
		{name: "stopped user and partial without a new user", msgs: []Message{user("a"), partial("half")}, n: 0, want: -1, wantOK: false},
		{name: "one user only", msgs: []Message{user("a")}, n: 0, want: -1, wantOK: true},
		{name: "empty suffix", msgs: []Message{user("a")}, n: 1, want: -1, wantOK: false},
		{name: "tail after a checkpoint", msgs: []Message{user("a"), user("b"), user("c"), partial("half"), user("d")}, n: 2, want: 3, wantOK: true},
		{name: "assistant without the partial marker", msgs: []Message{user("a"), {Role: MessageRoleAssistant, Content: "answer"}, user("b")}, n: 0, want: -1, wantOK: false},
		{name: "partial with tool calls", msgs: []Message{user("a"), {Role: MessageRoleAssistant, IncompleteText: "half", ToolCalls: []ToolCall{{ID: "c1"}}}, user("b")}, n: 0, want: -1, wantOK: false},
		{name: "tool in the suffix", msgs: []Message{user("a"), claudeSubSyncToolMsg("c1", "body")}, n: 0, want: -1, wantOK: false},
		{name: "start past the history", msgs: []Message{user("a")}, n: 2, want: -1, wantOK: false},
		{name: "negative start", msgs: []Message{user("a")}, n: -1, want: -1, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := claudeSubInterruptedTail(tc.msgs, tc.n)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("claudeSubInterruptedTail(n=%d) = %d, %v; want %d, %v", tc.n, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestClaudeSubSyncDrainedMatches pins the acceptance rule for reusing a drained
// process whose stored checkpoint is one stopped delivery behind.
func TestClaudeSubSyncDrainedMatches(t *testing.T) {
	// base builds a two-entry record with the identity a checkpoint needs.
	base := func(t *testing.T) *claudeSubSync {
		t.Helper()
		s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
		s.commitAssistant(claudeSubSyncAssistantMsg("an answer"), "u-1")
		s.fingerprint = "fp"
		s.cliSessionID = "sess-1"
		return s
	}
	checkpointOf := func(t *testing.T, s *claudeSubSync) claudeSubBackendState {
		t.Helper()
		st, ok, err := s.checkpoint("parent")
		if err != nil || !ok {
			t.Fatalf("checkpoint = %+v, %v, %v", st, ok, err)
		}
		return st
	}
	// drained builds a proven stop that appended entries after the base record.
	drained := func(t *testing.T, appendEntries func(*claudeSubSync), start int) (*claudeSubSync, claudeSubBackendState) {
		t.Helper()
		s := base(t)
		stored := checkpointOf(t, s)
		appendEntries(s)
		s.markDrained(start)
		return s, stored
	}

	t.Run("one user tail is one stopped delivery", func(t *testing.T) {
		s, stored := drained(t, func(s *claudeSubSync) {
			s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
		}, 2)
		if !s.drainedMatches(stored, "parent") {
			t.Fatal("a proven stop with a user tail did not match its stored checkpoint")
		}
	})
	t.Run("user and tool tail is one stopped delivery", func(t *testing.T) {
		s, stored := drained(t, func(s *claudeSubSync) {
			s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
			s.commitToolResult(claudeSubSyncToolMsg("c1", "body"))
		}, 2)
		if !s.drainedMatches(stored, "parent") {
			t.Fatal("a proven stop with a user and tool tail did not match its stored checkpoint")
		}
	})
	t.Run("empty tail is not a stopped delivery", func(t *testing.T) {
		s, stored := drained(t, func(*claudeSubSync) {}, 2)
		if s.drainedMatches(stored, "parent") {
			t.Fatal("a record with nothing after the checkpoint matched")
		}
	})
	t.Run("base behind the stored checkpoint", func(t *testing.T) {
		s := base(t)
		stored := checkpointOf(t, s)
		s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("one")}})
		s.commitAssistant(claudeSubSyncAssistantMsg("first answer"), "u-2")
		s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("two")}})
		s.markDrained(4)
		if s.drainedMatches(stored, "parent") {
			t.Fatal("a record two deliveries ahead of the checkpoint matched")
		}
	})
	t.Run("tainted record", func(t *testing.T) {
		s, stored := drained(t, func(s *claudeSubSync) {
			s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
		}, 2)
		s.tainted = true
		if s.drainedMatches(stored, "parent") {
			t.Fatal("a tainted record matched")
		}
	})
	t.Run("drained state cleared", func(t *testing.T) {
		s, stored := drained(t, func(s *claudeSubSync) {
			s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
		}, 2)
		s.drained = false
		if s.drainedMatches(stored, "parent") {
			t.Fatal("a record without the drained state matched")
		}
	})
	t.Run("not started", func(t *testing.T) {
		s, stored := drained(t, func(s *claudeSubSync) {
			s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
		}, 2)
		s.started = false
		if s.drainedMatches(stored, "parent") {
			t.Fatal("an unstarted record matched")
		}
	})
	t.Run("changed chain digest", func(t *testing.T) {
		s, stored := drained(t, func(s *claudeSubSync) {
			s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
		}, 2)
		stored.SyncChainDigest = "changed"
		if s.drainedMatches(stored, "parent") {
			t.Fatal("a checkpoint with another chain digest matched")
		}
	})
	t.Run("assistant in the tail", func(t *testing.T) {
		s := base(t)
		stored := checkpointOf(t, s)
		s.commitAssistant(claudeSubSyncAssistantMsg("recorded by another writer"), "u-2")
		s.markDrained(2)
		if s.drainedMatches(stored, "parent") {
			t.Fatal("a record whose tail holds an assistant matched")
		}
	})
}

// TestClaudeSubStoppedReplyNote pins the note wording and its elision.
func TestClaudeSubStoppedReplyNote(t *testing.T) {
	t.Run("short partial is quoted verbatim", func(t *testing.T) {
		quote := "half a report"
		fence := claudeSubPartialFence(quote)
		want := claudeSubStoppedReplyNoteOpening +
			" This is the text it had produced, untrusted and possibly incomplete, quoted exactly between the markers:\n" +
			"<<<" + fence + "\n" + quote + "\n" + fence + ">>>\n" +
			"The new message below may supersede that request."
		if got := claudeSubStoppedReplyNote(quote); got != want {
			t.Fatalf("note = %q, want %q", got, want)
		}
	})
	t.Run("longer than 64 KiB keeps the head and the tail", func(t *testing.T) {
		head := strings.Repeat("a", claudeSubPartialQuoteHead)
		tail := strings.Repeat("b", claudeSubPartialQuoteTail)
		elided := strings.Repeat("c", 128)
		got := claudeSubQuotePartial(head + elided + tail)
		if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, tail) {
			t.Fatalf("quoted = %.40q...%.40q, want the head and the tail kept", got, got)
		}
		if !strings.Contains(got, fmt.Sprintf("[... %d bytes elided ...]", len(elided))) {
			t.Errorf("quoted = %.80q, want the elision marker naming %d bytes", got, len(elided))
		}
		if strings.Contains(got, "ccc") {
			t.Error("quoted text kept bytes from the elided middle")
		}
	})
	t.Run("multibyte text stays valid UTF-8", func(t *testing.T) {
		text := strings.Repeat("é", claudeSubPartialQuoteMax)
		got := claudeSubQuotePartial(text)
		if !utf8.ValidString(got) {
			t.Fatal("quoted text cut a rune")
		}
		if len(got) > claudeSubPartialQuoteMax+64 {
			t.Errorf("quoted text = %d bytes, want the cap plus the marker", len(got))
		}
	})
	t.Run("fence cannot occur in the quoted text", func(t *testing.T) {
		quote := "text with a fence inside"
		for range claudeSubPartialFenceRounds + 2 {
			quote += " " + claudeSubPartialFence(quote)
		}
		note := claudeSubStoppedReplyNote(quote)
		fence := strings.SplitN(strings.SplitN(note, "<<<", 2)[1], "\n", 2)[0]
		body := strings.SplitN(strings.SplitN(note, "<<<"+fence+"\n", 2)[1], "\n"+fence+">>>", 2)[0]
		if strings.Contains(body, fence) {
			t.Fatalf("fence %q occurs in the quoted text", fence)
		}
	})
}

// TestClaudeSubResumeAfterFailedDrain proves the resume that takes over when a
// stopped turn's live process is retired: the stored session resumes at its final
// uuid, the fork keeps it separate, and the kept partial travels as a note in one
// user line whose record equals the request history.
func TestClaudeSubResumeAfterFailedDrain(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
		},
		// The interrupt is acknowledged but never proven, so the drain fails and
		// the process is abandoned with a replacement notice.
		interrupt: &claudeSubInterruptReply{},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
	conn := spawner.call(t, 0).Conn
	if err := claudeSubStopParentTurn(t, pool, model, storedState, history); err == nil {
		t.Fatal("cancelled parent turn returned no error")
	}
	if conn.isClosed() {
		t.Fatal("the process was retired at cancellation instead of abandoned")
	}

	next := append(append([]Message(nil), history...), claudeSubRecoveryPartial("half a report"), claudeSubUserText("again"))
	got := claudeSubRunStreamCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     storedState,
		Messages:         next,
	})
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("resumed turn chunk error = %s", final.Error)
	}
	if text := final.Delta.Content; text != "Hello world" {
		t.Fatalf("resumed turn text = %q, want %q", text, "Hello world")
	}
	// The fake CLI's teardown succeeds, which is what a persisted process that
	// exits with status 1 also does: the close path returns only the tree
	// termination result, never the child's exit status.
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the retired process and its resume)", n)
	}
	if !conn.isClosed() {
		t.Error("the abandoned process was reused instead of retired")
	}
	serving := spawner.call(t, 1)
	resume, _ := claudeSubArgValue(serving.Args, "--resume")
	at, _ := claudeSubArgValue(serving.Args, "--resume-session-at")
	state := claudeSubDecodeState(t, storedState)
	if resume != state.CLISessionID || at != state.FinalUUID {
		t.Errorf("resume = %q at %q, want %q at %q", resume, at, state.CLISessionID, state.FinalUUID)
	}
	if !claudeSubHasArg(serving.Args, "--fork-session") {
		t.Errorf("args %q, want --fork-session for a session a live process was writing", serving.Args)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 {
		t.Fatalf("user lines = %q, want one folded line", lines)
	}
	if strings.Contains(lines[0], "Conversation so far") {
		t.Errorf("user line = %s, want a resume, not a flattened transcript", lines[0])
	}
	blocks := claudeSubUserTextBlocks(t, lines[0])
	if len(blocks) != 3 {
		t.Fatalf("user blocks = %d, want the stopped content, the note and the new message", len(blocks))
	}
	if blocks[0] != "more" || blocks[2] != "again" {
		t.Errorf("user blocks = %q, want [more, note, again]", blocks)
	}
	if !strings.HasPrefix(blocks[1], claudeSubStoppedReplyNoteOpening) || !strings.Contains(blocks[1], "half a report") {
		t.Errorf("note block = %q, want the stopped reply quoted", blocks[1])
	}
	claudeSubWantOneExactWarning(t, got, claudeSubReplacedProcessWarning)
	if recovery := claudeSubRecoveryLabel(final); recovery != claudeSubRecoveryResumeAfterStop {
		t.Errorf("recovery = %q, want %q", recovery, claudeSubRecoveryResumeAfterStop)
	}

	// The record equals the request history, so its Done checkpoint resumes the
	// new tail again.
	historyAfter := append(append([]Message(nil), next...), final.Delta)
	claudeSubWantLedgerMatches(t, pool, "parent", historyAfter)
	restarted, restartedSpawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	restart := claudeSubRunStreamCollected(t, restarted, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     final.BackendState,
		Messages:         append(append([]Message(nil), historyAfter...), claudeSubUserText("onwards")),
	})
	if text := restart.final(t).Delta.Content; text != "Hello world" {
		t.Fatalf("second restart text = %q, want %q", text, "Hello world")
	}
	if n := restartedSpawner.callCount(); n != 1 {
		t.Fatalf("second restart spawn count = %d, want 1", n)
	}
	args := restartedSpawner.call(t, 0).Args
	if resume, _ := claudeSubArgValue(args, "--resume"); resume == "" {
		t.Errorf("second restart args %q, want a resume", args)
	}
	if !claudeSubHasArg(args, "--fork-session") {
		t.Errorf("second restart args %q, want --fork-session for every resume", args)
	}
	restartLines := claudeSubUserLines(restartedSpawner.call(t, 0).Conn)
	if len(restartLines) != 1 || !strings.Contains(restartLines[0], "onwards") {
		t.Errorf("second restart user lines = %q, want the plain new message", restartLines)
	}
	if blocks := claudeSubUserTextBlocks(t, restartLines[0]); len(blocks) != 1 {
		t.Errorf("second restart blocks = %d, want only the new message: %q", len(blocks), blocks)
	}
}

// TestClaudeSubDrainedParentEditedStoppedUserResumes proves an edited stopped
// user line takes the resume path instead of reusing the drained process.
func TestClaudeSubDrainedParentEditedStoppedUserResumes(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("write the report")}
	conn := spawner.call(t, 0).Conn
	if err := claudeSubStopParentTurn(t, pool, model, storedState, history); err == nil {
		t.Fatal("cancelled parent turn returned no error")
	}
	if conn.isClosed() {
		t.Fatal("the process was retired at cancellation instead of drained")
	}

	next := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("write a different report"), claudeSubRecoveryPartial("half a report"), claudeSubUserText("again")}
	got := claudeSubRunStreamCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     storedState,
		Messages:         next,
	})
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("resumed turn chunk error = %s", final.Error)
	}
	if text := final.Delta.Content; text != "Hello world" {
		t.Fatalf("resumed turn text = %q, want %q", text, "Hello world")
	}
	if !conn.isClosed() {
		t.Error("the drained process took an edited stopped user line")
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the drained process replaced by its resume)", n)
	}
	serving := spawner.call(t, 1)
	if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume == "" {
		t.Errorf("args %q, want a resume", serving.Args)
	}
	if !claudeSubHasArg(serving.Args, "--fork-session") {
		t.Errorf("args %q, want --fork-session", serving.Args)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 {
		t.Fatalf("user lines = %q, want one folded line", lines)
	}
	blocks := claudeSubUserTextBlocks(t, lines[0])
	if len(blocks) != 3 || blocks[0] != "write a different report" || blocks[2] != "again" {
		t.Fatalf("user blocks = %q, want [edited stopped user, note, new user]", blocks)
	}
	if warnings := got.warnings(); len(warnings) != 1 {
		t.Errorf("warnings = %q, want the replacement the discarded live process reports", warnings)
	}
	if recovery := claudeSubRecoveryLabel(final); recovery != claudeSubRecoveryResumeAfterStop {
		t.Errorf("recovery = %q, want %q", recovery, claudeSubRecoveryResumeAfterStop)
	}
}

// TestClaudeSubDrainedParentReusesProcessInToolLoop proves a parent stop while
// the model streams after a tool result reuses the same process, whose record
// ends with that tool result.
func TestClaudeSubDrainedParentReusesProcessInToolLoop(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc)
	first := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	if len(first.Delta.ToolCalls) != 1 {
		t.Fatalf("first turn tool calls = %+v, want one", first.Delta.ToolCalls)
	}
	storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
	result := Message{Role: MessageRoleTool, ToolCallID: first.Delta.ToolCalls[0].ID, Content: "notes body"}
	history := []Message{claudeSubUserText("read notes"), first.Delta, result}
	conn := spawner.call(t, 0).Conn
	// The tool result reaches the CLI through the MCP host, so the events it
	// streams next are queued directly.
	for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl")[:8] {
		conn.push(ev)
	}
	if err := claudeSubStopParentTurn(t, pool, model, storedState, history); err == nil {
		t.Fatal("cancelled parent turn returned no error")
	}
	if conn.isClosed() {
		t.Fatal("the process was retired at cancellation instead of drained")
	}

	next := append(append([]Message(nil), history...), claudeSubRecoveryPartial("half a report"), claudeSubUserText("keep going"))
	got := claudeSubRunStreamCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
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
	if warnings := got.warnings(); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	if recovery := claudeSubRecoveryLabel(final); recovery != claudeSubRecoveryDrainedReuse {
		t.Errorf("recovery = %q, want %q", recovery, claudeSubRecoveryDrainedReuse)
	}
	lines := claudeSubUserLines(conn)
	if len(lines) != 2 || !strings.Contains(lines[1], "keep going") {
		t.Fatalf("user lines = %q, want the first turn plus the new message", lines)
	}
	claudeSubWantLedgerMatches(t, pool, "parent", append(append([]Message(nil), next...), final.Delta))
}

// TestClaudeSubResumeFallbackOnUnconfirmedExit proves a teardown that misses its
// deadline never resumes the stored session: the request reseeds with a new one.
func TestClaudeSubResumeFallbackOnUnconfirmedExit(t *testing.T) {
	model := claudeSubTestSpec().Model
	closeTimeout := claudeSubSessionCloseTimeout
	claudeSubSessionCloseTimeout = 50 * time.Millisecond
	t.Cleanup(func() { claudeSubSessionCloseTimeout = closeTimeout })

	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
		},
		interrupt: &claudeSubInterruptReply{},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	// The session cleanup hangs, so its teardown never confirms the CLI is gone.
	release := make(chan struct{})
	pool.removeAll = func(dir string) error {
		<-release
		return os.RemoveAll(dir)
	}
	t.Cleanup(func() { close(release) })

	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
	if err := claudeSubStopParentTurn(t, pool, model, storedState, history); err == nil {
		t.Fatal("cancelled parent turn returned no error")
	}

	next := append(append([]Message(nil), history...), claudeSubRecoveryPartial("half a report"), claudeSubUserText("again"))
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
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the unconfirmed process replaced)", n)
	}
	serving := spawner.call(t, 1)
	if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume != "" {
		t.Errorf("replacement --resume = %q, want a new session", resume)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("replacement user lines = %q, want one flattened transcript", lines)
	}
	if !strings.Contains(lines[0], "half a report") {
		t.Errorf("transcript = %s, want the kept partial", lines[0])
	}
	if reason := claudeSubRecoveryReseedReason(final); reason != claudeSubReseedUnconfirmed {
		t.Errorf("reseed reason = %q, want %q", reason, claudeSubReseedUnconfirmed)
	}
	claudeSubWantOneExactWarning(t, got, claudeSubReplacedAndFlattenedWarning)
}

// TestClaudeSubTwoStopsInARowReseed proves the second stop after a reuse cannot
// reuse the process again: its record is two deliveries ahead of the stored
// checkpoint, which no proven stop describes.
func TestClaudeSubTwoStopsInARowReseed(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
		},
		interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("write the report")}
	if err := claudeSubStopParentTurn(t, pool, model, storedState, history); err == nil {
		t.Fatal("first cancelled turn returned no error")
	}

	second := append(append([]Message(nil), history...), claudeSubRecoveryPartial("half a report"), claudeSubUserText("again"))
	if err := claudeSubStopParentTurn(t, pool, model, storedState, second); err == nil {
		t.Fatal("second cancelled turn returned no error")
	}

	third := append(append([]Message(nil), second...), claudeSubRecoveryPartial("another half"), claudeSubUserText("once more"))
	got := claudeSubRunStreamCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     storedState,
		Messages:         third,
	})
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("third turn chunk error = %s", final.Error)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (two stops reseed on a fresh process)", n)
	}
	serving := spawner.call(t, 1)
	if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume != "" {
		t.Errorf("replacement --resume = %q, want none for two stopped deliveries", resume)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("replacement user lines = %q, want one flattened transcript", lines)
	}
	if reason := claudeSubRecoveryReseedReason(final); reason != claudeSubReseedCheckpoint {
		t.Errorf("reseed reason = %q, want %q: the live checkpoint is two deliveries behind", reason, claudeSubReseedCheckpoint)
	}
	claudeSubWantOneExactWarning(t, got, claudeSubFlattenedReseedWarning)
}

// TestClaudeSubRestoreRefusesToolSuffix pins the tool-suffix finding: a stored
// tool result cannot be delivered on a resumed process, which registers no
// pending call for it, so the restore refuses and the request reseeds.
func TestClaudeSubRestoreRefusesToolSuffix(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	firstPool, _ := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:8]}})
	first := claudeSubRunTurn(t, firstPool, ChatRequest{
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

	// Restart: a new pool has no live process, and the suffix holds a stored tool
	// result.
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	msgs := []Message{claudeSubUserText("read notes"), first.Delta, result, claudeSubUserText("more")}
	final := claudeSubRecoveryRunStream(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         msgs,
	})
	if final.Error != "" {
		t.Fatalf("turn error = %s", final.Error)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1", n)
	}
	serving := spawner.call(t, 0)
	if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume != "" {
		t.Errorf("--resume = %q, want a reseed: a resumed process holds no pending call", resume)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") || !strings.Contains(lines[0], "notes body") {
		t.Fatalf("user lines = %q, want one transcript carrying the tool result", lines)
	}
	if reason := claudeSubRecoveryReseedReason(final); reason != claudeSubReseedPlan {
		t.Errorf("reseed reason = %q, want %q", reason, claudeSubReseedPlan)
	}
}

// TestClaudeSubSyncDrainedMatchesOwner proves a stored state owned by another
// key never matches on the drained path.
func TestClaudeSubSyncDrainedMatchesOwner(t *testing.T) {
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
	s.commitAssistant(claudeSubSyncAssistantMsg("an answer"), "u-1")
	s.fingerprint = "fp"
	s.cliSessionID = "sess-1"
	stored, ok, err := s.checkpoint("parent")
	if err != nil || !ok {
		t.Fatalf("checkpoint = %+v, %v, %v", stored, ok, err)
	}
	s.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("stopped")}})
	s.markDrained(2)
	if !s.drainedMatches(stored, "parent") {
		t.Fatal("a proven stop did not match its own stored checkpoint")
	}
	if s.drainedMatches(stored, "other") {
		t.Fatal("a stored state owned by another key matched")
	}
}

// TestClaudeSubRestoreRefusesDanglingToolCalls proves a stored final assistant
// whose tool calls the request never answers cannot resume: the successor user
// line would follow an unanswered call.
func TestClaudeSubRestoreRefusesDanglingToolCalls(t *testing.T) {
	history := []Message{claudeSubSyncUserMsg("read notes"), claudeSubSyncAssistantMsg("looking", "c1"), claudeSubSyncUserMsg("more")}
	entries := []claudeSubEntry{claudeSubEntryFromMessage(history[0]), claudeSubEntryFromMessage(history[1])}
	entries[1].UUID = "u-final"
	prefix, err := claudeSubPrefixDigest(entries)
	if err != nil {
		t.Fatal(err)
	}
	stored := claudeSubBackendState{
		Provider:               "claude_subscription",
		Version:                claudeSubBackendStateVersion,
		CLISessionID:           "sess-1",
		TransportSession:       "parent",
		SyncEntries:            2,
		FinalMessageDigest:     entries[1].Digest,
		FinalUUID:              "u-final",
		SyncPrefixDigest:       prefix,
		PromptToolsFingerprint: "fp",
	}
	_, ok, reason, err := claudeSubRestoreFor(history, stored, true, "parent", "fp")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if ok || reason != claudeSubReseedPlan {
		t.Fatalf("restorable = %v, reason = %q; want a reseed reported as %q", ok, reason, claudeSubReseedPlan)
	}
}

// TestClaudeSubUnconfirmedRetireReseedsNextCall proves an unconfirmed retire of
// a process a stopped turn abandoned records the key, so the next call reseeds
// with a new session instead of resuming the stored one, and spawns once.
func TestClaudeSubUnconfirmedRetireReseedsNextCall(t *testing.T) {
	model := claudeSubTestSpec().Model
	sc := &claudeSubScript{
		batches: [][]claudeSubEvent{
			claudeSubDecodeEvents(t, "turn_text.jsonl"),
			claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
		},
		// The interrupt is acknowledged but never proven, so the drain fails and
		// the process is abandoned with a replacement notice.
		interrupt: &claudeSubInterruptReply{},
	}
	pool, spawner := claudeSubPoolWithScripts(t, sc, claudeSubTextTurns(t, 1))
	// The harness cannot stall a fake teardown deterministically, so the
	// unconfirmed outcome is injected at the retireConfirmed seam.
	pool.confirmRetire = func(*claudeSubSession) bool { return false }

	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	storedState := claudeSubEncodeState(t, claudeSubDecodeState(t, first.BackendState))
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
	if err := claudeSubStopParentTurn(t, pool, model, storedState, history); err == nil {
		t.Fatal("cancelled parent turn returned no error")
	}

	got := claudeSubRunStreamCollected(t, pool, ChatRequest{TransportSession: "parent", Model: model, BackendState: storedState, Messages: history})
	final := got.final(t)
	if final.Error != "" {
		t.Fatalf("next turn chunk error = %s", final.Error)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the abandoned process and one reseed)", n)
	}
	serving := spawner.call(t, 1)
	if resume, _ := claudeSubArgValue(serving.Args, "--resume"); resume != "" {
		t.Errorf("reseed spawn --resume = %q, want a new session", resume)
	}
	if !strings.Contains(claudeSubUserLines(serving.Conn)[0], "Conversation so far") {
		t.Errorf("reseed user lines = %q, want the flattened transcript", claudeSubUserLines(serving.Conn))
	}
	if reason := claudeSubRecoveryReseedReason(final); reason != claudeSubReseedUnconfirmed {
		t.Errorf("reseed reason = %q, want %q", reason, claudeSubReseedUnconfirmed)
	}
	if pool.reseedPending("parent") {
		t.Error("the reseed record survived the spawn it drove")
	}
}

// TestClaudeSubStoppedTurnRecoverySkipsSubAgents proves the recovery labels stay
// with parent calls: a sub-agent and an advisor reuse their drained process with
// no recovery recorded and no stored state to resume.
func TestClaudeSubStoppedTurnRecoverySkipsSubAgents(t *testing.T) {
	model := claudeSubTestSpec().Model
	t.Run("sub-agent", func(t *testing.T) {
		sc := &claudeSubScript{
			batches: [][]claudeSubEvent{
				claudeSubDecodeEvents(t, "turn_text.jsonl")[:8],
				claudeSubDecodeEvents(t, "turn_text.jsonl"),
			},
			interrupt: &claudeSubInterruptReply{events: claudeSubDecodeEvents(t, "interrupt_text.jsonl")},
		}
		pool, spawner := claudeSubPoolWithScripts(t, sc)
		if err := claudeSubCancelOnText(t, pool, claudeSubDrainRequest("child-1", model, claudeSubUserText(claudeSubDrainFirstUser))); err == nil {
			t.Fatal("cancelled sub-agent turn returned no error")
		}
		got := claudeSubDrainFollowUp(t, pool, "child-1", model, "half a report")
		claudeSubWantDrainedReuse(t, spawner, got)
		if recovery := claudeSubRecoveryLabel(got.final(t)); recovery != "" {
			t.Errorf("sub-agent recovery = %q, want none", recovery)
		}
	})
	t.Run("advisor", func(t *testing.T) {
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
		second := append(append([]Message(nil), first...), claudeSubUserText("now check this"), claudeSubUserText("is it right again?"))
		got := claudeSubRunStreamCollected(t, pool, advisor(second...))
		if text := got.final(t).Delta.Content; text != "Hello world" {
			t.Fatalf("second advisor text = %q, want %q", text, "Hello world")
		}
		if n := spawner.callCount(); n != 1 {
			t.Fatalf("spawn count = %d, want 1 (the drained advisor reused)", n)
		}
		if recovery := claudeSubRecoveryLabel(got.final(t)); recovery != "" {
			t.Errorf("advisor recovery = %q, want none", recovery)
		}
	})
}
