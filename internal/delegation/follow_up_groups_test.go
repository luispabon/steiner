package delegation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
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
			deps.Events = &recordingEventSink{}
			subDeps := deps.SubAgentHandlerDeps
			specialized := SpecializedToolDeps{SubAgentHandlerDeps: subDeps}
			fresh := SubAgentToolDef(specialized, nil).Handler
			follow := NewFollowUpHandler(subDeps)
			ctx := batchCtx("response-" + order)
			freshCtx := context.WithValue(ctx, tool.ExecutionCallIDKey{}, "call-fresh-"+order)
			followCtx := context.WithValue(ctx, tool.ExecutionCallIDKey{}, "call-follow-"+order)
			var freshAdmission, followAdmission *tool.DelegationAdmission
			callFresh := func() {
				t.Helper()
				got, err := fresh(freshCtx, map[string]any{"type": string(AgentTypeExplore), "objective": "inspect", "context": "context", "deliverable": "answer", "group": groupName})
				if err != nil {
					t.Fatalf("fresh dispatch: %v", err)
				}
				admission := got.(tool.ExecutionResult).DelegationAdmission
				if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.Group != groupName || admission.BatchID != "response-"+order || admission.AgentID == "" {
					t.Fatalf("fresh admission = %+v", admission)
				}
				freshAdmission = admission.Clone()
			}
			callFollow := func() {
				t.Helper()
				got, err := follow(followCtx, map[string]any{"agent_id": "warm-" + order, "message": "continue", "group": groupName})
				if err != nil {
					t.Fatalf("follow_up dispatch: %v", err)
				}
				admission := got.(tool.ExecutionResult).DelegationAdmission
				if admission == nil || admission.Status != tool.DelegationAdmissionAccepted || admission.Group != groupName || admission.BatchID != "response-"+order || admission.AgentID != "warm-"+order {
					t.Fatalf("follow_up admission = %+v", admission)
				}
				followAdmission = admission.Clone()
			}
			if order == "fresh-first" {
				callFresh()
				callFollow()
			} else {
				callFollow()
				callFresh()
			}
			if freshAdmission == nil || followAdmission == nil || freshAdmission.AgentID == followAdmission.AgentID {
				t.Fatalf("mixed admissions are not distinct: fresh=%+v follow=%+v", freshAdmission, followAdmission)
			}
			waitUntil(t, func() bool {
				pending := deps.Supervisor.Pending()
				return len(pending) == 2 && pending[0].State == agent.SubAgentFinished && pending[1].State == agent.SubAgentFinished
			})
			ledger := deps.Supervisor.Ledger()
			if len(ledger) != 2 || ledger[0].AgentID == ledger[1].AgentID || ledger[0].ParentCallID == "" || ledger[1].ParentCallID == "" || ledger[0].ParentCallID == ledger[1].ParentCallID {
				t.Fatalf("group ledger = %+v, want distinct agent/call identities", ledger)
			}
			for _, entry := range ledger {
				if entry.Group != groupName || entry.BatchID != "response-"+order || (entry.AgentID == "warm-"+order && entry.ParentCallID != "call-follow-"+order) || (entry.AgentID != "warm-"+order && entry.ParentCallID != "call-fresh-"+order) {
					t.Fatalf("admission ledger entry = %+v", entry)
				}
			}
			select {
			case got := <-completions.ch:
				t.Fatalf("completion delivered before seal: %+v", got)
			default:
			}
			if events, ok := deps.Events.(*recordingEventSink); !ok {
				t.Fatal("recording event sink unavailable")
			} else {
				for _, event := range events.Events() {
					if started, ok := event.Payload.(output.DelegationStartedEvent); ok && started.AgentID == "warm-"+order && started.CallID != "call-follow-"+order {
						t.Fatalf("follow-up started call ID = %q", started.CallID)
					}
				}
			}
			deps.Supervisor.SealGroupBatch(deps.GroupScope, "response-"+order)
			select {
			case delivered := <-completions.ch:
				if len(delivered) != 2 {
					t.Fatalf("delivered %d completions, want both group members: %+v", len(delivered), delivered)
				}
				seen := map[string]bool{}
				for _, completion := range delivered {
					seen[completion.ParentCallID] = true
				}
				if len(seen) != 2 || !seen["call-fresh-"+order] || !seen["call-follow-"+order] {
					t.Fatalf("joint completions = %+v, want both call identities", delivered)
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
	before := cloneFollowUpTestSession(store)
	if _, err := handler(batchCtx("prior"), map[string]any{"agent_id": "warm", "message": "grouped", "group": "old-name"}); err == nil || !strings.Contains(err.Error(), "was already used") {
		t.Fatalf("old ledger name error = %v, want reused-name rejection", err)
	}
	if after := cloneFollowUpTestSession(store); !reflect.DeepEqual(after, before) {
		t.Fatalf("session changed on old-name rejection: before=%+v after=%+v", before, after)
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
		wantErr string
		busy    bool
	}{
		{name: "unknown", input: map[string]any{"agent_id": "missing", "message": "continue", "group": "reject-unknown"}, wantErr: "has no session"},
		{name: "invalid input", input: map[string]any{"agent_id": "warm", "message": "", "group": "reject-invalid"}, wantErr: "message is required"},
		{name: "dead code worktree", prepare: func(store *SessionStore) {
			session, _ := store.Get("warm")
			session.Request.Tools = []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}}
			session.Remediation = &RemediationConfig{WorktreePath: t.TempDir() + "/missing", ExpectedBranch: "gone"}
		}, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-dead"}, wantErr: "no longer usable"},
		{name: "plan mode", prepare: func(store *SessionStore) {
			session, _ := store.Get("warm")
			session.Request.Tools = []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}}
		}, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-plan"}, wantErr: "plan mode is active"},
		{name: "invalidated nonresumable session", prepare: func(store *SessionStore) { store.Invalidate("warm") }, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-invalidated"}, wantErr: "has no session"},
		{name: "follow-up limit", prepare: func(store *SessionStore) { session, _ := store.Get("warm"); session.FollowUpCount = 1 }, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-limit"}, wantErr: "reached the maximum"},
		{name: "running", busy: true, input: map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-running"}, wantErr: "still running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewSessionStore()
			store.Save(followUpGroupSession("warm"))
			if tc.prepare != nil {
				tc.prepare(store)
			}
			beforeCopy := cloneFollowUpTestSession(store)
			s, _ := newAsyncSupervisor(2, nil)
			scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
			input := tc.input
			if input == nil {
				input = map[string]any{"agent_id": "warm", "message": "continue", "group": "reject-" + tc.name}
			}
			var release chan struct{}
			if tc.busy {
				release = make(chan struct{})
				_, err := s.Spawn(batchCtx("already-running"), ChildJob{AgentID: "warm", Execute: func(context.Context) (tool.ExecutionResult, error) { <-release; return tool.ExecutionResult{}, nil }})
				if err != nil {
					t.Fatal(err)
				}
				waitUntil(t, func() bool {
					pending := s.Pending()
					return len(pending) == 1 && pending[0].State == agent.SubAgentRunning
				})
			}
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
			_, err := handler(ctx, input)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("rejection error = %v, want %q", err, tc.wantErr)
			}
			if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
				t.Fatalf("rejection reserved group names: %v", got)
			}
			afterCopy := cloneFollowUpTestSession(store)
			if !reflect.DeepEqual(afterCopy, beforeCopy) {
				t.Fatalf("session changed on rejection: before=%+v after=%+v", beforeCopy, afterCopy)
			}
			pending := s.Pending()
			if tc.busy {
				if len(pending) != 1 || pending[0].State != agent.SubAgentRunning {
					t.Fatalf("running child state = %+v", pending)
				}
				close(release)
			} else if len(pending) != 0 {
				t.Fatalf("rejection admitted child: %+v", pending)
			}
		})
	}
}

func TestFollowUpBusyRejectionDoesNotReserveGroup(t *testing.T) {
	store := NewSessionStore()
	store.Save(followUpGroupSession("warm"))
	original := cloneFollowUpTestSession(store)
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
	after := cloneFollowUpTestSession(store)
	if !reflect.DeepEqual(after, original) {
		t.Fatalf("session changed on busy rejection: %+v", after)
	}
	close(block)
}

func TestFollowUpConcurrentBusyRejectionDoesNotReserveRejectedName(t *testing.T) {
	store := NewSessionStore()
	store.Save(followUpGroupSession("warm"))
	before := cloneFollowUpTestSession(store)
	started := make(chan struct{})
	release := make(chan struct{})
	s, sink := newAsyncSupervisor(2, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		close(started)
		<-release
		return agent.RunState{Conversation: req.SourceConversation, TurnCount: 2, StopReason: agent.StopReasonComplete}, nil
	}}
	handler := NewFollowUpHandler(SubAgentHandlerDeps{SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 10, MaxTurns: 3, MaxTokens: 30}, SessionStore: store, Supervisor: s, AsyncSubAgents: true, GroupScope: scope, Runner: runner})
	start := make(chan struct{})
	type outcome struct {
		group string
		err   error
	}
	outcomes := make(chan outcome, 2)
	for _, group := range []string{"concurrent-a", "concurrent-b"} {
		go func(group string) {
			<-start
			ctx := context.WithValue(batchCtx("concurrent-batch"), tool.ExecutionCallIDKey{}, "call-"+group)
			_, err := handler(ctx, map[string]any{"agent_id": "warm", "message": group, "group": group})
			outcomes <- outcome{group: group, err: err}
		}(group)
	}
	close(start)
	waitUntil(t, func() bool { return len(s.Pending()) == 1 })
	<-started
	first, second := <-outcomes, <-outcomes
	var accepted, rejected outcome
	for _, got := range []outcome{first, second} {
		if got.err == nil {
			accepted = got
		} else {
			rejected = got
		}
	}
	if accepted.err != nil || rejected.err == nil || (!strings.Contains(rejected.err.Error(), "still running, queued, or has a result") && !errors.Is(rejected.err, ErrAgentAlreadyActive)) {
		t.Fatalf("concurrent outcomes = {%s, %v}, {%s, %v}", first.group, first.err, second.group, second.err)
	}
	if afterAdmission := cloneFollowUpTestSession(store); !reflect.DeepEqual(afterAdmission, before) {
		t.Fatalf("session changed before winning execution: before=%+v after=%+v", before, afterAdmission)
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 || got[0] != accepted.group {
		t.Fatalf("ledger names = %v, want only accepted group %q", got, accepted.group)
	}
	pending := s.Pending()
	if len(pending) != 1 || pending[0].State != agent.SubAgentRunning {
		t.Fatalf("pending after competing calls = %+v", pending)
	}
	close(release)
	waitUntil(t, func() bool {
		pending := s.Pending()
		return len(pending) == 1 && pending[0].State == agent.SubAgentFinished
	})
	s.SealGroupBatch(scope, "concurrent-batch")
	completion := recv(t, sink.ch, "winning follow-up completion")
	if len(completion) != 1 || completion[0].AgentID != "warm" || completion[0].ParentCallID != "call-"+accepted.group {
		t.Fatalf("winner completion = %+v", completion)
	}
	afterWinner := cloneFollowUpTestSession(store)
	if afterWinner.FollowUpCount != before.FollowUpCount+1 || afterWinner.TurnCount != before.TurnCount+1 {
		t.Fatalf("winner counters = followups %d turns %d, baseline followups %d turns %d", afterWinner.FollowUpCount, afterWinner.TurnCount, before.FollowUpCount, before.TurnCount)
	}
	if len(afterWinner.Conversation) != len(before.Conversation)+1 || afterWinner.Conversation[len(afterWinner.Conversation)-1].Content != accepted.group {
		t.Fatalf("winner history = %+v, want only accepted message %q", afterWinner.Conversation, accepted.group)
	}
	if afterWinner.TokenCount != before.TokenCount || afterWinner.ToolCallCount != before.ToolCallCount {
		t.Fatalf("winner counters changed unexpectedly: tokens=%d tools=%d", afterWinner.TokenCount, afterWinner.ToolCallCount)
	}
}

func TestFollowUpQueuedAndFinishedUndeliveredRejections(t *testing.T) {
	for _, state := range []string{"queued", "finished-undelivered"} {
		t.Run(state, func(t *testing.T) {
			store := NewSessionStore()
			store.Save(followUpGroupSession("warm"))
			original := cloneFollowUpTestSession(store)
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
				waitUntil(t, func() bool { p := s.Pending(); return len(p) == 2 && p[1].State == agent.SubAgentQueued })
			} else {
				close(block)
				<-sink.ch
				s.MarkDelivered([]string{"blocker-call"})
				_, err = s.Spawn(batchCtx("finished"), ChildJob{AgentID: "warm", ParentCallID: "warm-call", Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }})
				if err == nil {
					waitUntil(t, func() bool { p := s.Pending(); return len(p) == 1 && p[0].State == agent.SubAgentFinished })
					<-sink.ch
					pending := s.Pending()
					if len(pending) != 1 || pending[0].State != agent.SubAgentFinished {
						t.Fatalf("finished-undelivered state = %+v", pending)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			handler := NewFollowUpHandler(SubAgentHandlerDeps{SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 10, MaxTurns: 3, MaxTokens: 30}, Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				return agent.RunState{Conversation: req.SourceConversation, TurnCount: 2, StopReason: agent.StopReasonComplete}, nil
			}}, SessionStore: store, Supervisor: s, AsyncSubAgents: true, GroupScope: scope})
			_, err = handler(batchCtx("rejected-"+state), map[string]any{"agent_id": "warm", "message": "continue", "group": "busy-" + state})
			if err == nil || !strings.Contains(err.Error(), "still running, queued, or has a result") {
				t.Fatalf("%s rejection = %v", state, err)
			}
			pending := s.Pending()
			if state == "queued" {
				if len(pending) != 2 || pending[1].AgentID != "warm" || pending[1].State != agent.SubAgentQueued || !s.IsPending("warm") {
					t.Fatalf("%s child state = %+v, IsPending=%t", state, pending, s.IsPending("warm"))
				}
			} else if len(pending) != 1 || pending[0].AgentID != "warm" || pending[0].State != agent.SubAgentFinished || !s.IsPending("warm") {
				t.Fatalf("%s child state = %+v, IsPending=%t", state, pending, s.IsPending("warm"))
			}
			if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
				t.Fatalf("rejection reserved group: %v", got)
			}
			after := cloneFollowUpTestSession(store)
			if !reflect.DeepEqual(after, original) {
				t.Fatalf("session changed: %+v", after)
			}
			if state == "queued" {
				close(block)
			} else {
				s.MarkDelivered([]string{"warm-call"})
			}
		})
	}
}

func cloneFollowUpTestSession(store *SessionStore) *ChildSession {
	session, ok := store.Get("warm")
	if !ok {
		return nil
	}
	clone := *session
	clone.Conversation = append([]agent.Message(nil), session.Conversation...)
	for i := range clone.Conversation {
		clone.Conversation[i].ToolCalls = append([]agent.ToolCall(nil), session.Conversation[i].ToolCalls...)
		clone.Conversation[i].Images = append([]agent.ImageBlock(nil), session.Conversation[i].Images...)
		if session.Conversation[i].Retention != nil {
			retention := *session.Conversation[i].Retention
			clone.Conversation[i].Retention = &retention
		}
		if session.Conversation[i].DelegationAdmission != nil {
			admission := *session.Conversation[i].DelegationAdmission
			clone.Conversation[i].DelegationAdmission = &admission
		}
		if session.Conversation[i].ProviderMetadata != nil {
			metadata := *session.Conversation[i].ProviderMetadata
			clone.Conversation[i].ProviderMetadata = &metadata
		}
	}
	clone.Request.SourceConversation = append([]agent.Message(nil), session.Request.SourceConversation...)
	clone.Request.Prompt.Conversation = append(clone.Request.Prompt.Conversation[:0:0], session.Request.Prompt.Conversation...)
	clone.Request.Tools = append([]provider.ToolSpec(nil), session.Request.Tools...)
	for i := range clone.Request.Tools {
		clone.Request.Tools[i].Function.Parameters = cloneFollowUpTestJSON(session.Request.Tools[i].Function.Parameters)
	}
	clone.Spec.Images = append([]provider.ImageBlock(nil), session.Spec.Images...)
	if session.Remediation != nil {
		remediation := *session.Remediation
		clone.Remediation = &remediation
	}
	return &clone
}

func cloneFollowUpTestJSON(value any) map[string]any {
	if value == nil {
		return nil
	}
	input, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	clone := make(map[string]any, len(input))
	for key, item := range input {
		if nested, ok := item.(map[string]any); ok {
			clone[key] = cloneFollowUpTestJSON(nested)
		} else if values, ok := item.([]any); ok {
			clone[key] = append([]any(nil), values...)
		} else {
			clone[key] = item
		}
	}
	return clone
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
