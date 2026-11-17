package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// sandboxRetryApproval drives the model into a pending one-time sandbox retry
// approval: a bash call whose approval request is marked NoPersist.
func sandboxRetryApproval(t *testing.T, m *Model) *Model {
	t.Helper()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call-bash", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Turn:      1,
		Tool:      "bash",
		CallID:    "call-bash",
		RequestID: "req-retry",
		Mode:      "command blocked by sandbox",
		Preview:   `{"command":"cat /host/path"}`,
		Kind:      "path",
		NoPersist: true,
	})})
	if !m.approval.active {
		t.Fatal("approval.active = false, want a pending retry approval")
	}
	if !m.approval.noPersist {
		t.Fatal("approval.noPersist = false, want true for a retry approval")
	}
	return m
}

func TestModelApprovalNoPersistHidesSessionGrant(t *testing.T) {
	t.Parallel()
	m := sandboxRetryApproval(t, newModel(Config{}, nil))

	rendered := m.content.String(m.viewport.Width())
	if strings.Contains(rendered, "Allowed for session") {
		t.Fatalf("content = %q, want no session-wide grant for a retry", rendered)
	}
	for _, want := range []string{"Allow once", "Deny", "One-time retry"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("content = %q, missing %q", rendered, want)
		}
	}

	// Selection cycles between allow once and deny only.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := m.approval.selectedAction, 2; got != want {
		t.Fatalf("selectedAction after tab = %d, want %d", got, want)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := m.approval.selectedAction, 0; got != want {
		t.Fatalf("selectedAction after second tab = %d, want %d", got, want)
	}
}

func TestModelApprovalNoPersistAlwaysAllowKeyNeverPersists(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := sandboxRetryApproval(t, newModel(Config{Controller: ctrl}, nil))

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if submissions := ctrl.submitApprovals(); len(submissions) != 0 {
		t.Fatalf("submissions after 'a' = %+v, want none", submissions)
	}
	if !m.approval.active {
		t.Fatal("approval.active = false after inert 'a', want still pending")
	}

	// Even a directly submitted session-wide decision is downgraded.
	m.executeApprovalDecision(ApprovalDecisionAlwaysAllow)
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("submissions = %+v, want one", submissions)
	}
	if got, want := submissions[0].Decision, "allow_once"; got != want {
		t.Fatalf("submission.Decision = %q, want %q", got, want)
	}
}
