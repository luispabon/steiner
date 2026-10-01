package tui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
)

type workflowHandoffRunWaiter interface {
	WaitRuns(context.Context) bool
}

type workflowHandoffConfigProvider interface {
	Config() config.Config
}

type workflowHandoffSettledMsg struct {
	launch *workflowHandoffLaunch
	err    error
}

func beginWorkflowHandoffSettlement(controller interactive.Controller, launch *workflowHandoffLaunch) tea.Cmd {
	waiter, ok := controller.(workflowHandoffRunWaiter)
	if !ok {
		return func() tea.Msg {
			return workflowHandoffSettledMsg{launch: launch, err: errors.New("workflow handoff run waiter unavailable")}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	launch.cancel = cancel
	return func() tea.Msg {
		if !waiter.WaitRuns(ctx) {
			err := ctx.Err()
			if err == nil {
				err = errors.New("run waiter refused to settle")
			}
			return workflowHandoffSettledMsg{launch: launch, err: err}
		}
		return workflowHandoffSettledMsg{launch: launch}
	}
}

func (m *Model) handleWorkflowHandoffSettled(msg workflowHandoffSettledMsg) (tea.Model, tea.Cmd) {
	launch := m.pendingWorkflowHandoffLaunch
	if launch == nil || launch != msg.launch {
		return m, nil
	}
	if launch.cancel != nil {
		launch.cancel()
		launch.cancel = nil
	}
	m.pendingWorkflowHandoffLaunch = nil
	m.suppressWorkflowHandoffRun = false
	if msg.err != nil {
		m.restoreWorkflowHandoffSubmission(launch, fmt.Errorf("wait for workflow handoff run: %w", msg.err))
		return m, nil
	}

	if m.oneshotRunning {
		m.pendingWorkflowHandoffLaunch = nil
		m.suppressWorkflowHandoffRun = false
		m.restoreWorkflowHandoffSubmission(launch, errors.New("cannot hand off while oneshot is active"))
		return m, nil
	}
	if launch.modelAlias != "" && launch.modelAlias != m.primaryModel && m.controller != nil {
		provider, ok := m.controller.(workflowHandoffConfigProvider)
		if !ok {
			m.pendingWorkflowHandoffLaunch = nil
			m.suppressWorkflowHandoffRun = false
			m.restoreWorkflowHandoffSubmission(launch, errors.New("cannot validate selected model before clearing conversation: controller config unavailable"))
			return m, nil
		}
		cfg := provider.Config()
		if _, _, err := config.ParseModelReference(&cfg, launch.modelAlias); err != nil {
			m.pendingWorkflowHandoffLaunch = nil
			m.suppressWorkflowHandoffRun = false
			m.restoreWorkflowHandoffSubmission(launch, fmt.Errorf("invalid selected handoff model %q: %w", launch.modelAlias, err))
			return m, nil
		}
	}
	m.pendingWorkflowHandoffLaunch = nil
	m.suppressWorkflowHandoffRun = false
	err := m.clearWorkflowHandoffConversation()
	if err != nil {
		m.restoreWorkflowHandoffSubmission(launch, nil)
		return m, nil
	}
	if launch.modelAlias != "" && launch.modelAlias != m.primaryModel {
		if m.controller != nil {
			if err := m.controller.Handle(context.Background(), interactive.SwitchModel{Name: launch.modelAlias}); err != nil {
				m.restoreWorkflowHandoffSubmission(launch, fmt.Errorf("conversation was cleared, but switching to model %q failed: %w", launch.modelAlias, err))
				return m, nil
			}
		}
		m.applyModelSelection(launch.modelAlias, m.modelBaseURLs[launch.modelAlias])
	}
	_, cmd := m.launchWorkflowHandoff(launch.next, launch.target, launch.submission)
	return m, cmd
}

func (m *Model) clearWorkflowHandoffConversation() error {
	if err := m.performClearConversationState(); err != nil {
		return err
	}
	if m.recorder != nil {
		m.recorder.ResetSession()
		m.syncSidebar()
	}
	return nil
}

func (m *Model) restoreWorkflowHandoffSubmission(launch *workflowHandoffLaunch, err error) {
	if err != nil {
		m.appendError(err)
	}
	composer := launch.submission
	if composer == "" {
		composer = "/" + launch.next
		if launch.target != "" {
			composer += " " + launch.target
		}
	}
	m.input.SetValue(composer)
	m.input.Focus()
	m.syncInputChrome()
	m.relayoutInput()
	m.syncViewport()
}

func (m *Model) cancelWorkflowHandoffSettlement() {
	launch := m.pendingWorkflowHandoffLaunch
	m.pendingWorkflowHandoffLaunch = nil
	m.suppressWorkflowHandoffRun = false
	if launch != nil && launch.cancel != nil {
		launch.cancel()
		launch.cancel = nil
	}
}
