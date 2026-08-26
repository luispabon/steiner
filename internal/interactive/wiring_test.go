package interactive

import (
	"context"
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
	))

	snap, ok := snaps.Snapshot()
	if !ok {
		t.Fatal("expected snapshot after APIRequestEvent")
	}
	if snap.Model != "test-model" {
		t.Fatalf("snapshot model = %q, want %q", snap.Model, "test-model")
	}
}

func TestSnapshotSinkCapturesAgentAttribution(t *testing.T) {
	store := &SnapshotStore{}
	sink := &snapshotSink{store: store}
	event := output.WithAgentScope(output.NewAPIRequestEvent("model", nil, nil, nil, nil, prompt.ModelTokenBudget{}), "child-1")
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
	responder := newApprovalResponder(coordinator)

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
	responder := newApprovalResponder(coordinator)

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
	responder := newApprovalResponder(coordinator)

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
	responder := newApprovalResponder(coordinator)

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
