package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func TestEventingApproverForwardsPreviewToInnerApprover(t *testing.T) {
	preview := tool.ApprovalPreview{
		Tool:    "bash",
		WorkDir: "/repo",
		Timeout: 3 * time.Second,
		Fields: []tool.PreviewField{
			{Name: "cwd", Value: "/repo/subdir"},
			{Name: "command", Value: "pwd"},
		},
	}

	var gotPreview tool.ApprovalPreview
	approver := NewEventingApprover(output.NoopSink{}, tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
		gotPreview = req.Path.Preview
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "ok"}
		return nil
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Reason:   "sandbox_violation",
		Response: response,
		Kind:     tool.ApprovalKindPath,
		Path: &tool.PathApprovalDetails{
			Preview: preview,
		},
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if !resp.Allow {
		t.Fatal("approval response = false, want true")
	}
	if got, want := gotPreview.Summary(), preview.Summary(); got != want {
		t.Fatalf("forwarded preview summary = %q, want %q", got, want)
	}
}

func TestEventingApproverMCPRequestUsesArgumentsPreview(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]any
		mcpPreview  string
		wantPreview string
	}{
		{
			name:        "formatted arguments preview preferred for normal input",
			input:       map[string]any{"message": "hi"},
			mcpPreview:  "message: hi",
			wantPreview: "message: hi",
		},
		{
			name:        "empty arguments preview falls back to compacted input",
			input:       map[string]any{"message": "hi"},
			wantPreview: `{"message":"hi"}`,
		},
		{
			name:        "unmarshalable input falls back to arguments preview",
			input:       map[string]any{"bad": func() {}},
			mcpPreview:  "message: hi",
			wantPreview: "message: hi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var events []output.Event
			approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
				req.Response <- tool.ApprovalResponse{Allow: true, Message: "ok"}
				return nil
			}))

			response := make(chan tool.ApprovalResponse, 1)
			err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: "mcp__fixture__echo"},
				Reason:   "MCP tool call",
				Input:    tt.input,
				Response: response,
				Kind:     tool.ApprovalKindMCP,
				MCP: &tool.MCPApprovalDetails{
					Server:           "fixture",
					ToolName:         "echo",
					ArgumentsPreview: tt.mcpPreview,
				},
			})
			if err != nil {
				t.Fatalf("RequestApproval() error = %v", err)
			}
			resp := <-response
			if !resp.Allow {
				t.Fatal("approval response = false, want true")
			}
			if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalAccepted}; !equalStrings(got, want) {
				t.Fatalf("event types = %v, want %v", got, want)
			}
			requested, ok := events[0].Payload.(output.ApprovalEvent)
			if !ok {
				t.Fatalf("requested payload type = %T, want ApprovalEvent", events[0].Payload)
			}
			if got, want := requested.Preview, tt.wantPreview; got != want {
				t.Fatalf("requested preview = %q, want %q", got, want)
			}
			if got, want := requested.Kind, string(tool.ApprovalKindMCP); got != want {
				t.Fatalf("requested kind = %q, want %q", got, want)
			}
			if got, want := requested.Server, "fixture"; got != want {
				t.Fatalf("requested server = %q, want %q", got, want)
			}
			if got, want := requested.ToolName, "echo"; got != want {
				t.Fatalf("requested tool name = %q, want %q", got, want)
			}
		})
	}
}

func TestEventingApproverEmitsLifecycleEvents(t *testing.T) {
	var events []output.Event
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "ok"}
		return nil
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "write"},
		Reason:   "sandbox_violation",
		Response: response,
		Kind:     tool.ApprovalKindPath,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if !resp.Allow {
		t.Fatal("approval response = false, want true")
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalAccepted}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

func TestEventingApproverNilInnerDeniesWithoutPanic(t *testing.T) {
	var events []output.Event
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), nil)

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Reason:   "sandbox_violation",
		Response: response,
		Kind:     tool.ApprovalKindPath,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if resp.Allow {
		t.Fatal("approval response = true, want false")
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalDenied}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

func TestEventingApproverInnerErrorPropagatesAndEmitsDenial(t *testing.T) {
	var events []output.Event
	innerErr := fmt.Errorf("inner boom")
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApprovalResponderFunc(func(_ context.Context, _ tool.ApprovalRequest) error {
		return innerErr
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Reason:   "sandbox_violation",
		Response: response,
		Kind:     tool.ApprovalKindPath,
	})
	if err == nil || err.Error() != innerErr.Error() {
		t.Fatalf("RequestApproval() error = %v, want %v", err, innerErr)
	}
	resp := <-response
	if resp.Allow {
		t.Fatal("approval response = true, want false")
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalDenied}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

func TestEventingApproverAllowFalseEmitsDenialNotAcceptance(t *testing.T) {
	var events []output.Event
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
		req.Response <- tool.ApprovalResponse{Allow: false, Message: "denied by user"}
		return nil
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Reason:   "sandbox_violation",
		Response: response,
		Kind:     tool.ApprovalKindPath,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if resp.Allow {
		t.Fatal("approval response = true, want false")
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalDenied}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for _, event := range events {
		if event.Type == output.EventTypeApprovalAccepted {
			t.Fatalf("denial emitted an approval-shaped event: %+v", event)
		}
	}
}

func TestEventingApproverContextCancelledDeniesWithoutHanging(t *testing.T) {
	var events []output.Event
	approver := NewEventingApprover(output.SinkFunc(func(event output.Event) { events = append(events, event) }), tool.ApprovalResponderFunc(func(_ context.Context, _ tool.ApprovalRequest) error {
		return nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(ctx, tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Reason:   "sandbox_violation",
		Response: response,
		Kind:     tool.ApprovalKindPath,
	})
	if err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	resp := <-response
	if resp.Allow {
		t.Fatal("approval response = true, want false")
	}
	if resp.Message != ctx.Err().Error() {
		t.Fatalf("response message = %q, want %q", resp.Message, ctx.Err().Error())
	}
	if got, want := eventTypes(events), []string{output.EventTypeApprovalRequested, output.EventTypeApprovalDenied}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}
