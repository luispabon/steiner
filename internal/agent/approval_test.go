package agent

import (
	"context"
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
		gotPreview = req.Preview
		req.Response <- tool.ApprovalResponse{Allow: true, Message: "ok"}
		return nil
	}))

	response := make(chan tool.ApprovalResponse, 1)
	err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Reason:   "sandbox_violation",
		Preview:  preview,
		Response: response,
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
