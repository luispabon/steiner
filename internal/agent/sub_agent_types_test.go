package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestNewToolBatchID(t *testing.T) {
	old := toolBatchNonce
	toolBatchNonce = "abcd1234"
	t.Cleanup(func() { toolBatchNonce = old })

	tests := []struct {
		name       string
		firstCall  string
		wantPrefix string
	}{
		{"repeated call id", "call_0", "call_0~abcd1234#"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]bool{}
			var prev uint64
			for range 5 {
				id := newToolBatchID(tt.firstCall)
				n, ok := ToolBatchSeq(id)
				if !ok {
					t.Fatalf("ToolBatchSeq(%q) did not round-trip", id)
				}
				if n <= prev {
					t.Fatalf("ToolBatchSeq(%q) = %d, want increasing after %d", id, n, prev)
				}
				prev = n
				if !strings.HasPrefix(id, tt.wantPrefix) || len(id) == len(tt.wantPrefix) {
					t.Fatalf("newToolBatchID(%q) = %q, want prefix %q plus sequence", tt.firstCall, id, tt.wantPrefix)
				}
				if seen[id] {
					t.Fatalf("newToolBatchID(%q) repeated %q", tt.firstCall, id)
				}
				seen[id] = true
			}
		})
	}
}

func TestNewToolBatchIDDiffersAcrossNonces(t *testing.T) {
	old := toolBatchNonce
	t.Cleanup(func() { toolBatchNonce = old })

	// Simulate two processes: each restarts the sequence at the same n.
	ids := map[string]string{}
	for _, nonce := range []string{"aaaa0000", "bbbb1111"} {
		toolBatchNonce = nonce
		toolBatchSeq.Store(2)
		ids[nonce] = newToolBatchID("call_0")
	}
	if ids["aaaa0000"] == ids["bbbb1111"] {
		t.Fatalf("ids under different nonces collide: %q", ids["aaaa0000"])
	}
}

func TestNewToolBatchNonce(t *testing.T) {
	n := newToolBatchNonce()
	if len(n) != 8 || strings.ContainsAny(n, toolBatchSeparator+toolBatchNonceJoiner) {
		t.Fatalf("newToolBatchNonce() = %q, want 8 hex chars", n)
	}
}
