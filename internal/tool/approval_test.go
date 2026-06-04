package tool

import (
	"context"
	"errors"
	"testing"
)

func TestApprovalRequestCarriesSandboxViolationFields(t *testing.T) {
	def := ToolDef{Name: "read"}
	responseCh := make(chan ApprovalResponse, 1)

	req := ApprovalRequest{
		Tool:              def,
		Input:             map[string]any{"path": "/etc/passwd"},
		WorkDir:           "/repo",
		DeniedPath:        "/etc/passwd",
		Reason:            "path is outside workspace boundary",
		GrantInstructions: "use --unsafe flag to disable sandboxing",
		Response:          responseCh,
	}

	if req.Tool.Name != "read" {
		t.Fatalf("Tool.Name = %q, want %q", req.Tool.Name, "read")
	}
	if req.DeniedPath != "/etc/passwd" {
		t.Fatalf("DeniedPath = %q, want %q", req.DeniedPath, "/etc/passwd")
	}
	if req.Reason != "path is outside workspace boundary" {
		t.Fatalf("Reason = %q, want unexpected", req.Reason)
	}
	if req.GrantInstructions != "use --unsafe flag to disable sandboxing" {
		t.Fatalf("GrantInstructions = %q, want unexpected", req.GrantInstructions)
	}
	if req.Response == nil {
		t.Fatal("Response channel is nil")
	}
}

func TestApprovalResponderFuncAdapter(t *testing.T) {
	var called bool
	var gotReq ApprovalRequest

	fn := ApprovalResponderFunc(func(_ context.Context, req ApprovalRequest) error {
		called = true
		gotReq = req
		req.Response <- ApprovalResponse{Allow: true}
		return nil
	})

	responseCh := make(chan ApprovalResponse, 1)
	req := ApprovalRequest{
		Tool:       ToolDef{Name: "bash"},
		DeniedPath: "",
		Reason:     "command was blocked by sandbox",
		Response:   responseCh,
	}

	if err := fn.RequestApproval(context.Background(), req); err != nil {
		t.Fatalf("RequestApproval() error = %v", err)
	}
	if !called {
		t.Fatal("responder func was not called")
	}
	if gotReq.Tool.Name != "bash" {
		t.Fatalf("got tool name = %q, want %q", gotReq.Tool.Name, "bash")
	}

	decision := <-responseCh
	if !decision.Allow {
		t.Fatal("expected Allow = true")
	}
}

func TestApprovalResponderFuncAdapterPropagatesError(t *testing.T) {
	wantErr := errors.New("transport unavailable")

	fn := ApprovalResponderFunc(func(_ context.Context, _ ApprovalRequest) error {
		return wantErr
	})

	responseCh := make(chan ApprovalResponse, 1)
	req := ApprovalRequest{
		Tool:     ToolDef{Name: "read"},
		Response: responseCh,
	}

	err := fn.RequestApproval(context.Background(), req)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
