package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

type blockedWorkflowHandoffController struct {
	testController
	entered chan struct{}
	release chan struct{}
}

func (c *blockedWorkflowHandoffController) WaitRuns(ctx context.Context) bool {
	close(c.entered)
	select {
	case <-c.release:
		return true
	case <-ctx.Done():
		return false
	}
}

func TestWorkflowHandoffWaitsBeforeClearAndLaunch(t *testing.T) {
	ctrl := &blockedWorkflowHandoffController{entered: make(chan struct{}), release: make(chan struct{})}
	m := newModel(Config{Model: "current", Controller: ctrl, SkillNames: []string{"review"}}, nil)
	m.content.AppendLine("old transcript")
	m.workflowHandoff = openWorkflowHandoffModal(80, 24, output.WorkflowHandoffEvent{Next: "review", Target: "plans/step"}, interactive.WorkflowHandoffModelSelection{})
	if _, cmd := m.acceptWorkflowHandoff(); cmd != nil {
		t.Fatal("accept returned command before handoff stop")
	}
	if got := m.content.String(m.viewport.Width()); got == "" || m.pendingWorkflowHandoffLaunch == nil {
		t.Fatal("accept cleared transcript or lost pending launch")
	}
	if got := ctrl.rotateSessionActions(); len(got) != 0 {
		t.Fatalf("RotateSession actions = %#v", got)
	}

	waitCmd := m.applyEvent(output.NewStopReasonEvent(1, "workflow_handoff", nil))
	waitResult := make(chan tea.Msg, 1)
	go func() { waitResult <- waitCmd() }()
	<-ctrl.entered
	if got := m.content.String(m.viewport.Width()); got == "" {
		t.Fatal("wait cleared transcript before WaitRuns settled")
	}
	if got := ctrl.countSubmitPrompt(); got != 0 {
		t.Fatalf("prompt count while waiting = %d", got)
	}
	close(ctrl.release)
	settled := <-waitResult
	m = updateModel(t, m, settled)
	if got := m.content.String(m.viewport.Width()); strings.Contains(got, "old transcript") {
		t.Fatalf("old transcript remained after settlement and clear: %q", got)
	}
	if got := ctrl.countSubmitPrompt(); got != 1 {
		t.Fatalf("prompt count after settlement = %d, want 1", got)
	}
	if got := ctrl.rotateSessionActions(); len(got) != 0 {
		t.Fatalf("RotateSession actions = %#v, want none", got)
	}
}
