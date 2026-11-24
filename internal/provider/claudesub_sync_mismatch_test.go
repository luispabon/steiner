package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// claudeSubSyncMismatchBase returns a sync whose record holds a user message, an
// assistant message with one tool call, and that call's tool result, built
// through the same plan and commit path a turn uses.
func claudeSubSyncMismatchBase(t *testing.T) *claudeSubSync {
	t.Helper()
	user := claudeSubSyncUserMsg("hi")
	s := claudeSubSyncStartedSync(t, user)
	assistant := claudeSubSyncAssistantMsg("done", "c1")
	s.commitAssistant(assistant, "")
	tool := claudeSubSyncToolMsg("c1", "shot")
	delta, err := s.plan(ChatRequest{Messages: []Message{user, assistant, tool}})
	if err != nil {
		t.Fatalf("setup tool plan: %v", err)
	}
	if len(delta.ToolResults) != 1 {
		t.Fatalf("setup tool plan results = %d, want 1", len(delta.ToolResults))
	}
	claudeSubSyncCommit(s, delta)
	return s
}

// TestClaudeSubSyncPlanMismatch proves plan reports the first failed request
// comparison as a bounded index, that position's role, and the fixed check
// label, and reports nothing for a request the record accepts.
func TestClaudeSubSyncPlanMismatch(t *testing.T) {
	base := []Message{
		claudeSubSyncUserMsg("hi"),
		claudeSubSyncAssistantMsg("done", "c1"),
		claudeSubSyncToolMsg("c1", "shot"),
	}
	tests := []struct {
		name      string
		msgs      []Message
		wantIndex int
		wantRole  MessageRole
		wantCheck string
	}{
		{
			name: "matching append records nothing",
			msgs: append(append([]Message(nil), base...), claudeSubSyncUserMsg("more")),
		},
		{
			name:      "role change at the first position",
			msgs:      []Message{claudeSubSyncAssistantMsg("hi"), base[1], base[2]},
			wantRole:  MessageRoleAssistant,
			wantCheck: claudeSubCheckRole,
		},
		{
			name:      "digest change at the first position",
			msgs:      []Message{claudeSubSyncUserMsg("hi!"), base[1], base[2]},
			wantRole:  MessageRoleUser,
			wantCheck: claudeSubCheckDigest,
		},
		{
			name:      "added image at the first position",
			msgs:      []Message{claudeSubSyncUserImage("hi", ImageBlock{MediaType: "image/png", Data: "AAAA"}), base[1], base[2]},
			wantRole:  MessageRoleUser,
			wantCheck: claudeSubCheckImages,
		},
		{
			name:      "assistant tool-call change",
			msgs:      []Message{base[0], claudeSubSyncAssistantMsg("done", "c9"), base[2]},
			wantIndex: 1,
			wantRole:  MessageRoleAssistant,
			wantCheck: claudeSubCheckToolCalls,
		},
		{
			name:      "tool result id change",
			msgs:      []Message{base[0], base[1], claudeSubSyncToolMsg("c9", "shot")},
			wantIndex: 2,
			wantRole:  MessageRoleTool,
			wantCheck: claudeSubCheckToolCalls,
		},
		{
			name:      "appended assistant",
			msgs:      append(append([]Message(nil), base...), claudeSubSyncAssistantMsg("extra")),
			wantIndex: 3,
			wantRole:  MessageRoleAssistant,
			wantCheck: claudeSubCheckShape,
		},
		{
			name:      "duplicate suffix tool result",
			msgs:      append(append([]Message(nil), base...), claudeSubSyncToolMsg("c1", "shot")),
			wantIndex: 3,
			wantRole:  MessageRoleTool,
			wantCheck: claudeSubCheckToolCalls,
		},
		{
			name:      "shortened request",
			msgs:      base[:2],
			wantIndex: 2,
			wantRole:  MessageRoleTool,
			wantCheck: claudeSubCheckLength,
		},
		{
			// The request is also shorter than the record, but the rewrite at
			// position zero is the first comparison that fails.
			name:      "shortened request with a changed first message",
			msgs:      []Message{claudeSubSyncUserMsg("hi!")},
			wantRole:  MessageRoleUser,
			wantCheck: claudeSubCheckDigest,
		},
		{
			name:      "first mismatch wins",
			msgs:      []Message{claudeSubSyncUserMsg("hi!"), base[1], base[2], claudeSubSyncAssistantMsg("extra")},
			wantRole:  MessageRoleUser,
			wantCheck: claudeSubCheckDigest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := claudeSubSyncMismatchBase(t)
			var mismatch claudeSubSyncMismatch
			_, err := s.planMismatch(ChatRequest{Messages: tc.msgs}, &mismatch)
			if tc.wantCheck == "" {
				if err != nil {
					t.Fatalf("planMismatch: %v", err)
				}
				if mismatch != (claudeSubSyncMismatch{}) {
					t.Fatalf("mismatch = %+v, want none", mismatch)
				}
				return
			}
			if !errors.Is(err, errClaudeSubHistoryChanged) {
				t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
			}
			want := claudeSubSyncMismatch{Index: tc.wantIndex, Role: tc.wantRole, Check: tc.wantCheck}
			if mismatch != want {
				t.Fatalf("mismatch = %+v, want %+v", mismatch, want)
			}
		})
	}
}

// TestClaudeSubSyncPlanMismatchFirstRequestShape proves a fresh transcript
// reports the position of the history it cannot start with.
func TestClaudeSubSyncPlanMismatchFirstRequestShape(t *testing.T) {
	msgs := []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("there")}
	var mismatch claudeSubSyncMismatch
	if _, err := (&claudeSubSync{}).planMismatch(ChatRequest{Messages: msgs}, &mismatch); !errors.Is(err, errClaudeSubHistoryChanged) {
		t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
	}
	want := claudeSubSyncMismatch{Index: 1, Role: MessageRoleAssistant, Check: claudeSubCheckShape}
	if mismatch != want {
		t.Fatalf("mismatch = %+v, want %+v", mismatch, want)
	}
}

// TestClaudeSubSyncPlanMismatchTaintedRecord proves a record an in-place summary
// rewrote fails without a request position, so no invented index is reported.
func TestClaudeSubSyncPlanMismatchTaintedRecord(t *testing.T) {
	s := &claudeSubSync{tainted: true}
	var mismatch claudeSubSyncMismatch
	if _, err := s.planMismatch(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi")}}, &mismatch); !errors.Is(err, errClaudeSubHistoryChanged) {
		t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
	}
	if mismatch != (claudeSubSyncMismatch{}) {
		t.Fatalf("mismatch = %+v, want none", mismatch)
	}
}

// TestClaudeSubCallMetricsSyncMismatchBounds proves only bounded mismatch
// values reach the call diagnostics: the index is clamped, and an unknown role
// or check label is dropped.
func TestClaudeSubCallMetricsSyncMismatchBounds(t *testing.T) {
	tests := []struct {
		name         string
		mismatch     claudeSubSyncMismatch
		wantRecorded bool
		wantIndex    int
	}{
		{
			name:         "zero index is recorded",
			mismatch:     claudeSubSyncMismatch{Index: 0, Role: MessageRoleUser, Check: claudeSubCheckDigest},
			wantRecorded: true,
			wantIndex:    0,
		},
		{
			name:         "negative index reports no position",
			mismatch:     claudeSubSyncMismatch{Index: -5, Check: claudeSubCheckLength},
			wantRecorded: true,
			wantIndex:    claudeSubSyncMismatchNoIndex,
		},
		{
			name:     "negative index with a role is dropped",
			mismatch: claudeSubSyncMismatch{Index: -5, Role: MessageRoleTool, Check: claudeSubCheckLength},
		},
		{
			name:         "record-state refusal reports no position",
			mismatch:     claudeSubSyncMismatch{Index: claudeSubSyncMismatchNoIndex, Check: claudeSubCheckInterrupted},
			wantRecorded: true,
			wantIndex:    claudeSubSyncMismatchNoIndex,
		},
		{
			name:     "record-state refusal with a role is dropped",
			mismatch: claudeSubSyncMismatch{Index: claudeSubSyncMismatchNoIndex, Role: MessageRoleUser, Check: claudeSubCheckTainted},
		},
		{
			name:         "large index clamps to the bound",
			mismatch:     claudeSubSyncMismatch{Index: claudeSubSyncMismatchMaxIndex + 10, Role: MessageRoleAssistant, Check: claudeSubCheckImages},
			wantRecorded: true,
			wantIndex:    claudeSubSyncMismatchMaxIndex,
		},
		{
			name:     "unknown check is dropped",
			mismatch: claudeSubSyncMismatch{Index: 1, Role: MessageRoleUser, Check: "digest:deadbeef"},
		},
		{
			name:     "unknown role is dropped",
			mismatch: claudeSubSyncMismatch{Index: 1, Role: MessageRole("user:someone"), Check: claudeSubCheckDigest},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &claudeSubCallMetrics{}
			m.noteSyncMismatch(tc.mismatch)
			diagnostics := m.diagnostics()
			if !tc.wantRecorded {
				if diagnostics != nil {
					t.Fatalf("diagnostics = %+v, want none", diagnostics.ClaudeSub)
				}
				return
			}
			if diagnostics == nil {
				t.Fatal("diagnostics = nil, want a record")
			}
			got := diagnostics.ClaudeSub
			if got.SyncMismatchIndex == nil {
				t.Fatal("mismatch index = nil, want a value")
			}
			if *got.SyncMismatchIndex != tc.wantIndex {
				t.Errorf("mismatch index = %d, want %d", *got.SyncMismatchIndex, tc.wantIndex)
			}
			if got.SyncMismatchRole != string(tc.mismatch.Role) {
				t.Errorf("mismatch role = %q, want %q", got.SyncMismatchRole, tc.mismatch.Role)
			}
			if got.SyncMismatchCheck != tc.mismatch.Check {
				t.Errorf("mismatch check = %q, want %q", got.SyncMismatchCheck, tc.mismatch.Check)
			}
		})
	}
}

// TestClaudeSubCallMetricsSyncMismatchCleared proves a mismatch is reported only
// while the call reseeds because of it: a resume, another reseed branch, and a
// retried delivery all drop it.
func TestClaudeSubCallMetricsSyncMismatchCleared(t *testing.T) {
	mismatch := claudeSubSyncMismatch{Index: 0, Role: MessageRoleUser, Check: claudeSubCheckDigest}
	tests := []struct {
		name   string
		apply  func(*claudeSubCallMetrics)
		absent bool
	}{
		{
			name: "plan reseed keeps it",
			apply: func(m *claudeSubCallMetrics) {
				m.noteSyncMismatch(mismatch)
				m.noteReseed(claudeSubReseedPlan)
			},
		},
		{
			name: "another reseed branch drops it",
			apply: func(m *claudeSubCallMetrics) {
				m.noteSyncMismatch(mismatch)
				m.noteReseed(claudeSubReseedPlan)
				m.noteReseed(claudeSubReseedCheckpoint)
			},
			absent: true,
		},
		{
			name: "a settled reuse drops it",
			apply: func(m *claudeSubCallMetrics) {
				m.noteSyncMismatch(mismatch)
				m.noteReseed(claudeSubReseedPlan)
				m.clearReseed()
			},
			absent: true,
		},
		{
			name: "a retried delivery drops it",
			apply: func(m *claudeSubCallMetrics) {
				m.noteSyncMismatch(mismatch)
				m.noteReseed(claudeSubReseedPlan)
				m.resetDelivery()
			},
			absent: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &claudeSubCallMetrics{}
			tc.apply(m)
			diagnostics := m.diagnostics()
			if tc.absent {
				if diagnostics == nil {
					return
				}
				if got := diagnostics.ClaudeSub.SyncMismatchIndex; got != nil {
					t.Fatalf("mismatch index = %d, want none", *got)
				}
				if diagnostics.ClaudeSub.SyncMismatchCheck != "" {
					t.Fatalf("mismatch check = %q, want empty", diagnostics.ClaudeSub.SyncMismatchCheck)
				}
				return
			}
			if diagnostics == nil || diagnostics.ClaudeSub.SyncMismatchIndex == nil {
				t.Fatal("mismatch = none, want a record")
			}
			if *diagnostics.ClaudeSub.SyncMismatchIndex != 0 {
				t.Errorf("mismatch index = %d, want 0", *diagnostics.ClaudeSub.SyncMismatchIndex)
			}
		})
	}
}

// TestClaudeSubCallDiagnosticsSyncMismatch proves a call that reseeds because a
// live process could not take the request reports where the comparison failed,
// and that the report carries no conversation content, digest or identifier.
func TestClaudeSubCallDiagnosticsSyncMismatch(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
	first := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("private-marker")},
	})
	rewritten := claudeSubUserText("private-marker-rewritten")
	msgs := []Message{rewritten, first.Delta, claudeSubUserText("more")}
	final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         msgs,
		BackendState:     first.BackendState,
	})
	sub := final.Diagnostics.ClaudeSub
	if sub.ReseedReason != claudeSubReseedPlan {
		t.Fatalf("reseed reason = %q, want %q", sub.ReseedReason, claudeSubReseedPlan)
	}
	if sub.SyncMismatchIndex == nil {
		t.Fatal("mismatch index = nil, want 0")
	}
	if *sub.SyncMismatchIndex != 0 {
		t.Errorf("mismatch index = %d, want 0", *sub.SyncMismatchIndex)
	}
	if sub.SyncMismatchRole != string(MessageRoleUser) {
		t.Errorf("mismatch role = %q, want %q", sub.SyncMismatchRole, MessageRoleUser)
	}
	if sub.SyncMismatchCheck != claudeSubCheckDigest {
		t.Errorf("mismatch check = %q, want %q", sub.SyncMismatchCheck, claudeSubCheckDigest)
	}
	raw, err := json.Marshal(final.Diagnostics)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	for _, secret := range []string{rewritten.Content, claudeSubDigest(rewritten)} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("diagnostics carry %q: %s", secret, raw)
		}
	}
}

// TestClaudeSubCallDiagnosticsSyncMismatchClearedByResume drives a live process
// whose drained record refuses the request while the stored checkpoint still
// accepts it. The call records the mismatch, retires the process, forks the
// stored session, and reports the resume without the mismatch that only
// explained the rejection.
func TestClaudeSubCallDiagnosticsSyncMismatchClearedByResume(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
	first := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	stored := claudeSubDecodeState(t, first.BackendState)
	live, err := pool.acquireLive(context.Background(), "parent")
	if err != nil || live == nil {
		t.Fatalf("acquire live session = %v, err = %v", live, err)
	}
	// The stopped delivery wrote a user message the request now revises, so the
	// live record is one entry ahead of the stored checkpoint.
	live.sync.commitSent(claudeSubDelta{User: []Message{claudeSubSyncUserMsg("pending line")}})
	live.sync.markDrained(stored.SyncEntries)
	pool.release(live)

	final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("edited line")},
		BackendState:     claudeSubEncodeState(t, stored),
	})
	if final.Error != "" {
		t.Fatalf("turn error = %s", final.Error)
	}
	sub := final.Diagnostics.ClaudeSub
	if sub.Process != claudeSubProcessResume {
		t.Errorf("process = %q, want %q", sub.Process, claudeSubProcessResume)
	}
	if sub.Recovery != claudeSubRecoveryResumeAfterMismatch {
		t.Errorf("recovery = %q, want %q", sub.Recovery, claudeSubRecoveryResumeAfterMismatch)
	}
	if sub.ReseedReason != "" {
		t.Errorf("reseed reason = %q, want empty", sub.ReseedReason)
	}
	if sub.SyncMismatchIndex != nil || sub.SyncMismatchRole != "" || sub.SyncMismatchCheck != "" {
		t.Fatalf("mismatch = %+v, want none after the resume", sub)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2", n)
	}
	if resume, _ := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); resume == "" {
		t.Error("the second process did not fork the stored session")
	}
}

// TestClaudeSubCallDiagnosticsSyncMismatchAbsentWithoutReseed proves a live
// process that is retired and resumed, and a call that appends to it, report no
// sync mismatch.
func TestClaudeSubCallDiagnosticsSyncMismatchAbsentWithoutReseed(t *testing.T) {
	model := claudeSubTestSpec().Model
	t.Run("resume after a retired process", func(t *testing.T) {
		pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
		first := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes")},
		})
		st := claudeSubDecodeState(t, first.BackendState)
		st.SyncChainDigest = "bad"
		final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")},
			BackendState:     claudeSubEncodeState(t, st),
		})
		if got := final.Diagnostics.ClaudeSub; got.SyncMismatchIndex != nil || got.SyncMismatchCheck != "" || got.SyncMismatchRole != "" {
			t.Fatalf("mismatch = %+v, want none", got)
		}
	})
	t.Run("accepted append", func(t *testing.T) {
		pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2))
		first := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes")},
		})
		final := claudeSubRunMeasuredTurn(t, pool, ChatRequest{
			TransportSession: "parent",
			Model:            model,
			Messages:         []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")},
			BackendState:     first.BackendState,
		})
		if got := final.Diagnostics.ClaudeSub; got.SyncMismatchIndex != nil || got.SyncMismatchCheck != "" || got.SyncMismatchRole != "" {
			t.Fatalf("mismatch = %+v, want none", got)
		}
	})
}
