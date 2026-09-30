package main

import (
	"context"
	"fmt"
	"regexp"
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
	var text strings.Builder
	for _, m := range req.Messages {
		text.WriteString(m.Content)
	}
	for objective, gate := range p.gates {
		if !strings.Contains(text.String(), objective) {
			continue
		}
		p.started <- objective
		select {
		case <-gate:
		case <-ctx.Done():
			return provider.ChatResponse{}, ctx.Err()
		}
		return textResponse("finished " + objective), nil
	}
	return provider.ChatResponse{}, fmt.Errorf("unscripted child request")
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
func newAsyncSession(t *testing.T, prov provider.Provider, maxParallel int) (*interactive.Session, *asyncClock) {
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
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: maxParallel, Controller: controller})
	workDir := t.TempDir()
	rt := cliRuntime{
		cfg:                        cfg,
		provider:                   prov,
		registry:                   runtimeRegistryWithSinkAndMode(cfg, workDir, nil, true, nil, nil, nil, nil, withPendingSubAgents(sup)),
		workDir:                    workDir,
		homeDir:                    t.TempDir(),
		events:                     output.NoopSink{},
		delegationSessionStore:     delegation.NewSessionStore(),
		delegationCacheKeyStore:    delegation.NewCacheKeyStore(),
		delegationActiveController: controller,
		delegationSupervisor:       sup,
	}
	clock := newAsyncClock()
	sess, err := interactive.NewSession(interactive.Dependencies{
		Config:            cfg,
		WorkDir:           workDir,
		HomeDir:           rt.homeDir,
		Background:        sup,
		SetCompletionSink: sup.SetCompletionSink,
		Clock:             clock,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sup.CancelAll(delegation.CancelCauseUser)
		sess.Close(ctx)
	})
	sess.SetRunner(sessionRunner{runner: cliRunner{runtime: rt, runMode: "interactive", sessionIDFn: sess.SessionID, modeGetterFunc: sess.Mode}})
	return sess, clock
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

func TestAsyncSubAgentsThroughInteractiveWiring(t *testing.T) {
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
}
