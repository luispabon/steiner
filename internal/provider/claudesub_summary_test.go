package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// claudeSubTwoTurnParent runs two text turns on the parent "parent" and returns
// the history they built (user, assistant, user) and the final chunk of each turn.
func claudeSubTwoTurnParent(t *testing.T, pool *ClaudeSubscriptionPool) (u1, a1, u2 Message, first, second ChatChunk) {
	t.Helper()
	model := claudeSubTestSpec().Model
	u1 = claudeSubUserText("first question")
	first = claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{u1}})
	if first.Delta.Role != MessageRoleAssistant || first.BackendState == nil {
		t.Fatalf("first turn chunk = %+v, want assistant delta with backend state", first)
	}
	a1 = first.Delta
	u2 = claudeSubUserText("second question")
	second = claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{u1, a1, u2}, BackendState: first.BackendState})
	if second.Delta.Role != MessageRoleAssistant || second.BackendState == nil {
		t.Fatalf("second turn chunk = %+v, want assistant delta with backend state", second)
	}
	return u1, a1, u2, first, second
}

func TestClaudeSubArgsMaxTurns(t *testing.T) {
	tests := []struct {
		name     string
		maxTurns int
		want     string
		wantSet  bool
	}{
		{name: "unset", maxTurns: 0},
		{name: "one turn", maxTurns: 1, want: "1", wantSet: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := claudeSubArgs(claudeSubSpawnOptions{Model: "m", MaxTurns: tt.maxTurns})
			got, ok := claudeSubArgValue(args, "--max-turns")
			if ok != tt.wantSet || got != tt.want {
				t.Errorf("--max-turns = %q (present %v), want %q (present %v); args %v", got, ok, tt.want, tt.wantSet, args)
			}
		})
	}
}

// claudeSubTextTurnWithUUID returns the text turn fixture with its final
// assistant uuid set to uuid, so scripted turns can end at distinct CLI uuids.
func claudeSubTextTurnWithUUID(t *testing.T, uuid string) []claudeSubEvent {
	t.Helper()
	events := claudeSubDecodeEvents(t, "turn_text.jsonl")
	for i, ev := range events {
		if ev.Type != "assistant" {
			continue
		}
		events[i].UUID = uuid
		events[i].Raw = json.RawMessage(strings.Replace(string(ev.Raw), `"uuid":"u12"`, `"uuid":"`+uuid+`"`, 1))
		return events
	}
	t.Fatal("turn_text.jsonl has no assistant event")
	return nil
}

func TestClaudeSubSummaryForkResumesAtSourceTail(t *testing.T) {
	// Each parent turn ends at its own CLI uuid. The fork must cut at the source
	// tail (first turn), not at the record tip (second turn).
	parent := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubTextTurnWithUUID(t, "uuid-first"),
		claudeSubTextTurnWithUUID(t, "uuid-second"),
	}}
	pool, spawner := claudeSubPoolWithScripts(t, parent, claudeSubTextTurns(t, 1))
	u1, a1, u2, first, second := claudeSubTwoTurnParent(t, pool)
	firstUUID := claudeSubDecodeState(t, first.BackendState).FinalUUID
	secondState := claudeSubDecodeState(t, second.BackendState)
	if firstUUID != "uuid-first" || secondState.FinalUUID != "uuid-second" {
		t.Fatalf("turn final uuids = %q, %q, want uuid-first, uuid-second", firstUUID, secondState.FinalUUID)
	}
	// The source is [u1, a1]. The retained tail u2 and the summary instruction
	// follow it. The record also holds the second answer, which the request
	// does not cover.
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{u1, a1, u2, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if summary.BackendState != nil || summary.ClearBackendState {
		t.Errorf("fork summary chunk = state %s clear %v, want no state change", summary.BackendState, summary.ClearBackendState)
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and summary helper)", got)
	}
	helper := spawner.call(t, 1).Args
	want := map[string]string{
		"--resume":            secondState.CLISessionID,
		"--resume-session-at": firstUUID,
		"--max-turns":         "1",
	}
	for flag, value := range want {
		if got, ok := claudeSubArgValue(helper, flag); !ok || got != value {
			t.Errorf("helper %s = %q (present %v), want %q; args %v", flag, got, ok, value, helper)
		}
	}
	for _, flag := range []string{"--fork-session", "--no-session-persistence"} {
		if !claudeSubHasArg(helper, flag) {
			t.Errorf("helper args lack %s: %v", flag, helper)
		}
	}
	if lines := claudeSubUserLines(spawner.call(t, 1).Conn); len(lines) != 1 || !strings.Contains(lines[0], "second question") || !strings.Contains(lines[0], "summarize the older part") {
		t.Errorf("helper user lines = %q, want one line carrying the retained tail and the instruction", lines)
	}
	if n := len(claudeSubUserLines(spawner.call(t, 0).Conn)); n != 2 {
		t.Errorf("live parent received %d user lines, want only its 2 turns", n)
	}
	if spawner.call(t, 0).Conn.isClosed() {
		t.Error("fork closed the live parent process")
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if !live {
		t.Error("fork removed the live parent from the pool")
	}
}

func TestClaudeSubSummaryInPlaceWithoutSourceTailUUID(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 3))
	u1, _, _, _, second := claudeSubTwoTurnParent(t, pool)
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{u1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 1,
	})
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawned %d processes, want 1 (no helper without a source tail)", got)
	}
	lines := strings.Join(spawner.call(t, 0).Conn.sentLines(), "\n")
	if !strings.Contains(lines, "steiner compaction summary request") {
		t.Error("in-place summary was not sent marked to the live process")
	}
	if summary.BackendState != nil || !summary.ClearBackendState {
		t.Errorf("in-place summary chunk = state %s clear %v, want no state and clear", summary.BackendState, summary.ClearBackendState)
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if live {
		t.Error("in-place summary left the tainted parent process pooled")
	}
}

func TestClaudeSubSummaryColdReseedWithoutLiveProcess(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	msgs := []Message{claudeSubUserText("first question"), {Role: MessageRoleAssistant, Content: "first answer"}, claudeSubUserText("summarize")}
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 msgs,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawned %d processes, want 1 cold helper", got)
	}
	args := spawner.call(t, 0).Args
	if !claudeSubHasArg(args, "--no-session-persistence") {
		t.Errorf("cold helper persists its session: %v", args)
	}
	if v, ok := claudeSubArgValue(args, "--max-turns"); !ok || v != "1" {
		t.Errorf("cold helper --max-turns = %q (present %v), want 1", v, ok)
	}
	if _, ok := claudeSubArgValue(args, "--resume"); ok {
		t.Errorf("cold helper resumed a session: %v", args)
	}
	if !strings.Contains(strings.Join(claudeSubUserLines(spawner.call(t, 0).Conn), "\n"), "Conversation so far") {
		t.Error("cold helper was not reseeded with the transcript")
	}
	if summary.BackendState != nil || !summary.ClearBackendState {
		t.Errorf("cold summary chunk = state %s clear %v, want no state and clear of the generation's old state", summary.BackendState, summary.ClearBackendState)
	}
}

func TestClaudeSubSummarySubAgentAppendsMarkedRequestInPlace(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2))
	model := claudeSubTestSpec().Model
	u1 := claudeSubUserText("look")
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent-agent-1", ParentTransportSession: "parent", Model: model, Messages: []Message{u1}})
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent-agent-1",
		ParentTransportSession:   "parent",
		Model:                    model,
		Messages:                 []Message{u1, first.Delta, claudeSubUserText("summarize")},
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawned %d processes, want 1 (summary appends to the sub-agent process)", got)
	}
	args := spawner.call(t, 0).Args
	if !claudeSubHasArg(args, "--no-session-persistence") {
		t.Errorf("sub-agent process persists: %v", args)
	}
	if lines := strings.Join(spawner.call(t, 0).Conn.sentLines(), "\n"); !strings.Contains(lines, "steiner compaction summary request") {
		t.Error("sub-agent summary was not sent marked")
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent-agent-1"]
	pool.mu.Unlock()
	if live {
		t.Error("sub-agent summary left its tainted process pooled")
	}
	if summary.BackendState != nil || summary.ClearBackendState {
		t.Errorf("sub-agent summary chunk = state %s clear %v, want no state change", summary.BackendState, summary.ClearBackendState)
	}
}

// TestClaudeSubSummarySubAgentNoPlaceReseedsOnFreshProcess proves a sub-agent
// whose record cannot take the in-place summary retires its live process, and
// the summary is cold-reseeded on a fresh process that answers without state.
func TestClaudeSubSummarySubAgentNoPlaceReseedsOnFreshProcess(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1), claudeSubTextTurns(t, 1))
	model := claudeSubTestSpec().Model
	u1 := claudeSubUserText("look")
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent-agent-1", ParentTransportSession: "parent", Model: model, Messages: []Message{u1}})
	// The source opens with a message the record does not hold, so the
	// in-place append cannot take it.
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent-agent-1",
		ParentTransportSession:   "parent",
		Model:                    model,
		Messages:                 []Message{claudeSubUserText("not the first question"), first.Delta, claudeSubUserText("summarize")},
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (retired sub-agent and fresh reseed helper)", got)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("no-place summary left the live sub-agent process running")
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent-agent-1"]
	pool.mu.Unlock()
	if live {
		t.Error("no-place retirement left the sub-agent pooled")
	}
	helper := spawner.call(t, 1).Args
	if !claudeSubHasArg(helper, "--no-session-persistence") {
		t.Errorf("reseed helper persists its session: %v", helper)
	}
	if _, ok := claudeSubArgValue(helper, "--resume"); ok {
		t.Errorf("reseed helper resumed a session: %v", helper)
	}
	if lines := strings.Join(claudeSubUserLines(spawner.call(t, 1).Conn), "\n"); !strings.Contains(lines, "steiner compaction summary request") {
		t.Errorf("reseed helper was not sent the marked summary request: %s", lines)
	}
	if summary.Delta.Content != "Hello world" {
		t.Errorf("summary text = %q, want %q", summary.Delta.Content, "Hello world")
	}
	if summary.BackendState != nil || summary.ClearBackendState {
		t.Errorf("sub-agent reseed summary chunk = state %s clear %v, want no state change", summary.BackendState, summary.ClearBackendState)
	}
}

// claudeSubFailedResumeBatch is a CLI turn that fails before any output.
func claudeSubFailedResumeBatch() []claudeSubEvent {
	return []claudeSubEvent{{Type: "result", Raw: json.RawMessage(`{"subtype":"error_during_execution","is_error":true,"result":"resume failed"}`)}}
}

// TestClaudeSubSummaryForkFailureFallsBackInPlace proves a fork whose helper
// fails before output does not fail compaction: the live parent, which the fork
// never touched, takes the marked summary in place and is retired with its state
// cleared.
func TestClaudeSubSummaryForkFailureFallsBackInPlace(t *testing.T) {
	helper := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubFailedResumeBatch()}}
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 3), helper)
	_, a1, _, _, second := claudeSubTwoTurnParent(t, pool)
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{claudeSubUserText("first question"), a1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and the failed fork helper)", got)
	}
	if _, ok := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); !ok {
		t.Error("fork helper did not resume: the fallback was not exercised")
	}
	lines := strings.Join(spawner.call(t, 0).Conn.sentLines(), "\n")
	if !strings.Contains(lines, "steiner compaction summary request") {
		t.Error("fallback did not append the marked summary to the live parent")
	}
	if summary.BackendState != nil || !summary.ClearBackendState {
		t.Errorf("fallback summary chunk = state %s clear %v, want no state and clear", summary.BackendState, summary.ClearBackendState)
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if live {
		t.Error("fallback in-place left the tainted parent pooled")
	}
}

// TestClaudeSubSummaryNoPlaceReseedClearsParentState proves a live parent whose
// record cannot take the summary is retired, the summary is cold-reseeded on a
// helper, and the final chunk clears the old generation's backend state.
func TestClaudeSubSummaryNoPlaceReseedClearsParentState(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
	_, a1, _, _, second := claudeSubTwoTurnParent(t, pool)
	// The source opens with a message the record does not hold, so neither the
	// fork nor the in-place append can take it.
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{claudeSubUserText("not the first question"), a1, claudeSubUserText("summarize")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and cold helper)", got)
	}
	if _, ok := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); ok {
		t.Error("cold reseed resumed a session")
	}
	if n := len(claudeSubUserLines(spawner.call(t, 0).Conn)); n != 2 {
		t.Errorf("live parent received %d user lines, want only the 2 turns", n)
	}
	if !strings.Contains(strings.Join(claudeSubUserLines(spawner.call(t, 1).Conn), "\n"), "Conversation so far") {
		t.Error("cold helper was not reseeded with the transcript")
	}
	if summary.BackendState != nil || !summary.ClearBackendState {
		t.Errorf("reseed summary chunk = state %s clear %v, want no state and clear", summary.BackendState, summary.ClearBackendState)
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if live {
		t.Error("no-place retirement left the parent pooled")
	}
}

// TestClaudeSubSummaryInPlaceFailureRetiresParent proves a failed in-place
// summary still retires the live parent, and reports the failure as retired so
// the caller drops the generation's backend state. The original failure text
// and chain are preserved.
func TestClaudeSubSummaryInPlaceFailureRetiresParent(t *testing.T) {
	// An empty uuid models an assistant entry the CLI did not stamp, so the
	// summary cannot fork. The third batch fails the in-place summary.
	parent := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubTextTurnWithUUID(t, ""),
		claudeSubTextTurnWithUUID(t, ""),
		claudeSubFailedResumeBatch(),
	}}
	pool, spawner := claudeSubPoolWithScripts(t, parent)
	u1, a1, _, _, second := claudeSubTwoTurnParent(t, pool)
	err := claudeSubTurn(context.Background(), pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{u1, a1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	}, func(ChatChunk) error { return nil })
	if err == nil {
		t.Fatal("in-place summary failure returned no error")
	}
	if !BackendStateRetired(err) {
		t.Errorf("in-place failure error %v is not marked retired, want retired", err)
	}
	if inner := errors.Unwrap(err); inner == nil || inner.Error() != err.Error() {
		t.Errorf("marked error %q unwraps to %v, want the original failure text", err, inner)
	}
	var detail *claudeSubDetailError
	if !errors.As(err, &detail) {
		t.Errorf("marked error %v lost the original *claudeSubDetailError chain", err)
	}
	if !strings.Contains(err.Error(), "resume failed") {
		t.Errorf("marked error = %q, want the CLI failure text", err)
	}
	if got := spawner.callCount(); got != 1 {
		t.Errorf("spawned %d processes, want 1 (no helper for an in-place failure)", got)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("in-place failure left the parent process running")
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if live {
		t.Error("in-place failure left the parent pooled")
	}
}

// TestClaudeSubSummaryNoPlaceReseedFailureRetiresParent proves a parent retired
// by a no-place summary reports a failed reseed as retired, because its backend
// state is gone either way.
func TestClaudeSubSummaryNoPlaceReseedFailureRetiresParent(t *testing.T) {
	helper := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubFailedResumeBatch()}}
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), helper)
	_, a1, _, _, second := claudeSubTwoTurnParent(t, pool)
	// The source opens with a message the record does not hold, so neither the
	// fork nor the in-place append can take it.
	err := claudeSubTurn(context.Background(), pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{claudeSubUserText("not the first question"), a1, claudeSubUserText("summarize")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	}, func(ChatChunk) error { return nil })
	if err == nil {
		t.Fatal("no-place reseed failure returned no error")
	}
	if !BackendStateRetired(err) {
		t.Errorf("no-place reseed failure %v is not marked retired, want retired", err)
	}
	if !strings.Contains(err.Error(), "resume failed") {
		t.Errorf("marked error = %q, want the CLI failure text", err)
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and failed reseed helper)", got)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("no-place retirement left the parent process running")
	}
	if !spawner.call(t, 1).Conn.isClosed() {
		t.Error("failed reseed helper was not retired")
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	_, helperLive := pool.sessions["parent"+claudeSubSummarySuffix]
	pool.mu.Unlock()
	if live || helperLive {
		t.Errorf("no-place failure left processes pooled: parent %v, helper %v", live, helperLive)
	}
}

// TestClaudeSubSummaryColdReseedFailureNotRetired proves a summary answered by
// a cold helper with no live parent does not mark its failure retired: no
// parent backend state was touched.
func TestClaudeSubSummaryColdReseedFailureNotRetired(t *testing.T) {
	helper := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubFailedResumeBatch()}}
	pool, spawner := claudeSubPoolWithScripts(t, helper)
	msgs := []Message{claudeSubUserText("first question"), {Role: MessageRoleAssistant, Content: "first answer"}, claudeSubUserText("summarize")}
	err := claudeSubTurn(context.Background(), pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 msgs,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	}, func(ChatChunk) error { return nil })
	if err == nil {
		t.Fatal("cold reseed failure returned no error")
	}
	if BackendStateRetired(err) {
		t.Errorf("cold reseed failure %v is marked retired, want unmarked", err)
	}
	if !strings.Contains(err.Error(), "resume failed") {
		t.Errorf("cold reseed failure = %q, want the CLI failure text", err)
	}
	if got := spawner.callCount(); got != 1 {
		t.Errorf("spawned %d processes, want 1 cold helper", got)
	}
}

// TestBackendStateRetiredMarker proves the marker reports retired backend state
// through wrapping and preserves the original error for errors.Is.
func TestBackendStateRetiredMarker(t *testing.T) {
	sentinel := errors.New("claude_subscription turn failed: error_during_execution: resume failed")
	if BackendStateRetired(nil) {
		t.Error("nil error reported as retired")
	}
	if BackendStateRetired(sentinel) {
		t.Error("plain error reported as retired")
	}
	marked := markBackendStateRetired(fmt.Errorf("summary: %w", sentinel))
	if !BackendStateRetired(marked) {
		t.Error("marked error not reported as retired")
	}
	if got, want := marked.Error(), "summary: "+sentinel.Error(); got != want {
		t.Errorf("marked error text = %q, want %q", got, want)
	}
	if !errors.Is(marked, sentinel) {
		t.Error("marked error lost the original error identity")
	}
	if !BackendStateRetired(fmt.Errorf("outer: %w", marked)) {
		t.Error("marker not found through an outer wrap")
	}
	if got := markBackendStateRetired(nil); got != nil {
		t.Errorf("markBackendStateRetired(nil) = %v, want nil", got)
	}
}

// TestClaudeSubSummaryHelperNeverReusesPrimedRecord proves a summary helper that
// is already primed is retired rather than reused, so the reseed runs on a fresh
// process whose record starts empty.
func TestClaudeSubSummaryHelperNeverReusesPrimedRecord(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1), claudeSubTextTurns(t, 1))
	ctx := context.Background()
	stale, err := pool.acquire(ctx, "parent"+claudeSubSummarySuffix, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire stale helper: %v", err)
	}
	stale.primed = true
	pool.release(stale)

	var final ChatChunk
	err = claudeSubReseedSummary(ctx, pool, ChatRequest{Messages: []Message{claudeSubUserText("summarize")}}, "parent", claudeSubTestSpec(), func(chunk ChatChunk) error {
		if chunk.Done {
			final = chunk
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reseed summary: %v", err)
	}
	if !final.Done {
		t.Fatal("reseed summary emitted no Done chunk")
	}
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (retired stale helper and fresh helper)", got)
	}
	if n := len(claudeSubUserLines(spawner.call(t, 0).Conn)); n != 0 {
		t.Errorf("stale primed helper received %d user lines, want none", n)
	}
	if n := len(claudeSubUserLines(spawner.call(t, 1).Conn)); n == 0 {
		t.Error("fresh helper received no user lines")
	}
}

func TestClaudeSubSyncTaintBlocksReuseAndCheckpoint(t *testing.T) {
	u1 := claudeSubUserText("first")
	a1 := Message{Role: MessageRoleAssistant, Content: "answer"}
	u2 := claudeSubUserText("second")
	s := &claudeSubSync{started: true, interruptible: -1, fingerprint: "fp", cliSessionID: "sess"}
	s.entries = []claudeSubEntry{claudeSubEntryFromMessage(u1), claudeSubEntryWithUUID(a1, "uuid-a1")}
	req := ChatRequest{Messages: []Message{u1, a1, u2}}
	if _, err := s.plan(req); err != nil {
		t.Fatalf("plan before taint: %v", err)
	}
	s.taint()
	if _, err := s.plan(req); !errors.Is(err, errClaudeSubHistoryChanged) {
		t.Errorf("plan after taint = %v, want errClaudeSubHistoryChanged", err)
	}
	if _, ok, err := s.checkpoint("parent"); ok || err != nil {
		t.Errorf("checkpoint after taint ok=%v err=%v, want no state", ok, err)
	}
}

func claudeSubEntryWithUUID(m Message, uuid string) claudeSubEntry {
	e := claudeSubEntryFromMessage(m)
	e.UUID = uuid
	return e
}

func TestClaudeSubSourceTailUUID(t *testing.T) {
	u1 := claudeSubUserText("first")
	a1 := Message{Role: MessageRoleAssistant, Content: "answer"}
	u2 := claudeSubUserText("second")
	s := &claudeSubSync{started: true, entries: []claudeSubEntry{
		claudeSubEntryFromMessage(u1),
		claudeSubEntryWithUUID(a1, "uuid-a1"),
		claudeSubEntryFromMessage(u2),
	}}
	tests := []struct {
		name   string
		msgs   []Message
		source int
		want   string
		wantOK bool
	}{
		{name: "assistant tail with uuid", msgs: []Message{u1, a1, u2}, source: 2, want: "uuid-a1", wantOK: true},
		{name: "user tail has no uuid", msgs: []Message{u1, a1, u2}, source: 1},
		{name: "no source", msgs: []Message{u1, a1, u2}, source: 0},
		{name: "source beyond record", msgs: []Message{u1, a1, u2, a1}, source: 4},
		{name: "source does not match record", msgs: []Message{u1, claudeSubUserText("other")}, source: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := s.sourceTailUUID(tt.msgs, tt.source)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("sourceTailUUID() = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestClaudeSubPlanSummaryAppendsAfterRetainedTail(t *testing.T) {
	u1 := claudeSubUserText("first")
	a1 := Message{Role: MessageRoleAssistant, Content: "answer"}
	u2 := claudeSubUserText("second")
	a2 := Message{Role: MessageRoleAssistant, Content: "second answer"}
	s := &claudeSubSync{started: true, interruptible: -1, entries: []claudeSubEntry{
		claudeSubEntryFromMessage(u1),
		claudeSubEntryWithUUID(a1, "uuid-a1"),
		claudeSubEntryFromMessage(u2),
		claudeSubEntryWithUUID(a2, "uuid-a2"),
	}}
	instruction := claudeSubUserText("summarize")
	delta, err := s.planSummary([]Message{u1, a1, instruction}, 2)
	if err != nil {
		t.Fatalf("planSummary: %v", err)
	}
	if len(delta.User) != 1 || !strings.Contains(delta.User[0].Content, "2 older source messages") || !strings.Contains(delta.User[0].Content, "2 retained tail messages") || !strings.HasSuffix(delta.User[0].Content, "summarize") {
		t.Errorf("summary delta user = %+v, want marked instruction naming source and tail", delta.User)
	}
	if len(delta.keep) != len(s.entries) {
		t.Errorf("summary keeps %d entries, want the whole record of %d", len(delta.keep), len(s.entries))
	}

	if _, err := s.planSummary([]Message{u1, claudeSubUserText("other"), instruction}, 2); !errors.Is(err, errClaudeSubSummaryNoPlace) {
		t.Errorf("planSummary with diverged source = %v, want errClaudeSubSummaryNoPlace", err)
	}
}

func TestClaudeSubAcquireLiveWithoutSession(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquireLive(context.Background(), "absent")
	if err != nil || s != nil {
		t.Fatalf("acquireLive() = %v, %v; want nil, nil", s, err)
	}
	if got := spawner.callCount(); got != 0 {
		t.Errorf("acquireLive spawned %d processes, want none", got)
	}
}

// claudeSubHistoryLocator is the history note a compaction adopts into the
// system prompt next to the summary.
const claudeSubHistoryLocator = "[steiner:history] archived at /data/history.md"

// claudeSubTwoTurnsUnderPrompt runs two parent turns under the system message
// promptA. It returns the messages they built and the final chunk of each turn.
func claudeSubTwoTurnsUnderPrompt(t *testing.T, pool *ClaudeSubscriptionPool, promptA Message) (u1, a1, u2 Message, first, second ChatChunk) {
	t.Helper()
	model := claudeSubTestSpec().Model
	u1 = claudeSubUserText("first question")
	first = claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{promptA, u1}})
	if first.Delta.Role != MessageRoleAssistant || first.BackendState == nil {
		t.Fatalf("first turn chunk = %+v, want assistant delta with backend state", first)
	}
	a1 = first.Delta
	u2 = claudeSubUserText("second question")
	second = claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{promptA, u1, a1, u2}, BackendState: first.BackendState})
	if second.Delta.Role != MessageRoleAssistant || second.BackendState == nil {
		t.Fatalf("second turn chunk = %+v, want assistant delta with backend state", second)
	}
	return u1, a1, u2, first, second
}

// claudeSubRunTurnPrompt runs one request and returns its Done chunk with the
// system prompt file of the spawn at index. The file is read in the Done
// callback, before the session can be cleaned up.
func claudeSubRunTurnPrompt(t *testing.T, pool *ClaudeSubscriptionPool, spawner *claudeSubTestSpawner, index int, req ChatRequest) (ChatChunk, string) {
	t.Helper()
	var final ChatChunk
	var prompt string
	err := claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
		if !chunk.Done {
			return nil
		}
		final = chunk
		path, ok := claudeSubArgValue(spawner.call(t, index).Args, "--system-prompt-file")
		if !ok {
			return errors.New("spawn omitted --system-prompt-file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		prompt = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if !final.Done {
		t.Fatal("turn emitted no Done chunk")
	}
	return final, prompt
}

// TestClaudeSubSummaryForkLocatorReachesNextTurn proves a fork summary leaves
// the live parent untouched. The next normal request, under the adopted prompt
// with the history locator, starts a fresh process whose transport prompt
// carries the locator once. The old parent receives no new line.
func TestClaudeSubSummaryForkLocatorReachesNextTurn(t *testing.T) {
	parent := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubTextTurnWithUUID(t, "uuid-first"),
		claudeSubTextTurnWithUUID(t, "uuid-second"),
	}}
	pool, spawner := claudeSubPoolWithScripts(t, parent, claudeSubTextTurns(t, 1), claudeSubTextTurns(t, 1))
	model := claudeSubTestSpec().Model
	promptA := Message{Role: MessageRoleSystem, Content: claudeSubTestSpec().SystemPrompt}
	u1, a1, u2, first, second := claudeSubTwoTurnsUnderPrompt(t, pool, promptA)
	firstUUID := claudeSubDecodeState(t, first.BackendState).FinalUUID
	secondState := claudeSubDecodeState(t, second.BackendState)
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    model,
		Messages:                 []Message{promptA, u1, a1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})

	// Route: fork. A helper resumes the stored session at the source tail.
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and fork helper)", got)
	}
	helper := spawner.call(t, 1).Args
	for flag, want := range map[string]string{"--resume": secondState.CLISessionID, "--resume-session-at": firstUUID} {
		if got, ok := claudeSubArgValue(helper, flag); !ok || got != want {
			t.Errorf("fork helper %s = %q (present %v), want %q; args %v", flag, got, ok, want, helper)
		}
	}
	if !claudeSubHasArg(helper, "--fork-session") {
		t.Errorf("fork helper args lack --fork-session: %v", helper)
	}
	if lines := strings.Join(spawner.call(t, 1).Conn.sentLines(), "\n"); !strings.Contains(lines, "summarize the older part") || strings.Contains(lines, "second question") {
		t.Errorf("fork helper received %q, want the instruction and no retained suffix", lines)
	}
	if summary.BackendState != nil || summary.ClearBackendState || summary.Delta.Content == "" {
		t.Errorf("fork summary chunk = state %s clear %v text %q, want text and no state change", summary.BackendState, summary.ClearBackendState, summary.Delta.Content)
	}
	parentLines := len(claudeSubUserLines(spawner.call(t, 0).Conn))

	// The adopted summary and the history locator form prompt B. The retained
	// suffix is u2 and its answer, followed by the next user message.
	adopted := Message{Role: MessageRoleSystem, Content: summary.Delta.Content + "\n\n" + claudeSubHistoryLocator}
	nextMsgs := []Message{promptA, adopted, u2, second.Delta, claudeSubUserText("third question")}
	next, prompt := claudeSubRunTurnPrompt(t, pool, spawner, 2, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         nextMsgs,
	})

	if got := spawner.callCount(); got != 3 {
		t.Fatalf("spawned %d processes, want 3 (parent, fork helper, fresh process)", got)
	}
	if _, ok := claudeSubArgValue(spawner.call(t, 2).Args, "--resume"); ok {
		t.Error("fresh process resumed a session; the request carries no backend state")
	}
	if n := strings.Count(prompt, claudeSubHistoryLocator); n != 1 {
		t.Errorf("transport system prompt carries the history locator %d times, want 1: %q", n, prompt)
	}
	if !strings.Contains(prompt, summary.Delta.Content) {
		t.Errorf("transport system prompt lacks the new summary %q: %q", summary.Delta.Content, prompt)
	}
	if next.BackendState == nil {
		t.Error("next turn returned no backend state for the new generation")
	}
	wantFP, err := claudeSubFingerprint(claudeSubSystemPrompt(nextMsgs), nil)
	if err != nil {
		t.Fatalf("fingerprint adopted prompt: %v", err)
	}
	if got := claudeSubDecodeState(t, next.BackendState).PromptToolsFingerprint; got != wantFP {
		t.Errorf("new generation fingerprint = %q, want the adopted prompt's %q", got, wantFP)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("old parent process was not closed by the next turn")
	}
	reseed := strings.Join(claudeSubUserLines(spawner.call(t, 2).Conn), "\n")
	if !strings.Contains(reseed, "Conversation so far") || !strings.Contains(reseed, "second question") || !strings.Contains(reseed, "third question") || strings.Contains(reseed, "first question") {
		t.Errorf("fresh process transcript = %q, want the retained suffix without the summarized source", reseed)
	}
	if got := len(claudeSubUserLines(spawner.call(t, 0).Conn)); got != parentLines {
		t.Errorf("old parent received %d new user lines, want none", got-parentLines)
	}
}

// TestClaudeSubSummaryInPlaceLocatorReachesNextTurn proves an in-place summary
// retires the live parent. The next normal request, under the adopted prompt
// with the history locator, reseeds a fresh process whose transport prompt
// carries the locator once. The old parent receives no new line.
func TestClaudeSubSummaryInPlaceLocatorReachesNextTurn(t *testing.T) {
	// An empty uuid models an assistant entry the CLI did not stamp, so a source
	// ending on it cannot fork.
	parent := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubTextTurnWithUUID(t, ""),
		claudeSubTextTurnWithUUID(t, ""),
		claudeSubTextTurnWithUUID(t, ""),
	}}
	pool, spawner := claudeSubPoolWithScripts(t, parent, claudeSubTextTurns(t, 1))
	model := claudeSubTestSpec().Model
	promptA := Message{Role: MessageRoleSystem, Content: claudeSubTestSpec().SystemPrompt}
	u1, a1, u2, _, second := claudeSubTwoTurnsUnderPrompt(t, pool, promptA)
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    model,
		Messages:                 []Message{promptA, u1, a1, claudeSubUserText("summarize the older part")},
		BackendState:             second.BackendState,
		CompactionSummary:        true,
		CompactionSourceMessages: 2,
	})

	// Route: in place. The source ends on a1, whose entry has no uuid, so nothing forks. The
	// marked instruction goes to the live parent, which is then retired.
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawned %d processes, want 1 (no fork helper without a source tail uuid)", got)
	}
	if lines := strings.Join(spawner.call(t, 0).Conn.sentLines(), "\n"); !strings.Contains(lines, "steiner compaction summary request") {
		t.Error("in-place summary was not sent marked to the live parent")
	}
	if summary.BackendState != nil || !summary.ClearBackendState || summary.Delta.Content == "" {
		t.Errorf("in-place summary chunk = state %s clear %v text %q, want text, no state and clear", summary.BackendState, summary.ClearBackendState, summary.Delta.Content)
	}
	pool.mu.Lock()
	_, live := pool.sessions["parent"]
	pool.mu.Unlock()
	if live {
		t.Error("in-place summary left the tainted parent pooled")
	}
	parentLines := len(claudeSubUserLines(spawner.call(t, 0).Conn))

	// The retained suffix starts at u2: u2 and its answer, then
	// the next user message.
	adopted := Message{Role: MessageRoleSystem, Content: summary.Delta.Content + "\n\n" + claudeSubHistoryLocator}
	nextMsgs := []Message{promptA, adopted, u2, second.Delta, claudeSubUserText("third question")}
	next, prompt := claudeSubRunTurnPrompt(t, pool, spawner, 1, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         nextMsgs,
	})

	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawned %d processes, want 2 (parent and fresh process)", got)
	}
	if _, ok := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); ok {
		t.Error("fresh process resumed a session; the request carries no backend state")
	}
	if n := strings.Count(prompt, claudeSubHistoryLocator); n != 1 {
		t.Errorf("transport system prompt carries the history locator %d times, want 1: %q", n, prompt)
	}
	if !strings.Contains(prompt, summary.Delta.Content) {
		t.Errorf("transport system prompt lacks the new summary %q: %q", summary.Delta.Content, prompt)
	}
	if next.BackendState == nil {
		t.Error("next turn returned no backend state for the new generation")
	}
	wantFP, err := claudeSubFingerprint(claudeSubSystemPrompt(nextMsgs), nil)
	if err != nil {
		t.Fatalf("fingerprint adopted prompt: %v", err)
	}
	if got := claudeSubDecodeState(t, next.BackendState).PromptToolsFingerprint; got != wantFP {
		t.Errorf("new generation fingerprint = %q, want the adopted prompt's %q", got, wantFP)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("old parent process was not closed by the next turn")
	}
	reseed := strings.Join(claudeSubUserLines(spawner.call(t, 1).Conn), "\n")
	if !strings.Contains(reseed, "Conversation so far") || !strings.Contains(reseed, "second question") || !strings.Contains(reseed, "third question") || strings.Contains(reseed, "first question") {
		t.Errorf("fresh process transcript = %q, want the retained suffix without the summarized source", reseed)
	}
	if got := len(claudeSubUserLines(spawner.call(t, 0).Conn)); got != parentLines {
		t.Errorf("old parent received %d new user lines, want none", got-parentLines)
	}
}
