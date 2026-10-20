package interactive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

func TestSessionSnapshotSinkCapturesAPIRequestEvent(t *testing.T) {
	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	snaps := s.SnapshotStore()

	_, ok := snaps.Snapshot()
	if ok {
		t.Fatal("expected no snapshot initially")
	}

	s.EventSink().Emit(output.NewAPIRequestEvent(
		"test-model",
		nil, nil, nil, nil,
		prompt.ModelTokenBudget{},
		42,
		84,
	))

	snap, ok := snaps.Snapshot()
	if !ok {
		t.Fatal("expected snapshot after APIRequestEvent")
	}
	if snap.Model != "test-model" {
		t.Fatalf("snapshot model = %q, want %q", snap.Model, "test-model")
	}
	if snap.EstimatedPromptTokens != 42 {
		t.Fatalf("snapshot estimated prompt tokens = %d, want 42", snap.EstimatedPromptTokens)
	}
	if snap.RawPromptTokens != 84 {
		t.Fatalf("snapshot raw prompt tokens = %d, want 84", snap.RawPromptTokens)
	}
}

func TestSnapshotSinkCapturesAgentAttribution(t *testing.T) {
	store := &SnapshotStore{}
	sink := &snapshotSink{store: store}
	event := output.WithAgentScope(output.NewAPIRequestEvent("model", nil, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0), "child-1")
	event = output.WithAgentTypeScope(event, "code")
	event = output.WithAPIRequestKind(event, output.APIRequestKindCompaction)
	sink.Emit(event)

	snapshot, ok := store.Snapshot()
	if !ok {
		t.Fatal("expected snapshot")
	}
	if snapshot.AgentID != "child-1" || snapshot.AgentType != "code" || snapshot.Kind != output.APIRequestKindCompaction {
		t.Fatalf("snapshot attribution = (%q, %q, %q), want (child-1, code, %s)", snapshot.AgentID, snapshot.AgentType, snapshot.Kind, output.APIRequestKindCompaction)
	}
}

func TestSessionSnapshotSinkIgnoresNonAPIRequestEvents(t *testing.T) {
	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	snaps := s.SnapshotStore()

	s.EventSink().Emit(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))

	_, ok := snaps.Snapshot()
	if ok {
		t.Fatal("expected no snapshot after non-API-request event")
	}
}

func TestSessionApproverReturnsNonNil(t *testing.T) {
	var events []output.Event
	sink := output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	})

	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	responder := s.Approver(sink)

	if responder == nil {
		t.Fatal("Approver() returned nil")
	}
}

func TestApprovalResponderAllowsAndCachesAlwaysAllow(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	firstResponse := make(chan tool.ApprovalResponse, 1)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			CallID:   "call-write-1",
			Reason:   "sandbox_violation",
			Response: firstResponse,
			Kind:     tool.ApprovalKindPath,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.Submit(SubmitApproval{
		Identity: coordinator.HeadIdentity(),
		Tool:     "write",
		Mode:     "prompt",
		Decision: "always_allow",
	})

	if err := <-firstDone; err != nil {
		t.Fatalf("first RequestApproval() error = %v", err)
	}
	if got, want := <-firstResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("first response = %#v, want %#v", got, want)
	}

	secondResponse := make(chan tool.ApprovalResponse, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			CallID:   "call-write-2",
			Tool:     tool.ToolDef{Name: "write"},
			Reason:   "sandbox_violation",
			Response: secondResponse,
			Kind:     tool.ApprovalKindPath,
		})
	}()

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second RequestApproval() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second RequestApproval() blocked, want cached always-allow response")
	}

	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("second response = %#v, want %#v", got, want)
	}
}

func TestApprovalResponderDeniesDecisions(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			CallID:   "call-bash-1",
			Tool:     tool.ToolDef{Name: "bash"},
			Reason:   "sandbox_violation",
			Response: responseCh,
			Kind:     tool.ApprovalKindPath,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.Submit(SubmitApproval{
		Identity: coordinator.HeadIdentity(),
		Tool:     "bash",
		Mode:     "prompt",
		Decision: "deny",
	})

	if err := <-done; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestApprovalResponderDoesNotDependOnTerminalHandoff(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "edit"},
			CallID:   "call-edit-1",
			Reason:   "sandbox_violation",
			Response: responseCh,
			Kind:     tool.ApprovalKindPath,
		})
	}()
	waitForPendingApproval(t, coordinator)

	coordinator.Submit(SubmitApproval{
		Identity: coordinator.HeadIdentity(),
		Tool:     "edit",
		Mode:     "prompt",
		Decision: "allow_once",
	})

	if err := <-done; err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: true, Message: "approved"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

// TestApprovalResponderCachesMCPToolsByServerAndTool proves MCP tools are
// cached by the server+tool grant key: an always_allow decision on one MCP
// tool short-circuits the next request for the same server+tool, but a
// different tool from the same server still prompts.
func TestApprovalResponderCachesMCPToolsByServerAndTool(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	responder := newApprovalResponder(coordinator, nil)

	mcpTool := tool.ToolDef{
		Name: "mcp__fixture__echo",
		MCP:  tool.MCPProvenance{Server: "fixture", ToolName: "echo"},
	}
	otherTool := tool.ToolDef{
		Name: "mcp__fixture__other",
		MCP:  tool.MCPProvenance{Server: "fixture", ToolName: "other"},
	}

	request := func(td tool.ToolDef, server, toolName string) (chan tool.ApprovalResponse, chan error) {
		responseCh := make(chan tool.ApprovalResponse, 1)
		done := make(chan error, 1)
		go func() {
			done <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
				Tool:     td,
				Reason:   "MCP tool call",
				CallID:   toolName,
				Response: responseCh,
				Kind:     tool.ApprovalKindMCP,
				MCP: &tool.MCPApprovalDetails{
					Server:   server,
					ToolName: toolName,
				},
			})
		}()
		return responseCh, done
	}

	firstResponse, firstDone := request(mcpTool, "fixture", "echo")
	waitForPendingApproval(t, coordinator)
	coordinator.Submit(SubmitApproval{
		Identity: coordinator.HeadIdentity(),
		Tool:     mcpTool.Name,
		Mode:     "prompt",
		Decision: "always_allow",
	})
	if err := <-firstDone; err != nil {
		t.Fatalf("first RequestApproval() error = %v", err)
	}
	if got, want := <-firstResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("first response = %#v, want %#v", got, want)

	}
	// The second request for the same server+tool is served from the cache.
	secondResponse, secondDone := request(mcpTool, "fixture", "echo")
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second RequestApproval() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second RequestApproval() blocked, want cached always-allow response")
	}
	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("second response = %#v, want %#v", got, want)
	}

	// A different tool from the same server still prompts.
	thirdResponse, thirdDone := request(otherTool, "fixture", "other")
	select {
	case err := <-thirdDone:
		t.Fatalf("third RequestApproval() returned early (err = %v), want a fresh approval prompt", err)
	case <-time.After(100 * time.Millisecond):
	}

	waitForPendingApproval(t, coordinator)
	coordinator.Submit(SubmitApproval{
		Identity: coordinator.HeadIdentity(),
		Tool:     otherTool.Name,
		Mode:     "prompt",
		Decision: "deny",
	})
	if err := <-thirdDone; err != nil {
		t.Fatalf("third RequestApproval() error = %v", err)
	}
	if got, want := <-thirdResponse, (tool.ApprovalResponse{Allow: false, Message: "denied"}); got != want {
		t.Fatalf("third response = %#v, want %#v", got, want)
	}
}

// TestSessionApproverRegistersBeforeRequestedEvent proves a client can answer
// an approval synchronously from the requested-event sink. Before the fix the
// response was submitted before registration, so the request blocked forever.
// Each case covers one user decision for a single request: the requested event
// sees the pending registration, the decision event is observed only after the
// registration is gone, and the coordinator is empty afterward.
func TestSessionApproverRegistersBeforeRequestedEvent(t *testing.T) {
	type observedEvent struct {
		eventType string
		pending   bool
		head      string
	}
	tests := []struct {
		name         string
		decision     string
		wantEvent    string
		wantResponse tool.ApprovalResponse
	}{
		{
			name:         "allow_once",
			decision:     "allow_once",
			wantEvent:    string(output.EventTypeApprovalAccepted),
			wantResponse: tool.ApprovalResponse{Allow: true, Message: "approved"},
		},
		{
			name:         "deny",
			decision:     "deny",
			wantEvent:    string(output.EventTypeApprovalDenied),
			wantResponse: tool.ApprovalResponse{Allow: false, Message: "denied"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewSession(Dependencies{})
			if err != nil {
				t.Fatalf("NewSession failed: %v", err)
			}
			coordinator := s.ApprovalCoordinator()

			// Buffered for the two events a single request emits. The decision event
			// is sent from the eventing goroutine, so this channel is the sync point.
			observed := make(chan observedEvent, 2)
			responder := s.Approver(output.SinkFunc(func(event output.Event) {
				switch event.Type {
				case output.EventTypeApprovalRequested:
					observed <- observedEvent{
						eventType: event.Type,
						pending:   coordinator.HasPending(),
						head:      coordinator.HeadIdentity(),
					}
					coordinator.Submit(SubmitApproval{
						Identity: coordinator.HeadIdentity(),
						Tool:     "bash",
						Mode:     "prompt",
						Decision: tt.decision,
					})
				case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
					observed <- observedEvent{
						eventType: event.Type,
						pending:   coordinator.HasPending(),
					}
				}
			}))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			responseCh := make(chan tool.ApprovalResponse, 1)
			done := make(chan error, 1)
			go func() {
				done <- responder.RequestApproval(ctx, tool.ApprovalRequest{
					Tool:     tool.ToolDef{Name: "bash"},
					CallID:   "call-bash-sync",
					Reason:   "sandbox_violation",
					Response: responseCh,
					Kind:     tool.ApprovalKindPath,
				})
			}()

			next := func() observedEvent {
				select {
				case ev := <-observed:
					return ev
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for approval event")
					return observedEvent{}
				}
			}

			requested := next()
			if requested.eventType != string(output.EventTypeApprovalRequested) {
				t.Fatalf("first event = %q, want %q", requested.eventType, output.EventTypeApprovalRequested)
			}
			if !requested.pending || requested.head != "call-bash-sync" {
				t.Fatalf("at requested event pending = %t, head = %q; want pending call-bash-sync", requested.pending, requested.head)
			}

			decision := next()
			if decision.eventType != tt.wantEvent {
				t.Fatalf("second event = %q, want %q", decision.eventType, tt.wantEvent)
			}
			if decision.pending {
				t.Fatalf("approval still registered when %s was observed", decision.eventType)
			}

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("RequestApproval() error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("RequestApproval() blocked; synchronous requested-event response was lost")
			}
			if coordinator.HasPending() {
				t.Fatal("coordinator still has a pending approval after the decision")
			}
			if got := <-responseCh; got != tt.wantResponse {
				t.Fatalf("response = %#v, want %#v", got, tt.wantResponse)
			}
		})
	}
}

// TestSessionApproverRemovesPendingBeforeDeniedEvent proves a cancelled
// approval is unregistered before its denied event reaches the sink.
func TestSessionApproverRemovesPendingBeforeDeniedEvent(t *testing.T) {
	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	coordinator := s.ApprovalCoordinator()

	pendingAtDenied := make(chan bool, 1)
	responder := s.Approver(output.SinkFunc(func(event output.Event) {
		if event.Type == output.EventTypeApprovalDenied {
			pendingAtDenied <- coordinator.HasPending()
		}
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	responseCh := make(chan tool.ApprovalResponse, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RequestApproval(ctx, tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "bash"},
			CallID:   "call-bash-cancel",
			Reason:   "sandbox_violation",
			Response: responseCh,
			Kind:     tool.ApprovalKindPath,
		})
	}()
	waitForPendingApproval(t, coordinator)

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RequestApproval() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval() did not return after cancellation")
	}
	if pending := <-pendingAtDenied; pending {
		t.Fatal("approval still registered when denied event was emitted")
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: false, Message: context.Canceled.Error()}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

// TestApprovalResponderNilCoordinatorEmitsRequestedThenDenied keeps the
// unavailable-coordinator decision and its event pair unchanged.
func TestApprovalResponderNilCoordinatorEmitsRequestedThenDenied(t *testing.T) {
	var events []output.Event
	responder := newApprovalResponder(nil, output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}))

	responseCh := make(chan tool.ApprovalResponse, 1)
	err := responder.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		CallID:   "call-bash-nil",
		Reason:   "sandbox_violation",
		Response: responseCh,
		Kind:     tool.ApprovalKindPath,
	})
	if err == nil || err.Error() != "approval UI is unavailable" {
		t.Fatalf("RequestApproval() error = %v, want approval UI is unavailable", err)
	}
	if got, want := <-responseCh, (tool.ApprovalResponse{Allow: false, Message: "approval UI is unavailable"}); got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
	if len(events) != 2 || events[0].Type != output.EventTypeApprovalRequested || events[1].Type != output.EventTypeApprovalDenied {
		t.Fatalf("events = %#v, want requested then denied approval events", events)
	}
}

// TestApprovalResponderCachedGrantSkipsRegistration proves a cached always_allow
// emits its requested event without registering a pending approval.
func TestApprovalResponderCachedGrantSkipsRegistration(t *testing.T) {
	coordinator := &ApprovalCoordinator{}
	var pendingAtRequested []bool
	responder := newApprovalResponder(coordinator, output.SinkFunc(func(event output.Event) {
		if event.Type == output.EventTypeApprovalRequested {
			pendingAtRequested = append(pendingAtRequested, coordinator.HasPending())
		}
	}))

	firstResponse := make(chan tool.ApprovalResponse, 1)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- responder.RequestApproval(context.Background(), tool.ApprovalRequest{
			Tool:     tool.ToolDef{Name: "write"},
			CallID:   "call-write-1",
			Reason:   "sandbox_violation",
			Response: firstResponse,
			Kind:     tool.ApprovalKindPath,
		})
	}()
	waitForPendingApproval(t, coordinator)
	coordinator.Submit(SubmitApproval{
		Identity: coordinator.HeadIdentity(),
		Tool:     "write",
		Mode:     "prompt",
		Decision: "always_allow",
	})
	if err := <-firstDone; err != nil {
		t.Fatalf("first RequestApproval() error = %v", err)
	}

	secondResponse := make(chan tool.ApprovalResponse, 1)
	if err := responder.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "write"},
		CallID:   "call-write-2",
		Reason:   "sandbox_violation",
		Response: secondResponse,
		Kind:     tool.ApprovalKindPath,
	}); err != nil {
		t.Fatalf("second RequestApproval() error = %v", err)
	}
	if got, want := <-secondResponse, (tool.ApprovalResponse{Allow: true, Message: "always allowed"}); got != want {
		t.Fatalf("second response = %#v, want %#v", got, want)
	}

	if len(pendingAtRequested) != 2 {
		t.Fatalf("requested events = %d, want 2", len(pendingAtRequested))
	}
	if !pendingAtRequested[0] {
		t.Error("first requested event saw no pending approval, want one registered")
	}
	if pendingAtRequested[1] {
		t.Error("cached grant requested event saw a pending approval, want none registered")
	}
}

func TestApprovalResponseForDecision(t *testing.T) {
	tests := []struct {
		decision string
		want     tool.ApprovalResponse
	}{
		{"always_allow", tool.ApprovalResponse{Allow: true, Message: "always allowed"}},
		{"allow_once", tool.ApprovalResponse{Allow: true, Message: "approved"}},
		{"deny", tool.ApprovalResponse{Allow: false, Message: "denied"}},
		{"unknown", tool.ApprovalResponse{Allow: false, Message: "denied"}},
	}
	for _, tt := range tests {
		got := approvalResponseForDecision(tt.decision)
		if got != tt.want {
			t.Errorf("approvalResponseForDecision(%q) = %#v, want %#v", tt.decision, got, tt.want)
		}
	}
}

func TestWorkflowHandoffResponderAcceptsDecision(t *testing.T) {
	coordinator := &WorkflowHandoffCoordinator{}
	var events []output.Event
	responder := newWorkflowHandoffResponder(coordinator, output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}), nil)

	done := make(chan struct {
		response tool.WorkflowHandoffResponse
		err      error
	}, 1)
	go func() {
		response, err := responder.RequestWorkflowHandoff(context.Background(), tool.WorkflowHandoffRequest{
			Next:   "implement",
			Target: ".steiner/plans/step-2",
		})
		done <- struct {
			response tool.WorkflowHandoffResponse
			err      error
		}{response: response, err: err}
	}()
	waitForPendingWorkflowHandoff(t, coordinator)

	coordinator.Submit(SubmitWorkflowHandoff{Decision: "accept"})

	got := <-done
	if got.err != nil {
		t.Fatalf("RequestWorkflowHandoff() error = %v", got.err)
	}
	if !got.response.Accepted {
		t.Fatal("Accepted = false, want true")
	}
	if len(events) != 2 || events[0].Type != output.EventTypeWorkflowHandoffRequested || events[1].Type != output.EventTypeWorkflowHandoffAccepted {
		t.Fatalf("events = %#v, want requested then accepted handoff events", events)
	}
}

func TestWorkflowHandoffResponderRegistersPendingBeforeRequestedEvent(t *testing.T) {
	coordinator := &WorkflowHandoffCoordinator{}
	var events []output.Event
	pendingObserved := make(chan bool, 1)
	responder := newWorkflowHandoffResponder(coordinator, output.SinkFunc(func(event output.Event) {
		events = append(events, event)
		if event.Type == output.EventTypeWorkflowHandoffRequested {
			pendingObserved <- coordinator.HasPending()
			coordinator.Submit(SubmitWorkflowHandoff{Decision: "accept"})
		}
	}), nil)

	done := make(chan struct {
		response tool.WorkflowHandoffResponse
		err      error
	}, 1)
	go func() {
		response, err := responder.RequestWorkflowHandoff(context.Background(), tool.WorkflowHandoffRequest{
			Next:    "implement",
			Target:  ".steiner/plans/step-2",
			Message: "ready",
		})
		done <- struct {
			response tool.WorkflowHandoffResponse
			err      error
		}{response: response, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RequestWorkflowHandoff() error = %v", got.err)
		}
		if !got.response.Accepted {
			t.Fatal("Accepted = false, want true")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("RequestWorkflowHandoff() timed out after synchronous requested-event submission")
	}
	if ok := <-pendingObserved; !ok {
		t.Fatal("workflow handoff request not registered before requested event emission")
	}

	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Type != output.EventTypeWorkflowHandoffRequested {
		t.Fatalf("events[0].Type = %q, want %q", events[0].Type, output.EventTypeWorkflowHandoffRequested)
	}
	if events[1].Type != output.EventTypeWorkflowHandoffAccepted {
		t.Fatalf("events[1].Type = %q, want %q", events[1].Type, output.EventTypeWorkflowHandoffAccepted)
	}
}

func TestWorkflowHandoffResponderDeclinesDecision(t *testing.T) {
	coordinator := &WorkflowHandoffCoordinator{}
	var events []output.Event
	responder := newWorkflowHandoffResponder(coordinator, output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}), nil)

	done := make(chan struct {
		response tool.WorkflowHandoffResponse
		err      error
	}, 1)
	go func() {
		response, err := responder.RequestWorkflowHandoff(context.Background(), tool.WorkflowHandoffRequest{
			Next:   "review",
			Target: ".steiner/plans/step-2",
		})
		done <- struct {
			response tool.WorkflowHandoffResponse
			err      error
		}{response: response, err: err}
	}()
	waitForPendingWorkflowHandoff(t, coordinator)

	coordinator.Submit(SubmitWorkflowHandoff{Decision: "dismiss"})

	got := <-done
	if got.err != nil {
		t.Fatalf("RequestWorkflowHandoff() error = %v", got.err)
	}
	if got.response.Accepted {
		t.Fatal("Accepted = true, want false")
	}
	if len(events) != 2 || events[0].Type != output.EventTypeWorkflowHandoffRequested || events[1].Type != output.EventTypeWorkflowHandoffDeclined {
		t.Fatalf("events = %#v, want requested then declined handoff events", events)
	}
}

func TestWorkflowHandoffResponderAcceptanceFlipsSessionModeToBuild(t *testing.T) {
	coordinator := &WorkflowHandoffCoordinator{}
	var events []output.Event

	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	}
	s, err := NewSession(deps)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if got, want := s.Mode(), config.ExecutionModePlan; got != want {
		t.Fatalf("initial Mode() = %q, want %q", got, want)
	}

	responder := newWorkflowHandoffResponder(coordinator, output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}), s)

	done := make(chan struct {
		response tool.WorkflowHandoffResponse
		err      error
	}, 1)
	go func() {
		response, err := responder.RequestWorkflowHandoff(context.Background(), tool.WorkflowHandoffRequest{
			Next:   "implement",
			Target: ".steiner/plans/step-2",
		})
		done <- struct {
			response tool.WorkflowHandoffResponse
			err      error
		}{response: response, err: err}
	}()
	waitForPendingWorkflowHandoff(t, coordinator)

	coordinator.Submit(SubmitWorkflowHandoff{Decision: "accept"})

	got := <-done
	if got.err != nil {
		t.Fatalf("RequestWorkflowHandoff() error = %v", got.err)
	}
	if !got.response.Accepted {
		t.Fatal("Accepted = false, want true")
	}

	if mode := s.Mode(); mode != config.ExecutionModeBuild {
		t.Fatalf("Mode() after acceptance = %q, want %q", mode, config.ExecutionModeBuild)
	}
}

func waitForPendingApproval(t *testing.T, coordinator *ApprovalCoordinator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if coordinator.HasPending() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("approval request was not registered")
}

func waitForPendingWorkflowHandoff(t *testing.T, coordinator *WorkflowHandoffCoordinator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if coordinator.HasPending() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workflow handoff request was not registered")
}
