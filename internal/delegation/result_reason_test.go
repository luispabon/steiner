package delegation

import "testing"

func TestProjectToolResultReason(t *testing.T) {
	t.Parallel()

	t.Run("failed result projects the failure explanation", func(t *testing.T) {
		t.Parallel()
		envelope := Result{Status: StatusFailed, Reason: "delegation failed: child exploded"}.ProjectToolResult()
		if envelope.Status != "failed" {
			t.Fatalf("Status = %q, want failed", envelope.Status)
		}
		if envelope.Reason != "delegation failed: child exploded" {
			t.Errorf("Reason = %q, want the failure explanation", envelope.Reason)
		}
	})

	t.Run("failed result without a reason falls back to unknown failure", func(t *testing.T) {
		t.Parallel()
		envelope := Result{Status: StatusFailed}.ProjectToolResult()
		if envelope.Reason != "unknown failure" {
			t.Errorf("Reason = %q, want %q", envelope.Reason, "unknown failure")
		}
	})

	t.Run("cancelled result projects its reason", func(t *testing.T) {
		t.Parallel()
		reason := "cancelled after 2 turns, 1 tool call(s); the child session is preserved and can be resumed with follow_up"
		envelope := Result{Status: StatusCancelled, Reason: reason}.ProjectToolResult()
		if envelope.Reason != reason {
			t.Errorf("Reason = %q, want %q", envelope.Reason, reason)
		}
	})

	t.Run("cancelled result without a reason keeps the projection token", func(t *testing.T) {
		t.Parallel()
		envelope := Result{Status: StatusCancelled, StopReason: "limit reached"}.ProjectToolResult()
		if envelope.Reason != "limit reached" {
			t.Errorf("Reason = %q, want limit reached", envelope.Reason)
		}
	})

	t.Run("complete result projects an empty reason", func(t *testing.T) {
		t.Parallel()
		envelope := Result{Status: StatusComplete, Output: "done"}.ProjectToolResult()
		if envelope.Status != "" {
			t.Errorf("Status = %q, want omitted for a complete result", envelope.Status)
		}
		if envelope.Reason != "" {
			t.Errorf("Reason = %q, want empty for a complete result", envelope.Reason)
		}
	})
}
