package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// asyncClock records timers, signals each registration and fires them only
// when the test says so.
type asyncClock struct {
	mu     sync.Mutex
	timers []func()
	armed  chan struct{}
}

func newAsyncClock() *asyncClock { return &asyncClock{armed: make(chan struct{}, 16)} }

func (c *asyncClock) Now() time.Time { return time.Time{} }

func (c *asyncClock) AfterFunc(_ time.Duration, f func()) agent.Timer {
	c.mu.Lock()
	c.timers = append(c.timers, f)
	c.mu.Unlock()
	c.armed <- struct{}{}
	return asyncTimer{}
}

func (c *asyncClock) fire(t *testing.T) {
	t.Helper()
	select {
	case <-c.armed:
	case <-time.After(10 * time.Second):
		t.Fatal("coalescing window was never armed")
	}
	c.mu.Lock()
	timers := c.timers
	c.timers = nil
	c.mu.Unlock()
	for _, f := range timers {
		f()
	}
}

type asyncTimer struct{}

func (asyncTimer) Stop() bool { return true }

// asyncScript is a provider whose parent turns follow a script and whose child
// turns block on a per-objective gate. Parent requests are told apart by the
// presence of the sub_agent tool, which children never receive.
type asyncScript struct {
	mu      sync.Mutex
	parent  []func(req provider.ChatRequest) provider.ChatResponse
	next    int
	gates   map[string]chan struct{}
	started chan string
	turns   chan struct{}
}

func newAsyncScript(objectives ...string) *asyncScript {
	s := &asyncScript{gates: map[string]chan struct{}{}, started: make(chan string, 16), turns: make(chan struct{}, 64)}
	for _, o := range objectives {
		s.gates[o] = make(chan struct{})
	}
	return s
}

func (p *asyncScript) SupportsUsageStats() bool { return false }

func (p *asyncScript) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, fmt.Errorf("stream not used")
}

func (p *asyncScript) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	for _, spec := range req.Tools {
		if spec.Function.Name == "sub_agent" {
			return p.parentTurn(req), nil
		}
	}
	return p.childTurn(ctx, req)
}

func (p *asyncScript) parentTurn(req provider.ChatRequest) provider.ChatResponse {
	p.turns <- struct{}{}
	p.mu.Lock()
	var step func(provider.ChatRequest) provider.ChatResponse
	if p.next < len(p.parent) {
		step = p.parent[p.next]
		p.next++
	}
	p.mu.Unlock()
	if step == nil {
		return textResponse("done")
	}
	return step(req)
}

func (p *asyncScript) childTurn(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	marker := "## Objective\n\n"
	objective := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		message := req.Messages[i]
		if message.Role != provider.MessageRoleUser || strings.TrimSpace(message.Content) == "" {
			continue
		}
		content := message.Content
		if start := strings.LastIndex(content, marker); start >= 0 {
			objective = strings.SplitN(content[start+len(marker):], "\n", 2)[0]
		} else {
			objective = strings.SplitN(strings.TrimSpace(content), "\n", 2)[0]
		}
		break
	}
	if objective == "" {
		return provider.ChatResponse{}, fmt.Errorf("unscripted child request")
	}
	gate, ok := p.gates[objective]
	if !ok {
		return provider.ChatResponse{}, fmt.Errorf("unscripted child objective %q", objective)
	}
	p.started <- objective
	select {
	case <-gate:
	case <-ctx.Done():
		return provider.ChatResponse{}, ctx.Err()
	}
	return textResponse("finished " + objective), nil
}

func (p *asyncScript) release(objective string) { close(p.gates[objective]) }

func textResponse(text string) provider.ChatResponse {
	return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: text}, FinishReason: "stop"}
}

func toolCallsResponse(calls ...provider.ToolCall) provider.ChatResponse {
	return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: calls}, FinishReason: "tool_calls"}
}

func subAgentCall(id, objective, group string) provider.ToolCall {
	args := map[string]any{
		"type": "explore", "objective": objective, "context": "ctx " + objective,
		"deliverable": "answer", "constraints": []any{}, "success_criteria": []any{}, "checks": []any{},
	}
	if group != "" {
		args["group"] = group
	}
	return provider.ToolCall{ID: id, Name: "sub_agent", Arguments: args}
}

func step(resp provider.ChatResponse) func(provider.ChatRequest) provider.ChatResponse {
	return func(provider.ChatRequest) provider.ChatResponse { return resp }
}

var agentIDPattern = regexp.MustCompile(`child-[0-9a-z-]+`)

// toolResultFor returns the content of the tool message answering callID.
func toolResultFor(messages []provider.Message, callID string) string {
	for _, m := range messages {
		if m.Role == provider.MessageRoleTool && m.ToolCallID == callID {
			return m.Content
		}
	}
	return ""
}

func toolResultInConversation(conv []agent.Message, callID string) string {
	for _, m := range conv {
		if m.Role == agent.MessageRoleTool && m.ToolCallID == callID {
			return m.Content
		}
	}
	return ""
}

func subAgentResultMessages(conv []agent.Message) []agent.Message {
	var out []agent.Message
	for _, m := range conv {
		if m.Role == agent.MessageRoleUser && m.Source == agent.MessageSourceSubAgentResult {
			out = append(out, m)
		}
	}
	return out
}

// newAsyncSession builds the real interactive wiring: session, driver,
// supervisor, delegation registry and runner, over the scripted provider.
type asyncEventRecorder struct {
	mu     sync.Mutex
	events []output.Event
}

func newAsyncEventRecorder() *asyncEventRecorder {
	return &asyncEventRecorder{}
}

func (r *asyncEventRecorder) Emit(event output.Event) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *asyncEventRecorder) snapshot() []output.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]output.Event(nil), r.events...)
}

func (r *asyncEventRecorder) waitFor(t *testing.T, match func(output.Event) bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		for _, event := range r.snapshot() {
			if match(event) {
				return
			}
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline.C:
			t.Fatal("expected event was not emitted")
		}
	}
}

type asyncHarness struct {
	session    *interactive.Session
	clock      *asyncClock
	supervisor *delegation.Supervisor
	events     *asyncEventRecorder
	scope      string
}

// newAsyncHarness builds the production interactive group-scope, completion,
// event, and display wiring over the scripted provider. Child completion is
// delivered through the live driver's sink; no completion is injected by the test.
func newAsyncHarness(t *testing.T, prov *asyncScript, maxParallel int) *asyncHarness {
	t.Helper()
	cfg := testRuntimeConfig("test-model")
	cfg.SubAgent.Enabled = true
	cfg.SubAgent.MaxParallel = maxParallel
	cfg.SubAgent.MaxFollowUps = 4
	model := cfg.Models.Definitions["test-model"]
	model.Advanced.Limits.ContextWindow = 200_000
	model.Advanced.Limits.MaxOutputTokens = 4096
	cfg.Models.Definitions["test-model"] = model
	cfg.Limits.MaxTurns = 20
	cfg.Limits.MaxTokens = 1_000_000

	controller := delegation.NewActiveController()
	events := newAsyncEventRecorder()
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: maxParallel, Controller: controller})
	store := delegation.NewSessionStore()
	workDir := t.TempDir()
	rt := cliRuntime{
		cfg:                    cfg,
		provider:               prov,
		workDir:                workDir,
		homeDir:                t.TempDir(),
		events:                 events,
		delegationSessionStore: store,
		// Keep this harness ungated. Cache-key serialization has separate tests;
		// sharing it here can block the second child before the provider request.
		delegationCacheKeyStore:    nil,
		delegationActiveController: controller,
		delegationSupervisor:       sup,
	}
	clock := newAsyncClock()
	var scope string
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents: events,
		Config:     cfg,
		WorkDir:    workDir,
		HomeDir:    rt.homeDir,
		Background: sup,
		OpenGroupScope: func(seed agent.DelegationGroupLedger) (string, func()) {
			var release func()
			scope, release = sup.OpenGroupScope(seed)
			return scope, release
		},
		SnapshotGroupLedger: sup.SnapshotGroupLedger,
		SetCompletionSink:   sup.SetCompletionSink,
		Clock:               clock,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	rt.events = sess.EventSink()
	rt.registry = runtimeRegistryWithSinkAndMode(cfg, workDir, sess.DisplaySink(), true, sess.WorkflowHandoffResponder(sess.EventSink()), nil, nil, nil, withPendingSubAgents(sup))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sup.CancelAll(delegation.CancelCauseUser)
		sess.Close(ctx)
	})
	sess.SetRunner(sessionRunner{runner: cliRunner{runtime: rt, runMode: "interactive", sessionIDFn: sess.SessionID, modeGetterFunc: sess.Mode}})
	return &asyncHarness{session: sess, clock: clock, supervisor: sup, events: events, scope: scope}
}

func newAsyncSession(t *testing.T, prov provider.Provider, maxParallel int) (*interactive.Session, *asyncClock) {
	t.Helper()
	h := newAsyncHarness(t, prov.(*asyncScript), maxParallel)
	return h.session, h.clock
}

func waitRuns(t *testing.T, sess *interactive.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if !sess.WaitRuns(ctx) {
		t.Fatal("session did not settle")
	}
}

func submit(t *testing.T, sess *interactive.Session, text string) {
	t.Helper()
	if err := sess.Handle(context.Background(), interactive.SubmitPrompt{Text: text}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
}

// fireAndAwaitWake fires the shared coalescing window and waits for the run.
func fireAndAwaitWake(t *testing.T, sess *interactive.Session, p *asyncScript, clock *asyncClock) {
	t.Helper()
	for len(p.turns) > 0 {
		<-p.turns
	}
	clock.fire(t)
	select {
	case <-p.turns:
	case <-time.After(10 * time.Second):
		t.Fatal("delivery did not wake a parent run")
	}
	waitRuns(t, sess)
}

// clearTurnNotifications drops notifications from parent runs that have
// already settled before a child completion is released.
func clearTurnNotifications(p *asyncScript) {
	for len(p.turns) > 0 {
		<-p.turns
	}
}

// awaitImmediateWake waits for a completion with no unfinished sibling to
// start its parent run without arming the coalescing window.
func awaitImmediateWake(t *testing.T, sess *interactive.Session, p *asyncScript, clock *asyncClock) {
	t.Helper()
	select {
	case <-p.turns:
	case <-time.After(10 * time.Second):
		t.Fatal("delivery did not wake a parent run")
	}
	select {
	case <-clock.armed:
		t.Fatal("coalescing window armed despite no unfinished sibling")
	default:
	}
	waitRuns(t, sess)
}

func recvStarted(t *testing.T, p *asyncScript, want string) {
	t.Helper()
	select {
	case got := <-p.started:
		if got != want {
			t.Fatalf("child %q started, want %q", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("child %q never started", want)
	}
}

func recvStartedSet(t *testing.T, p *asyncScript, wants ...string) {
	t.Helper()
	seen := make(map[string]bool, len(wants))
	for len(seen) < len(wants) {
		select {
		case got := <-p.started:
			for _, want := range wants {
				if got == want {
					seen[got] = true
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("children %v did not all start, saw %v", wants, seen)
		}
	}
}

func acceptedAgentFor(events []output.Event, callID string) string {
	for _, event := range events {
		if event.Type != output.EventTypeDelegationAccepted {
			continue
		}
		payload, ok := event.Payload.(output.DelegationAcceptedEvent)
		if ok && payload.CallID == callID {
			return payload.AgentID
		}
	}
	return ""
}

func acceptedDelegationFor(events []output.Event, callID string) (output.DelegationAcceptedEvent, bool) {
	for _, event := range events {
		if event.Type != output.EventTypeDelegationAccepted {
			continue
		}
		payload, ok := event.Payload.(output.DelegationAcceptedEvent)
		if ok && payload.CallID == callID {
			return payload, true
		}
	}
	return output.DelegationAcceptedEvent{}, false
}

func waitForDelegationComplete(t *testing.T, h *asyncHarness, agentID string) {
	t.Helper()
	h.events.waitFor(t, func(event output.Event) bool {
		if event.Type != output.EventTypeDelegationComplete {
			return false
		}
		payload, ok := event.Payload.(output.DelegationCompleteEvent)
		return ok && payload.AgentID == agentID
	})
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		for _, pending := range h.supervisor.Pending() {
			if pending.AgentID == agentID {
				if pending.State != agent.SubAgentFinished {
					break
				}
				return
			}
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline.C:
			t.Fatalf("completed child %q never reached terminal ledger state", agentID)
		}
	}
}

func TestAsyncSubAgentsThroughInteractiveWiring(t *testing.T) {
	t.Parallel()
	t.Run("ack, guards, then result wakes a run", func(t *testing.T) {
		prov := newAsyncScript("task-one", "task-two")
		var firstAgentID string
		prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
			step(toolCallsResponse(subAgentCall("c1", "task-one", ""))),
			step(toolCallsResponse(subAgentCall("c2", "task-two", ""))),
			step(toolCallsResponse(subAgentCall("c3", "task-three", ""))),
			func(req provider.ChatRequest) provider.ChatResponse {
				firstAgentID = agentIDPattern.FindString(toolResultFor(req.Messages, "c1"))
				return toolCallsResponse(provider.ToolCall{ID: "c4", Name: "follow_up", Arguments: map[string]any{"agent_id": firstAgentID, "message": "more"}})
			},
			step(toolCallsResponse(provider.ToolCall{ID: "c5", Name: "workflow_handoff", Arguments: map[string]any{"next": "implement", "target": "."}})),
			step(textResponse("waiting")),
			step(textResponse("saw one")),
			step(textResponse("saw two")),
		}
		sess, clock := newAsyncSession(t, prov, 1)

		submit(t, sess, "go")
		recvStarted(t, prov, "task-one")
		waitRuns(t, sess)
		conv := sess.Conversation()

		if got := toolResultInConversation(conv, "c1"); !strings.Contains(got, `"status":"running"`) || firstAgentID == "" {
			t.Fatalf("c1 ack = %q (agent %q), want a running ack while the child is blocked", got, firstAgentID)
		}
		if got := toolResultInConversation(conv, "c2"); !strings.Contains(got, `"status":"queued"`) {
			t.Fatalf("c2 ack = %q, want queued", got)
		}
		if got := toolResultInConversation(conv, "c3"); !strings.Contains(got, "already outstanding") {
			t.Fatalf("c3 = %q, want the outstanding-cap error", got)
		}
		if got := toolResultInConversation(conv, "c4"); !strings.Contains(got, "still running, queued") {
			t.Fatalf("c4 = %q, want the follow_up pending error", got)
		}
		if got := toolResultInConversation(conv, "c5"); !strings.Contains(got, "still outstanding") {
			t.Fatalf("c5 = %q, want the workflow_handoff outstanding error", got)
		}
		if n := len(subAgentResultMessages(conv)); n != 0 {
			t.Fatalf("%d result messages before any child finished, want 0", n)
		}

		prov.release("task-one")
		fireAndAwaitWake(t, sess, prov, clock)
		results := subAgentResultMessages(sess.Conversation())
		if !strings.Contains(results[0].Content, `call_id="c1"`) || !strings.Contains(results[0].Content, "finished task-one") {
			t.Fatalf("result message = %q, want the c1 envelope", results[0].Content)
		}

		recvStarted(t, prov, "task-two")
		clearTurnNotifications(prov)
		prov.release("task-two")
		awaitImmediateWake(t, sess, prov, clock)
		if n := len(subAgentResultMessages(sess.Conversation())); n != 2 {
			t.Fatalf("%d result messages after both children finished, want 2", n)
		}
	})

	t.Run("grouped calls are delivered together", func(t *testing.T) {
		prov := newAsyncScript("task-alpha", "task-beta")
		prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
			step(toolCallsResponse(subAgentCall("g1", "task-alpha", "pair"), subAgentCall("g2", "task-beta", "pair"))),
			step(textResponse("waiting")),
			step(textResponse("saw both")),
		}
		sess, clock := newAsyncSession(t, prov, 4)

		submit(t, sess, "go")
		waitRuns(t, sess)
		prov.release("task-alpha")
		if n := len(subAgentResultMessages(sess.Conversation())); n != 0 {
			t.Fatalf("%d result messages after one grouped child finished, want 0", n)
		}
		clearTurnNotifications(prov)
		prov.release("task-beta")
		awaitImmediateWake(t, sess, prov, clock)

		results := subAgentResultMessages(sess.Conversation())
		if len(results) != 1 {
			t.Fatalf("%d result messages, want one message carrying the whole group", len(results))
		}
		body := results[0].Content
		g1, g2 := strings.Index(body, `call_id="g1"`), strings.Index(body, `call_id="g2"`)
		if g1 < 0 || g2 < 0 {
			t.Fatalf("group message = %q, want both g1 and g2", body)
		}
		if n := strings.Count(body, "<steiner-sub-agent-result"); n != 2 {
			t.Fatalf("group message holds %d envelopes, want 2", n)
		}
	})

	t.Run("AsyncGroup rejects reused group while original children run", func(t *testing.T) {
		prov := newAsyncScript("task-alpha", "task-beta")
		prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
			step(toolCallsResponse(subAgentCall("g1", "task-alpha", "pair"), subAgentCall("g2", "task-beta", "pair"))),
			step(textResponse("accepted")),
			step(toolCallsResponse(subAgentCall("r1", "retry-alpha", "pair"), subAgentCall("r2", "retry-beta", "pair"))),
			func(req provider.ChatRequest) provider.ChatResponse {
				for _, callID := range []string{"r1", "r2"} {
					got := toolResultFor(req.Messages, callID)
					if !strings.Contains(got, "delegation group name") || !strings.Contains(got, "pair") || !strings.Contains(got, "already used") || !strings.Contains(got, "choose a fresh name") {
						t.Errorf("provider request %s result = %q, want corrective reason", callID, got)
					}
				}
				return textResponse("saw both")
			},
		}
		h := newAsyncHarness(t, prov, 4)

		submit(t, h.session, "dispatch the pair")
		waitRuns(t, h.session)
		recvStartedSet(t, prov, "task-alpha", "task-beta")
		events := h.events.snapshot()
		for _, callID := range []string{"g1", "g2"} {
			if acceptedAgentFor(events, callID) == "" {
				t.Fatalf("no real accepted event for %s", callID)
			}
		}
		before := h.supervisor.SnapshotGroupLedger(h.scope)
		if before.Version != 1 || !containsGroupName(before, "pair") {
			t.Fatalf("ledger before reuse = %+v, want reserved pair", before)
		}

		submit(t, h.session, "reuse the pair while it is still running")
		waitRuns(t, h.session)
		after := h.supervisor.SnapshotGroupLedger(h.scope)
		if !sameGroupLedger(before, after) {
			t.Fatalf("ledger changed after rejected reuse: before=%+v after=%+v", before, after)
		}
		for _, callID := range []string{"r1", "r2"} {
			if got := toolResultInConversation(h.session.Conversation(), callID); !strings.Contains(got, "pair") || !strings.Contains(got, "already used") || !strings.Contains(got, "choose a fresh name") {
				t.Fatalf("%s rejection = %q, want corrective model reason", callID, got)
			}
		}

		events = h.events.snapshot()
		for _, callID := range []string{"r1", "r2"} {
			finished := delegationFinishedFor(events, callID)
			if finished == nil {
				t.Fatalf("no finished event for rejected call %s", callID)
			}
			if finished.DelegationAdmission == nil || finished.DelegationAdmission.Status != "rejected" || finished.DelegationAdmission.Group != "pair" {
				t.Fatalf("rejected %s admission = %#v, want rejected pair metadata", callID, finished.DelegationAdmission)
			}
		}
		for _, callID := range []string{"r1", "r2"} {
			if hasDelegationAcceptedFor(events, callID) || hasDelegationStartedFor(events, callID) || hasDelegationQueuedFor(events, callID) {
				t.Fatalf("rejected call %s emitted accepted or child lifecycle event", callID)
			}
		}

		if n := len(subAgentResultMessages(h.session.Conversation())); n != 0 {
			t.Fatalf("%d result messages before original pair finishes, want 0", n)
		}
		prov.release("task-alpha")
		waitForDelegationComplete(t, h, acceptedAgentFor(events, "g1"))
		if n := len(subAgentResultMessages(h.session.Conversation())); n != 0 {
			t.Fatalf("%d result messages after first original child completed, want 0", n)
		}
		clearTurnNotifications(prov)
		prov.release("task-beta")
		awaitImmediateWake(t, h.session, prov, h.clock)

		results := subAgentResultMessages(h.session.Conversation())
		if len(results) != 1 {
			t.Fatalf("%d result messages after original pair finishes, want one", len(results))
		}
		body := results[0].Content
		for _, callID := range []string{"g1", "g2"} {
			if !strings.Contains(body, `call_id="`+callID+`"`) {
				t.Fatalf("delivered pair = %q, want %s envelope", body, callID)
			}
		}
		if strings.Contains(body, `call_id="r1"`) || strings.Contains(body, `call_id="r2"`) {
			t.Fatalf("delivered pair = %q, rejected calls must not be group members", body)
		}
	})

	t.Run("mixed fresh and resumed child share one live group", func(t *testing.T) {
		prov := newAsyncScript("warm", "fresh-mixed", "warm-followup")
		var warmAgentID string
		prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
			step(toolCallsResponse(subAgentCall("warm-call", "warm", ""))),
			step(textResponse("waiting for warm")),
			step(textResponse("saw warm")),
			func(req provider.ChatRequest) provider.ChatResponse {
				warmAgentID = agentIDPattern.FindString(toolResultFor(req.Messages, "warm-call"))
				return toolCallsResponse(
					subAgentCall("fresh-call", "fresh-mixed", "mixed"),
					provider.ToolCall{ID: "follow-call", Name: "follow_up", Arguments: map[string]any{
						"agent_id": warmAgentID, "message": "warm-followup", "group": "mixed",
					}},
				)
			},
			step(textResponse("waiting for mixed group")),
			step(textResponse("saw mixed group")),
		}
		h := newAsyncHarness(t, prov, 4)

		submit(t, h.session, "start warm child")
		recvStarted(t, prov, "warm")
		waitRuns(t, h.session)
		clearTurnNotifications(prov)
		prov.release("warm")
		awaitImmediateWake(t, h.session, prov, h.clock)

		events := h.events.snapshot()
		warmAdmission, ok := acceptedDelegationFor(events, "warm-call")
		if !ok {
			t.Fatal("warm child has no accepted admission")
		}
		warmAgentID = warmAdmission.AgentID
		if warmAdmission.BatchID == "" || warmAdmission.Group != "" {
			t.Fatalf("warm admission = %+v, want ungrouped accepted admission", warmAdmission)
		}
		if n := len(subAgentResultMessages(h.session.Conversation())); n != 1 {
			t.Fatalf("%d warm result messages, want one", n)
		}
		submit(t, h.session, "dispatch mixed warm follow-up and fresh child")
		waitRuns(t, h.session)

		recvStartedSet(t, prov, "fresh-mixed", "warm-followup")
		events = h.events.snapshot()
		freshAdmission, freshOK := acceptedDelegationFor(events, "fresh-call")
		followAdmission, followOK := acceptedDelegationFor(events, "follow-call")
		if !freshOK || !followOK {
			t.Fatalf("mixed accepted events: fresh=%+v follow=%+v", freshAdmission, followAdmission)
		}
		if freshAdmission.BatchID == "" || freshAdmission.BatchID != followAdmission.BatchID || freshAdmission.Group != "mixed" || followAdmission.Group != "mixed" || freshAdmission.AgentID == followAdmission.AgentID || followAdmission.AgentID != warmAgentID {
			t.Fatalf("mixed admissions = fresh=%+v follow=%+v, want one batch/group and current call identities", freshAdmission, followAdmission)
		}
		if freshAdmission.AgentID == warmAgentID {
			t.Fatal("fresh admission reused warm agent ID")
		}

		prov.release("fresh-mixed")
		waitForDelegationComplete(t, h, freshAdmission.AgentID)
		if n := len(subAgentResultMessages(h.session.Conversation())); n != 1 {
			t.Fatalf("%d result messages after fresh mixed child, want warm result only", n)
		}
		clearTurnNotifications(prov)
		prov.release("warm-followup")
		awaitImmediateWake(t, h.session, prov, h.clock)
		results := subAgentResultMessages(h.session.Conversation())
		if len(results) != 2 {
			t.Fatalf("%d result messages after mixed group, want warm and one joint mixed message", len(results))
		}
		body := results[1].Content
		if strings.Count(body, "<steiner-sub-agent-result") != 2 || !strings.Contains(body, `call_id="fresh-call"`) || !strings.Contains(body, `call_id="follow-call"`) {
			t.Fatalf("mixed delivery = %q, want one joint message with both current call IDs", body)
		}
	})

	t.Run("omitted follow-up group stays ungrouped", func(t *testing.T) {
		prov := newAsyncScript("warm", "warm-omitted")
		var warmAgentID string
		prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
			step(toolCallsResponse(subAgentCall("warm-call", "warm", ""))),
			step(textResponse("waiting for warm")),
			step(textResponse("saw warm")),
			func(req provider.ChatRequest) provider.ChatResponse {
				warmAgentID = agentIDPattern.FindString(toolResultFor(req.Messages, "warm-call"))
				return toolCallsResponse(provider.ToolCall{ID: "omitted-call", Name: "follow_up", Arguments: map[string]any{
					"agent_id": warmAgentID, "message": "warm-omitted",
				}})
			},
			step(textResponse("waiting for omitted follow-up")),
			step(textResponse("saw omitted follow-up")),
		}
		h := newAsyncHarness(t, prov, 2)

		submit(t, h.session, "start warm child")
		recvStarted(t, prov, "warm")
		waitRuns(t, h.session)
		clearTurnNotifications(prov)
		prov.release("warm")
		awaitImmediateWake(t, h.session, prov, h.clock)
		if n := len(subAgentResultMessages(h.session.Conversation())); n != 1 {
			t.Fatalf("%d warm result messages, want one", n)
		}
		submit(t, h.session, "dispatch omitted warm follow-up")
		waitRuns(t, h.session)
		recvStarted(t, prov, "warm-omitted")
		events := h.events.snapshot()
		warmAdmission, warmOK := acceptedDelegationFor(events, "warm-call")
		if !warmOK {
			t.Fatal("warm child has no accepted admission")
		}
		warmAgentID = warmAdmission.AgentID
		admission, ok := acceptedDelegationFor(events, "omitted-call")
		if !ok || admission.AgentID != warmAgentID || admission.Group != "" || admission.BatchID == "" {
			t.Fatalf("omitted follow-up admission = %+v, want accepted current ungrouped membership", admission)
		}
		prov.release("warm-omitted")
		h.events.waitFor(t, func(event output.Event) bool {
			count := 0
			for _, emitted := range h.events.snapshot() {
				if emitted.Type == output.EventTypeSubAgentsDelivered {
					count++
				}
			}
			return event.Type == output.EventTypeSubAgentsDelivered && count >= 2
		})
		waitRuns(t, h.session)
		results := subAgentResultMessages(h.session.Conversation())
		if len(results) != 2 || strings.Count(results[1].Content, "<steiner-sub-agent-result") != 1 || !strings.Contains(results[1].Content, `call_id="omitted-call"`) {
			t.Fatalf("omitted follow-up delivery = %+v, want one ungrouped result", results)
		}
	})

	t.Run("partial group admits fresh sibling without busy follow-up", func(t *testing.T) {
		prov := newAsyncScript("warm", "fresh-partial")
		var warmAgentID string
		prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
			step(toolCallsResponse(subAgentCall("warm-call", "warm", ""))),
			step(textResponse("waiting for warm")),
			func(req provider.ChatRequest) provider.ChatResponse {
				warmAgentID = agentIDPattern.FindString(toolResultFor(req.Messages, "warm-call"))
				return toolCallsResponse(
					subAgentCall("fresh-call", "fresh-partial", "partial"),
					provider.ToolCall{ID: "busy-follow-call", Name: "follow_up", Arguments: map[string]any{
						"agent_id": warmAgentID, "message": "busy follow-up", "group": "partial",
					}},
				)
			},
			step(textResponse("waiting for partial group")),
			step(textResponse("saw partial group")),
		}
		h := newAsyncHarness(t, prov, 4)

		submit(t, h.session, "start warm child")
		recvStarted(t, prov, "warm")
		waitRuns(t, h.session)
		submit(t, h.session, "dispatch partial group while warm is busy")
		waitRuns(t, h.session)
		recvStarted(t, prov, "fresh-partial")
		events := h.events.snapshot()
		freshAdmission, ok := acceptedDelegationFor(events, "fresh-call")
		if !ok || freshAdmission.Group != "partial" || freshAdmission.BatchID == "" {
			t.Fatalf("fresh partial admission = %+v", freshAdmission)
		}
		if hasDelegationAcceptedFor(events, "busy-follow-call") || hasDelegationStartedFor(events, "busy-follow-call") || hasDelegationQueuedFor(events, "busy-follow-call") {
			t.Fatal("busy follow-up emitted an accepted or child lifecycle event")
		}
		rejected := delegationFinishedFor(events, "busy-follow-call")
		if rejected == nil || rejected.DelegationAdmission == nil || rejected.DelegationAdmission.Status != "rejected" {
			t.Fatalf("busy follow-up rejection = %#v, want rejected current group admission", rejected)
		}
		if rejected.DelegationAdmission.AgentID != "" || rejected.DelegationAdmission.Group != "" || rejected.DelegationAdmission.BatchID == "" {
			t.Fatalf("busy follow-up rejection admission = %#v, want no admitted child but its batch identity", rejected.DelegationAdmission)
		}
		if got := toolResultInConversation(h.session.Conversation(), "busy-follow-call"); !strings.Contains(got, "still running") {
			t.Fatalf("busy follow-up result = %q, want model-visible busy rejection", got)
		}

		clearTurnNotifications(prov)
		prov.release("fresh-partial")
		waitForDelegationComplete(t, h, freshAdmission.AgentID)
		fireAndAwaitWake(t, h.session, prov, h.clock)
		if n := len(subAgentResultMessages(h.session.Conversation())); n != 1 {
			t.Fatalf("%d result messages after accepted fresh child, want one without rejected sibling", n)
		}
		prov.release("warm")
		awaitImmediateWake(t, h.session, prov, h.clock)
		results := subAgentResultMessages(h.session.Conversation())
		if len(results) != 2 {
			t.Fatalf("%d result messages after partial group, want fresh and warm", len(results))
		}
		body := results[0].Content
		if !strings.Contains(body, `call_id="fresh-call"`) || strings.Contains(body, `call_id="busy-follow-call"`) {
			t.Fatalf("partial delivery = %q, want accepted fresh member only", body)
		}
	})
}

func containsGroupName(ledger agent.DelegationGroupLedger, want string) bool {
	return slices.Contains(ledger.Names, want)
}

func sameGroupLedger(a, b agent.DelegationGroupLedger) bool {
	return a.Version == b.Version && slices.Equal(a.Names, b.Names)
}

func delegationFinishedFor(events []output.Event, callID string) *output.ToolCallFinishedEvent {
	for _, event := range events {
		if event.Type != output.EventTypeToolCallFinished {
			continue
		}
		payload, ok := event.Payload.(output.ToolCallFinishedEvent)
		if ok && payload.CallID == callID {
			return &payload
		}
	}
	return nil
}

func hasDelegationAcceptedFor(events []output.Event, callID string) bool {
	for _, event := range events {
		payload, ok := event.Payload.(output.DelegationAcceptedEvent)
		if event.Type == output.EventTypeDelegationAccepted && ok && payload.CallID == callID {
			return true
		}
	}
	return false
}

func hasDelegationStartedFor(events []output.Event, callID string) bool {
	for _, event := range events {
		payload, ok := event.Payload.(output.DelegationStartedEvent)
		if event.Type == output.EventTypeDelegationStarted && ok && payload.CallID == callID {
			return true
		}
	}
	return false
}

func hasDelegationQueuedFor(events []output.Event, callID string) bool {
	for _, event := range events {
		payload, ok := event.Payload.(output.DelegationQueuedEvent)
		if event.Type == output.EventTypeDelegationQueued && ok && payload.CallID == callID {
			return true
		}
	}
	return false
}
