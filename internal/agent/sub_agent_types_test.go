package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestToolBatchIDRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"unset", context.Background(), ""},
		{"stamped", WithToolBatchID(context.Background(), "b1"), "b1"},
		{"restamped", WithToolBatchID(WithToolBatchID(context.Background(), "b1"), "b2"), "b2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToolBatchIDFrom(tt.ctx); got != tt.want {
				t.Fatalf("ToolBatchIDFrom = %q, want %q", got, tt.want)
			}
		})
	}
}

type projectedValue struct{}

func (projectedValue) ProjectToolResult() DelegationResultEnvelope {
	return DelegationResultEnvelope{Output: "done"}
}

type projectedErr struct{}

func (projectedErr) Error() string { return "boom" }
func (projectedErr) ProjectToolError() DelegationResultEnvelope {
	return DelegationResultEnvelope{Output: "projected", Status: "failed"}
}

func TestProjectedBodies(t *testing.T) {
	if body, ok := ProjectedToolResult(projectedValue{}); !ok || body != `{"output":"done"}` {
		t.Fatalf("ProjectedToolResult = %q, %v", body, ok)
	}
	if _, ok := ProjectedToolResult("plain"); ok {
		t.Fatal("ProjectedToolResult accepted a non-projector")
	}
	if body, ok := ProjectedToolError(projectedErr{}); !ok || body != `{"output":"projected","status":"failed"}` {
		t.Fatalf("ProjectedToolError = %q, %v", body, ok)
	}
	if _, ok := ProjectedToolError(errors.New("plain")); ok {
		t.Fatal("ProjectedToolError accepted a non-projector")
	}

	var env DelegationResultEnvelope
	if err := json.Unmarshal([]byte(FailureBody("cancelled", "stopped")), &env); err != nil {
		t.Fatalf("FailureBody is not JSON: %v", err)
	}
	if env.Status != "cancelled" || env.Output != "stopped" || env.Reason != "stopped" {
		t.Fatalf("FailureBody envelope = %+v", env)
	}
}
