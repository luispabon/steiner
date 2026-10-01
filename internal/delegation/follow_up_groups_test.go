package delegation

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestFollowUpMixedGroupOrders(t *testing.T) {
	for _, order := range []string{"fresh-first", "follow-up-first"} {
		t.Run(order, func(t *testing.T) {
			groupName := "mixed-" + order
			runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				return agent.RunState{Conversation: req.SourceConversation, TurnCount: 2, StopReason: agent.StopReasonComplete}, nil
			}}
			deps := minimalDeps(runner)
			deps.AsyncSubAgents = true
			deps.SessionStore = NewSessionStore()
			var completions *channelSink
			deps.Supervisor, completions = newAsyncSupervisor(4, nil)
			deps.GroupScope = deps.Supervisor.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
			deps.SubAgentCfg = config.SubAgentConfig{MaxTurns: 3, MaxTokens: 30, MaxFollowUps: 10, MaxParallel: 4}
			deps.SessionStore.Save(followUpGroupSession("warm-" + order))
			subDeps := deps.SubAgentHandlerDeps
			specialized := SpecializedToolDeps{SubAgentHandlerDeps: subDeps}
			fresh := SubAgentToolDef(specialized, nil).Handler
			follow := NewFollowUpHandler(subDeps)
			ctx := batchCtx("response-" + order)
			callFresh := func() {
				t.Helper()
				_, err := fresh(ctx, map[string]any{"type": string(AgentTypeExplore), "objective": "inspect", "context": "context", "deliverable": "answer", "group": groupName})
				if err != nil {
					t.Fatalf("fresh dispatch: %v", err)
				}
			}
			callFollow := func() {
				t.Helper()
				_, err := follow(ctx, map[string]any{"agent_id": "warm-" + order, "message": "continue", "group": groupName})
				if err != nil {
					t.Fatalf("follow_up dispatch: %v", err)
				}
			}
			if order == "fresh-first" {
				callFresh()
				callFollow()
			} else {
				callFollow()
				callFresh()
			}
			waitUntil(t, func() bool { return len(deps.Supervisor.Pending()) == 2 })
			deps.Supervisor.SealGroupBatch(deps.GroupScope, "response-"+order)
			select {
			case delivered := <-completions.ch:
				if len(delivered) != 2 {
					t.Fatalf("delivered %d completions, want both group members: %+v", len(delivered), delivered)
				}
			case <-time.After(time.Second):
				t.Fatal("group completion not delivered")
			}
		})
	}
}

func TestFollowUpOmittedGroupIsUngroupedAndReuseRejected(t *testing.T) {
	store := NewSessionStore()
	store.Save(followUpGroupSession("warm"))
	s, sink := newAsyncSupervisor(2, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1, Names: []string{"old-name"}})
	deps := SubAgentHandlerDeps{
		SubAgentCfg: config.SubAgentConfig{MaxTurns: 3, MaxTokens: 30, MaxFollowUps: 10, MaxParallel: 2},
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			return agent.RunState{Conversation: req.SourceConversation, TurnCount: 2, StopReason: agent.StopReasonComplete}, nil
		}},
		SessionStore:   store,
		Supervisor:     s,
		AsyncSubAgents: true,
		GroupScope:     scope,
	}
	handler := NewFollowUpHandler(deps)
	if _, err := handler(batchCtx("prior"), map[string]any{"agent_id": "warm", "message": "grouped", "group": "old-name"}); err == nil {
		t.Fatal("old ledger name was accepted")
	}
	if got := s.SnapshotGroupLedger(scope).Names; !reflect.DeepEqual(got, []string{"old-name"}) {
		t.Fatalf("ledger after rejection = %v", got)
	}
	if _, err := handler(batchCtx("accepted"), map[string]any{"agent_id": "warm", "message": "fresh", "group": "new-name"}); err != nil {
		t.Fatalf("fresh name rejected: %v", err)
	}
	waitUntil(t, func() bool { p := s.Pending(); return len(p) == 1 && p[0].State == agent.SubAgentFinished })
	if got := s.SnapshotGroupLedger(scope).Names; !reflect.DeepEqual(got, []string{"new-name", "old-name"}) {
		t.Fatalf("ledger names = %v", got)
	}
	s.SealGroupBatch(scope, "accepted")
	batch := recv(t, sink.ch, "accepted follow-up")
	if len(batch) != 1 || batch[0].AgentID != "warm" {
		t.Fatalf("completion = %+v", batch)
	}
	s.MarkDelivered([]string{batch[0].ParentCallID})
	if _, err := handler(batchCtx("omitted"), map[string]any{"agent_id": "warm", "message": "without a group"}); err != nil {
		t.Fatalf("ungrouped follow-up: %v", err)
	}
	waitUntil(t, func() bool { p := s.Pending(); return len(p) == 1 && p[0].State == agent.SubAgentFinished })
	if got := s.SnapshotGroupLedger(scope).Names; !reflect.DeepEqual(got, []string{"new-name", "old-name"}) {
		t.Fatalf("omitted group changed ledger: %v", got)
	}
	s.SealGroupBatch(scope, "omitted")
	ungrouped := recv(t, sink.ch, "ungrouped follow-up")
	if len(ungrouped) != 1 || ungrouped[0].AgentID != "warm" {
		t.Fatalf("omitted group completion = %+v", ungrouped)
	}
	ledger := s.Ledger()
	if len(ledger) != 1 || ledger[0].Group != "" {
		t.Fatalf("omitted group ledger = %+v, want ungrouped warm child", ledger)
	}
}

func TestFollowUpRejectedAdmissionLeavesSessionAndGroupNames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*SessionStore)
		input   map[string]any
	}{
		{name: "unknown", input: map[string]any{"agent_id": "missing", "message": "continue", "group": "reject-unknown"}},
		{name: "invalid input", input: map[string]any{"agent_id": "warm", "message": "", "group": "reject-invalid"}},
		{name: "dead code worktree", prepare: func(store *SessionStore) {
			session, _ := store.Get("warm")
			session.Request.Tools = []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}}
			session.Remediation = &RemediationConfig{WorktreePath: t.TempDir() + "/missing", ExpectedBranch: "gone"}
		}, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-dead"}},
		{name: "plan mode", prepare: func(store *SessionStore) {
			session, _ := store.Get("warm")
			session.Request.Tools = []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}}
		}, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-plan"}},
		{name: "invalidated", prepare: func(store *SessionStore) { store.Invalidate("warm") }, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-invalidated"}},
		{name: "follow-up limit", prepare: func(store *SessionStore) { session, _ := store.Get("warm"); session.FollowUpCount = 1 }, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-limit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewSessionStore()
			store.Save(followUpGroupSession("warm"))
			if tc.prepare != nil {
				tc.prepare(store)
			}
			before, exists := store.Get("warm")
			var beforeCopy ChildSession
			if exists {
				beforeCopy = *before
			}
			s, _ := newAsyncSupervisor(2, nil)
			scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
			handler := NewFollowUpHandler(SubAgentHandlerDeps{
				SubAgentCfg: config.SubAgentConfig{MaxTurns: 3, MaxTokens: 30, MaxFollowUps: 1, MaxParallel: 2},
				Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
					return agent.RunState{Conversation: req.SourceConversation, TurnCount: 2}, nil
				}},
				SessionStore: store, Supervisor: s, AsyncSubAgents: true, GroupScope: scope,
			})
			ctx := batchCtx("reject-" + tc.name)
			if tc.name == "plan mode" {
				ctx = context.WithValue(ctx, tool.ExecutionModeKey{}, config.ExecutionModePlan)
			}
			if _, err := handler(ctx, tc.input); err == nil {
				t.Fatal("rejected follow_up succeeded")
			}
			if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
				t.Fatalf("rejection reserved group names: %v", got)
			}
			after, afterExists := store.Get("warm")
			if afterExists != exists || (exists && !reflect.DeepEqual(*after, beforeCopy)) {
				t.Fatalf("session changed on rejection: before=%+v after=%+v", beforeCopy, after)
			}
			if pending := s.Pending(); len(pending) != 0 {
				t.Fatalf("rejection admitted child: %+v", pending)
			}
		})
	}
}

func TestFollowUpBusyRejectionDoesNotReserveGroup(t *testing.T) {
	store := NewSessionStore()
	original := followUpGroupSession("warm")
	store.Save(original)
	s, _ := newAsyncSupervisor(1, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	block := make(chan struct{})
	_, err := s.Spawn(batchCtx("busy-batch"), ChildJob{AgentID: "warm", Execute: func(context.Context) (tool.ExecutionResult, error) {
		<-block
		return tool.ExecutionResult{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return s.IsPending("warm") })
	handler := NewFollowUpHandler(SubAgentHandlerDeps{SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 10, MaxParallel: 1}, SessionStore: store, Supervisor: s, AsyncSubAgents: true, GroupScope: scope})
	_, err = handler(batchCtx("busy-follow-up"), map[string]any{"agent_id": "warm", "message": "continue", "group": "busy-fresh-name"})
	if err == nil {
		t.Fatal("busy follow_up was accepted")
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
		t.Fatalf("busy rejection reserved group names: %v", got)
	}
	after, ok := store.Get("warm")
	if !ok || !reflect.DeepEqual(*after, *original) {
		t.Fatalf("session changed on busy rejection: %+v", after)
	}
	close(block)
}

func TestFollowUpQueuedAndFinishedUndeliveredRejections(t *testing.T) {
	for _, state := range []string{"queued", "finished-undelivered"} {
		t.Run(state, func(t *testing.T) {
			store := NewSessionStore()
			original := followUpGroupSession("warm")
			store.Save(original)
			s, sink := newAsyncSupervisor(1, nil)
			scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
			block := make(chan struct{})
			_, err := s.Spawn(batchCtx("blocker"), ChildJob{AgentID: "blocker", ParentCallID: "blocker-call", Execute: func(context.Context) (tool.ExecutionResult, error) { <-block; return tool.ExecutionResult{}, nil }})
			if err != nil {
				t.Fatal(err)
			}
			waitUntil(t, func() bool { p := s.Pending(); return len(p) == 1 && p[0].State == agent.SubAgentRunning })
			if state == "queued" {
				_, err = s.Spawn(batchCtx("queued"), ChildJob{AgentID: "warm", Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }})
			} else {
				close(block)
				<-sink.ch
				s.MarkDelivered([]string{"blocker-call"})
				_, err = s.Spawn(batchCtx("finished"), ChildJob{AgentID: "warm", ParentCallID: "warm-call", Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }})
				if err == nil {
					waitUntil(t, func() bool { p := s.Pending(); return len(p) == 1 && p[0].State == agent.SubAgentFinished })
					<-sink.ch
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			handler := NewFollowUpHandler(SubAgentHandlerDeps{SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 10}, SessionStore: store, Supervisor: s, AsyncSubAgents: true, GroupScope: scope})
			if _, err := handler(batchCtx("rejected-"+state), map[string]any{"agent_id": "warm", "message": "continue", "group": "busy-" + state}); err == nil {
				t.Fatal("busy follow_up accepted")
			}
			if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
				t.Fatalf("rejection reserved group: %v", got)
			}
			after, ok := store.Get("warm")
			if !ok || !reflect.DeepEqual(*after, *original) {
				t.Fatalf("session changed: %+v", after)
			}
			if state == "queued" {
				close(block)
			}
		})
	}
}

func followUpGroupSession(id string) *ChildSession {
	return &ChildSession{
		Spec:         Spec{AgentID: id, AgentType: AgentTypeReview, Task: "inspect"},
		Request:      agent.RunRequest{Prompt: promptWithConversation("initial")},
		Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "initial"}},
		TurnCount:    1,
	}
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
