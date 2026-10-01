package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/usagestats"
)

type blockedWorkflowHandoffController struct {
	testController
	entered   chan struct{}
	release   chan struct{}
	waitFalse bool
}

func (c *blockedWorkflowHandoffController) WaitRuns(ctx context.Context) bool {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return !c.waitFalse
	case <-ctx.Done():
		return false
	}
}

func workflowHandoffConfig() config.Config {
	return config.Config{Providers: map[string]config.ProviderConfig{"local": {}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
		"current": {Provider: "local", ID: "current"}, "selected": {Provider: "local", ID: "selected"},
	}}}
}

func handoffAcceptedModel(t *testing.T, ctrl interactive.Controller, modelAlias string) *Model {
	t.Helper()
	m := newModel(Config{Model: "current", Controller: ctrl, ModelNames: []string{"current", "selected"}, SkillNames: []string{"review"}}, nil)
	m.content.AppendLine("old transcript")
	m.workflowHandoff = openWorkflowHandoffModal(80, 24, output.WorkflowHandoffEvent{Next: "review", Target: "plans/step"}, interactive.WorkflowHandoffModelSelection{ModelAlias: modelAlias})
	next, _ := m.acceptWorkflowHandoff()
	return next.(*Model)
}

func settleMessage(t *testing.T, m *Model) *Model {
	t.Helper()
	cmd := m.applyEvent(output.NewStopReasonEvent(1, "workflow_handoff", nil))
	if cmd == nil {
		t.Fatal("handoff stop returned no settlement command")
	}
	return updateModel(t, m, cmd())
}

func TestWorkflowHandoffWaitsBeforeClearAndLaunch(t *testing.T) {
	ctrl := &blockedWorkflowHandoffController{testController: testController{config: workflowHandoffConfig()}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	m := handoffAcceptedModel(t, ctrl, "")
	m.recorder = usagestats.New(nil)
	m.recorder.Record(usagestats.Observation{Source: usagestats.SourceParent, PromptTokens: 7})
	m.convState = output.ConversationStateEvent{State: "generating"}
	m.convStateSeen = true
	m.convLabelShown = true
	if got := m.content.String(m.viewport.Width()); !strings.Contains(got, "old transcript") || m.pendingWorkflowHandoffLaunch == nil {
		t.Fatal("accept cleared transcript or lost pending launch")
	}
	if !m.sessionBusy() {
		t.Fatal("sessionBusy = false while handoff waits")
	}
	if len(ctrl.rotateSessionActions()) != 0 {
		t.Fatal("accept rotated session")
	}
	waitCmd := m.applyEvent(output.NewStopReasonEvent(1, "workflow_handoff", nil))
	waitResult := make(chan tea.Msg, 1)
	go func() { waitResult <- waitCmd() }()
	<-ctrl.entered
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "workflow_handoff", "call", "", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1, ToolCalls: 1})})
	if !strings.Contains(m.content.String(m.viewport.Width()), "old transcript") {
		t.Fatal("late finish events cleared transcript before settlement")
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatal("prompt submitted while waiting")
	}
	close(ctrl.release)
	m = updateModel(t, m, <-waitResult)
	if strings.Contains(m.content.String(m.viewport.Width()), "old transcript") {
		t.Fatal("old transcript remained after clear")
	}
	if ctrl.countSubmitPrompt() != 1 || ctrl.countByType(interactive.ClearConversation{}) != 1 || len(ctrl.rotateSessionActions()) != 0 {
		t.Fatalf("prompt=%d clear=%d rotates=%d", ctrl.countSubmitPrompt(), ctrl.countByType(interactive.ClearConversation{}), len(ctrl.rotateSessionActions()))
	}
	if m.recorder.SessionReport().Requests != 0 || m.convStateSeen || m.convLabelShown || m.convState.State != "" {
		t.Fatal("successful settlement left recorder or conversation labels stale")
	}
}

func TestWorkflowHandoffStaleAndDuplicateSettlementIgnored(t *testing.T) {
	ctrl := &testController{config: workflowHandoffConfig()}
	m := handoffAcceptedModel(t, ctrl, "")
	old := m.pendingWorkflowHandoffLaunch
	m = settleMessage(t, m)
	if _, secondCancel := m.acceptWorkflowHandoff(); secondCancel != nil {
		t.Fatal("new acceptance returned unexpected command")
	}
	current := m.pendingWorkflowHandoffLaunch
	_, _ = m.handleWorkflowHandoffSettled(workflowHandoffSettledMsg{launch: old})
	if m.pendingWorkflowHandoffLaunch != current {
		t.Fatal("stale settlement consumed newer launch")
	}
	_, _ = m.handleWorkflowHandoffSettled(workflowHandoffSettledMsg{launch: &workflowHandoffLaunch{}})
	if m.pendingWorkflowHandoffLaunch != current || ctrl.countByType(interactive.ClearConversation{}) != 1 || ctrl.countSubmitPrompt() != 1 {
		t.Fatal("stale settlement consumed current launch")
	}
	m = settleMessage(t, m)
	if ctrl.countByType(interactive.ClearConversation{}) != 2 || ctrl.countSubmitPrompt() != 2 {
		t.Fatal("second valid handoff did not clear and launch once")
	}
	_, _ = m.handleWorkflowHandoffSettled(workflowHandoffSettledMsg{launch: current})
	if ctrl.countByType(interactive.ClearConversation{}) != 2 || ctrl.countSubmitPrompt() != 2 {
		t.Fatal("duplicate settlement repeated clear or launch")
	}
}

func TestWorkflowHandoffWaitRefusalDoesNotLaunch(t *testing.T) {
	ctrl := &blockedWorkflowHandoffController{testController: testController{config: workflowHandoffConfig()}, entered: make(chan struct{}, 1), release: make(chan struct{}), waitFalse: true}
	m := handoffAcceptedModel(t, ctrl, "")
	waitCmd := m.applyEvent(output.NewStopReasonEvent(1, "workflow_handoff", nil))
	result := make(chan tea.Msg, 1)
	go func() { result <- waitCmd() }()
	<-ctrl.entered
	close(ctrl.release)
	m = updateModel(t, m, <-result)
	if ctrl.countSubmitPrompt() != 0 || ctrl.countByType(interactive.ClearConversation{}) != 0 || m.pendingWorkflowHandoffLaunch != nil {
		t.Fatal("WaitRuns refusal cleared or launched")
	}
	if !strings.Contains(m.content.String(m.viewport.Width()), "run waiter refused to settle") {
		t.Fatal("wait refusal not visible")
	}
	if m.input.Value() != "/review plans/step" {
		t.Fatalf("composer = %q", m.input.Value())
	}
}

func TestWorkflowHandoffOneshotRefusalDoesNotClear(t *testing.T) {
	ctrl := &testController{config: workflowHandoffConfig()}
	m := handoffAcceptedModel(t, ctrl, "selected")
	m.oneshotRunning = true
	m = settleMessage(t, m)
	if ctrl.countByType(interactive.ClearConversation{}) != 0 || len(ctrl.switchModelActions()) != 0 || ctrl.countSubmitPrompt() != 0 || !strings.Contains(m.content.String(m.viewport.Width()), "oneshot is active") {
		t.Fatal("oneshot handoff refusal cleared, switched, launched, or omitted error")
	}
}

func TestWorkflowHandoffSelectedModelNeedsConfigCapability(t *testing.T) {
	ctrl := &waiterOnlyWorkflowHandoffController{}
	m := handoffAcceptedModel(t, ctrl, "selected")
	m = settleMessage(t, m)
	if ctrl.clears != 0 || ctrl.switches != 0 || ctrl.prompts != 0 || !strings.Contains(m.content.String(m.viewport.Width()), "config unavailable") {
		t.Fatal("missing config capability allowed clear/switch/launch")
	}
}

type waiterOnlyWorkflowHandoffController struct {
	clears   int
	switches int
	prompts  int
}

func (*waiterOnlyWorkflowHandoffController) WaitRuns(context.Context) bool { return true }
func (c *waiterOnlyWorkflowHandoffController) Handle(_ context.Context, action interactive.Action) error {
	switch action.(type) {
	case interactive.ClearConversation:
		c.clears++
	case interactive.SwitchModel:
		c.switches++
	case interactive.SubmitPrompt:
		c.prompts++
	}
	return nil
}

func TestWorkflowHandoffClearRefusalPreservesState(t *testing.T) {
	ctrl := &testController{config: workflowHandoffConfig(), clearConversationErr: errors.New("clear refused")}
	m := handoffAcceptedModel(t, ctrl, "")
	m.fileHistory = []string{"recent"}
	m.imageMarkers = []imageMarker{{label: "[img]"}}
	m.recorder = usagestats.New(nil)
	m.recorder.Record(usagestats.Observation{Source: usagestats.SourceParent, PromptTokens: 10})
	m = settleMessage(t, m)
	if !strings.Contains(m.content.String(m.viewport.Width()), "old transcript") || m.fileHistory[0] != "recent" || len(m.imageMarkers) != 1 || m.recorder.SessionReport().Requests != 1 {
		t.Fatal("refused clear changed preserved state")
	}
	if m.input.Value() != "/review plans/step" || ctrl.countSubmitPrompt() != 0 || ctrl.countByType(interactive.ClearConversation{}) != 1 {
		t.Fatalf("clear refusal state: composer=%q prompts=%d clears=%d content=%q", m.input.Value(), ctrl.countSubmitPrompt(), ctrl.countByType(interactive.ClearConversation{}), m.content.String(m.viewport.Width()))
	}
}

func TestWorkflowHandoffMissingWaiterRefusesBeforeAccept(t *testing.T) {
	ctrl := &clearTransactionController{}
	m := newModel(Config{Controller: ctrl}, nil)
	m.workflowHandoff = openWorkflowHandoffModal(80, 24, output.WorkflowHandoffEvent{Next: "review"}, interactive.WorkflowHandoffModelSelection{})
	m.acceptWorkflowHandoff()
	if !m.workflowHandoff.IsOpen() || ctrl.calls != 0 || !strings.Contains(m.content.String(m.viewport.Width()), "requires a run waiter") {
		t.Fatalf("missing waiter refusal: modal=%v calls=%d content=%q", m.workflowHandoff.IsOpen(), ctrl.calls, m.content.String(m.viewport.Width()))
	}
}

func TestWorkflowHandoffInvalidModelDoesNotClear(t *testing.T) {
	ctrl := &testController{config: workflowHandoffConfig()}
	m := settleMessage(t, handoffAcceptedModel(t, ctrl, "missing-model"))
	if ctrl.countByType(interactive.ClearConversation{}) != 0 || len(ctrl.switchModelActions()) != 0 || ctrl.countSubmitPrompt() != 0 || m.primaryModel != "current" || !strings.Contains(m.content.String(m.viewport.Width()), "invalid selected handoff model") {
		t.Fatalf("invalid-model outcome: clears=%d switches=%v prompts=%d model=%s content=%q", ctrl.countByType(interactive.ClearConversation{}), ctrl.switchModelActions(), ctrl.countSubmitPrompt(), m.primaryModel, m.content.String(m.viewport.Width()))
	}
}

func TestWorkflowHandoffUnexpectedSwitchFailureReportsPartialClear(t *testing.T) {
	ctrl := &testController{config: workflowHandoffConfig(), switchModelErr: errors.New("switch rejected")}
	m := settleMessage(t, handoffAcceptedModel(t, ctrl, "selected"))
	if ctrl.countByType(interactive.ClearConversation{}) != 1 || len(ctrl.switchModelActions()) != 1 || ctrl.countSubmitPrompt() != 0 || m.primaryModel != "current" || !strings.Contains(m.content.String(m.viewport.Width()), "conversation was cleared") {
		t.Fatalf("switch-failure outcome: clears=%d switches=%v prompts=%d model=%s content=%q", ctrl.countByType(interactive.ClearConversation{}), ctrl.switchModelActions(), ctrl.countSubmitPrompt(), m.primaryModel, m.content.String(m.viewport.Width()))
	}
}

func TestWorkflowHandoffCancellationInvalidatesWait(t *testing.T) {
	for _, name := range []string{"interrupt", "exit", "bridge", "error stop"} {
		t.Run(name, func(t *testing.T) {
			ctrl := &blockedWorkflowHandoffController{testController: testController{config: workflowHandoffConfig()}, entered: make(chan struct{}, 1), release: make(chan struct{})}
			m := handoffAcceptedModel(t, ctrl, "")
			waitCmd := m.applyEvent(output.NewStopReasonEvent(1, "workflow_handoff", nil))
			result := make(chan tea.Msg, 1)
			go func() { result <- waitCmd() }()
			<-ctrl.entered
			switch name {
			case "interrupt":
				m.executeInterruptAction()
			case "exit":
				m.doExit()
			case "bridge":
				m.handleBridgeClosedMsg(bridgeClosedMsg{})
			case "error stop":
				m.applyEvent(output.NewStopReasonEvent(2, "error", errors.New("driver error")))
			}
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Fatal("cancel did not unblock WaitRuns")
			}
			if m.pendingWorkflowHandoffLaunch != nil || ctrl.countSubmitPrompt() != 0 || ctrl.countByType(interactive.ClearConversation{}) != 0 {
				t.Fatal("cancelled handoff retained launch or mutated session")
			}
		})
	}
}
