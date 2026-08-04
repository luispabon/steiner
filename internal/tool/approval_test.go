package tool

import (
	"context"
	"errors"
	"testing"
)

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
