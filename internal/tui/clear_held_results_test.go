package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

// TestClearAllowedWhenPausedOnFinishedResults drives the real sequence: a run
// with a child, Esc to interrupt, the child completing, the driver pausing on
// the unacknowledged result, then /clear.
func TestClearAllowedWhenPausedOnFinishedResults(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("old transcript")

	m.applyEvent(output.NewRunStartedEvent("interactive", "test-model", "prompt", 10, 4096))
	m.applyEvent(output.NewDelegationStartedEvent(callOcc("call-1", "child-1"), "inspect the repository", "", "explore"))
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: callOcc("call-1", "child-1"),
		Status:               "completed",
	}))

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if got := ctrl.countInterruptActiveRun(); got != 1 {
		t.Fatalf("interrupt actions = %d, want 1", got)
	}
	applyConversationStateEvent(t, m, conversationStateWaiting, true, 1, false)
	if !m.sessionBusy() {
		t.Fatal("sessionBusy = false while paused on finished results")
	}

	m.input.SetValue("/clear")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := ctrl.countByType(interactive.ClearConversation{}); got != 1 {
		t.Fatalf("clear actions = %d, want 1", got)
	}
	if m.convStateSeen || m.convState != (output.ConversationStateEvent{}) {
		t.Fatalf("conversation state after clear = %+v seen=%v, want reset", m.convState, m.convStateSeen)
	}
	if m.activity.label != "" || m.activity.spinning {
		t.Fatalf("activity after clear = %+v, want paused label cleared", m.activity)
	}
	if m.interruptPending {
		t.Error("interruptPending survived clear")
	}
	if got := m.content.String(80); strings.Contains(got, "old transcript") {
		t.Fatalf("content after clear = %q, want transcript cleared", got)
	}
}

func TestClearRefusedUnlessPausedWithoutDelegations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		setup       func(*testing.T, *Model)
		wantCleared bool
	}{
		{
			name:        "paused on finished results",
			wantCleared: true,
			setup:       func(*testing.T, *Model) {},
		},
		{
			name: "active delegation",
			setup: func(_ *testing.T, m *Model) {
				m.applyEvent(output.NewDelegationStartedEvent(callOcc("call-1", "child-1"), "work", "", "code"))
			},
		},
		{
			name: "ordinary waiting",
			setup: func(t *testing.T, m *Model) {
				applyConversationStateEvent(t, m, conversationStateWaiting, false, 1, false)
			},
		},
		{
			name: "budget exhausted waiting",
			setup: func(t *testing.T, m *Model) {
				applyConversationStateEvent(t, m, conversationStateWaiting, false, 1, true)
			},
		},
		{
			name: "generating",
			setup: func(t *testing.T, m *Model) {
				applyConversationStateEvent(t, m, conversationStateGenerating, false, 1, false)
			},
		},
		{
			name: "active tool call",
			setup: func(_ *testing.T, m *Model) {
				m.content.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-1", nil))
			},
		},
		{
			name: "activity",
			setup: func(_ *testing.T, m *Model) {
				m.activity = m.activity.waiting("running", "model")
			},
		},
		{
			name: "compaction",
			setup: func(_ *testing.T, m *Model) {
				m.setCompaction(compactionState{active: true, summary: "compacting"})
			},
		},
		{
			name: "oneshot",
			setup: func(_ *testing.T, m *Model) {
				m.oneshotRunning = true
			},
		},
		{
			name: "pending handoff",
			setup: func(_ *testing.T, m *Model) {
				m.pendingWorkflowHandoffLaunch = &workflowHandoffLaunch{}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := &testController{}
			m := newModel(Config{}, nil)
			m.controller = ctrl
			m.content.AppendLine("old transcript")
			applyConversationStateEvent(t, m, conversationStateWaiting, true, 1, false)
			tt.setup(t, m)

			m.executeClearAction()

			gotCleared := ctrl.countByType(interactive.ClearConversation{}) == 1
			if gotCleared != tt.wantCleared {
				t.Fatalf("cleared = %v, want %v", gotCleared, tt.wantCleared)
			}
			if tt.wantCleared {
				if got := m.content.String(80); strings.Contains(got, "old transcript") {
					t.Fatalf("content after clear = %q, want transcript cleared", got)
				}
				return
			}
			if got := m.content.String(80); !strings.Contains(got, "old transcript") {
				t.Fatalf("content after refused clear = %q, want transcript preserved", got)
			}
			assertClearConversationRefused(t, m)
		})
	}
}

func TestClearRejectionWhilePausedKeepsTranscript(t *testing.T) {
	t.Parallel()
	clearErr := errors.New("clear refused")
	ctrl := &testController{clearConversationErr: clearErr}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m.content.AppendLine("paused transcript")
	applyConversationStateEvent(t, m, conversationStateWaiting, true, 2, false)

	_, cleared, err := m.clearConversationStateWithError()

	if cleared || !errors.Is(err, clearErr) {
		t.Fatalf("clear result = (%v, %v), want (false, %v)", cleared, err, clearErr)
	}
	if got := ctrl.countByType(interactive.ClearConversation{}); got != 1 {
		t.Fatalf("clear actions = %d, want 1", got)
	}
	got := m.content.String(80)
	if !strings.Contains(got, "paused transcript") || !strings.Contains(got, clearErr.Error()) {
		t.Fatalf("content = %q, want paused transcript and clear error", got)
	}
	if !m.convStateSeen || !m.convState.Held || m.activity.label == "" {
		t.Fatalf("paused state after rejected clear = %+v seen=%v activity=%q, want preserved", m.convState, m.convStateSeen, m.activity.label)
	}
}
