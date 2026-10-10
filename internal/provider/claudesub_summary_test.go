package provider

import (
	"context"
	"errors"
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

func TestClaudeSubSummaryForkResumesAtSourceTail(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
	u1, a1, _, first, second := claudeSubTwoTurnParent(t, pool)
	summary := claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession:         "parent",
		Model:                    claudeSubTestSpec().Model,
		Messages:                 []Message{u1, a1, claudeSubUserText("summarize the older part")},
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
	second2 := claudeSubDecodeState(t, second.BackendState)
	want := map[string]string{
		"--resume":            second2.CLISessionID,
		"--resume-session-at": claudeSubDecodeState(t, first.BackendState).FinalUUID,
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
	if summary.BackendState != nil || summary.ClearBackendState {
		t.Errorf("cold summary chunk = state %s clear %v, want no state change", summary.BackendState, summary.ClearBackendState)
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
	if summary.BackendState != nil || summary.ClearBackendState {
		t.Errorf("sub-agent summary chunk = state %s clear %v, want no state change", summary.BackendState, summary.ClearBackendState)
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
