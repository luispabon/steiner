package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// pendingBashApproval drives the model into a pending bash approval. A retry
// approval is marked NoPersist; an ordinary path approval is not.
func pendingBashApproval(t *testing.T, m *Model, noPersist bool) *Model {
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
		NoPersist: noPersist,
	})})
	if !m.approval.active {
		t.Fatal("approval.active = false, want a pending approval")
	}
	if m.approval.noPersist != noPersist {
		t.Fatalf("approval.noPersist = %v, want %v", m.approval.noPersist, noPersist)
	}
	return m
}

func TestModelApprovalNoPersistHidesSessionGrant(t *testing.T) {
	t.Parallel()
	m := pendingBashApproval(t, newModel(Config{}, nil), true)

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
	m := pendingBashApproval(t, newModel(Config{Controller: ctrl}, nil), true)

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

// TestModelApprovalSelectionWrapsBothDirections pins the selection wrap in both
// directions for ordinary and one-time approvals: stepping left or up from the
// first option must land on the last offered option instead of indexing -1.
func TestModelApprovalSelectionWrapsBothDirections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		noPersist bool
		key       tea.KeyPressMsg
		want      int
	}{
		{"persistent up at first option", false, tea.KeyPressMsg{Code: tea.KeyUp}, 2},
		{"persistent left at first option", false, tea.KeyPressMsg{Code: tea.KeyLeft}, 2},
		{"persistent down from first option", false, tea.KeyPressMsg{Code: tea.KeyDown}, 1},
		{"persistent right from first option", false, tea.KeyPressMsg{Code: tea.KeyRight}, 1},
		{"one-time up at first option", true, tea.KeyPressMsg{Code: tea.KeyUp}, 2},
		{"one-time left at first option", true, tea.KeyPressMsg{Code: tea.KeyLeft}, 2},
		{"one-time down skips session grant", true, tea.KeyPressMsg{Code: tea.KeyDown}, 2},
		{"one-time right skips session grant", true, tea.KeyPressMsg{Code: tea.KeyRight}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := pendingBashApproval(t, newModel(Config{}, nil), tt.noPersist)
			m = updateModel(t, m, tt.key)
			if got := m.approval.selectedAction; got != tt.want {
				t.Fatalf("selectedAction = %d, want %d", got, tt.want)
			}
		})
	}

	// Stepping forward from the last offered option wraps back to allow once.
	for _, noPersist := range []bool{false, true} {
		m := pendingBashApproval(t, newModel(Config{}, nil), noPersist)
		m.approval.selectedAction = 2
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		if got := m.approval.selectedAction; got != 0 {
			t.Fatalf("selectedAction from last option = %d, want 0 (noPersist=%v)", got, noPersist)
		}
	}
}

func TestModelApprovalNoPersistPillHidesSessionGrant(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{collapseState: make(map[int]bool), styles: testStyles("#e8814b")}
	buffer.appendApprovalRequestedEvent(output.NewApprovalRequestedEvent(output.ApprovalEventParams{
		Tool:      "bash",
		RequestID: "req-pill",
		Mode:      "command blocked by sandbox",
		Kind:      "path",
		NoPersist: true,
	}))

	pill := findApprovalPill(t, buffer, "req-pill")
	if pill == nil {
		t.Fatal("standalone pill was not rendered")
	}
	if !pill.noPersist {
		t.Fatal("pill.noPersist = false, want true")
	}

	rendered := buffer.renderApprovalPillSegment(contentSegment{kind: segmentApprovalPill, approvalData: pill}, 100)
	if strings.Contains(rendered, "Allowed for session") {
		t.Fatalf("pill = %q, want no session-wide grant", rendered)
	}
	for _, want := range []string{"approve", "deny", "One-time retry"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("pill = %q, missing %q", rendered, want)
		}
	}
}

func TestModelApprovalPromotionPreservesNoPersist(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	pillID := "approval-retry"
	m.content.segments = append(m.content.segments, contentSegment{
		kind: segmentApprovalPill,
		approvalData: &approvalPillData{
			tool:      "bash",
			identity:  pillID,
			noPersist: true,
		},
	})

	if !m.promoteNextApproval(pillID) {
		t.Fatal("promotion failed")
	}
	if !m.approval.noPersist {
		t.Fatal("promoted approval lost its one-time flag")
	}
	if got := m.selectedApprovalDecision(); got == ApprovalDecisionAlwaysAllow {
		t.Fatalf("selectedApprovalDecision() = %q, want no session-wide grant", got)
	}
}
