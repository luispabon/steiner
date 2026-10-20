package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
)

// oneshotSessionStoreOrEmit casts sessionStore to oneshot.SessionStore,
// emitting the standard "not configured" failure events on the sink when it
// does not implement the interface.
func oneshotSessionStoreOrEmit(sessionStore SessionLister, sink output.EventSink, runID string) (oneshot.SessionStore, bool) {
	oneshotSessionStore, ok := sessionStore.(oneshot.SessionStore)
	if !ok {
		sink.Emit(output.NewOverlayReportEvent("Oneshot Report", "session store does not implement required oneshot interface"))
		sink.Emit(output.NewOneshotFinishedEvent(runID, nil))
		return nil, false
	}
	return oneshotSessionStore, true
}

// runOrchestratorAndReport runs run (Orchestrator.Run or Orchestrator.Resume)
// and emits the standard failed/report/finished event sequence.
func runOrchestratorAndReport(sink output.EventSink, runID, failureLabel string, run func() (oneshot.Manifest, error)) {
	manifest, err := run()
	if err != nil {
		sink.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("%s: %v", failureLabel, err)))
	}
	if manifest.ReportPath != "" {
		sink.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot report: %s", manifest.ReportPath)))
	}
	sink.Emit(output.NewOneshotFinishedEvent(runID, err))
}

// routeControlToOneshot points the session's prompt, steer, stop and cancel
// actions at the oneshot run being launched, from now until release. register
// is the per-phase callback: it moves control to the phase that is starting
// and, between phases, leaves steers queued for the next one.
func routeControlToOneshot(sess *interactive.Session) (register func(oneshot.PhaseControl) func(), release func()) {
	router := &interactive.PhaseRouter{}
	release = sess.SetActivePhaseControl(router)
	return func(pc oneshot.PhaseControl) func() { return router.Register(pc) }, release
}

// prepareOneshotRun applies the guard checks and run-state setup shared
// by launch and resume. ok is false when a guard failed and m already
// carries the corresponding status message and reset input.
func (m *Model) prepareOneshotRun() (*Model, bool) {
	if !m.subAgentsEnabled {
		m.content.AppendLine("status: oneshot unavailable: sub-agents are disabled in config")
		m.input.Reset()
		m.relayoutInput()
		m.syncViewport()
		return m, false
	}

	if m.oneshotRunnerFactory == nil {
		m.content.AppendLine("status: oneshot runner factory not configured")
		m.input.Reset()
		m.relayoutInput()
		m.syncViewport()
		return m, false
	}

	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		m.input.Reset()
		m.relayoutInput()
		m.syncViewport()
		return m, false
	}

	m.oneshotRunning = true
	m.oneshotPhase = ""
	return m, true
}

func (m *Model) executeLaunchOneshotAction(task string) (tea.Model, tea.Cmd) {
	m, ok := m.prepareOneshotRun()
	if !ok {
		return m, nil
	}

	// Create run identity
	runIdentity, err := oneshot.NewRunIdentity(strings.TrimSpace(task))
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: launch oneshot failed: %v", err))
		m.oneshotRunning = false
		m.syncInputChrome()
		m.syncSidebar()
		m.syncViewport()
		return m, nil
	}

	sessionStore := m.sessionStore

	sess, ok := sessionController(m.controller)
	if !ok {
		m.content.AppendLine("status: oneshot unavailable: controller does not support this action")
		m.oneshotRunning = false
		m.syncViewport()
		return m, nil
	}

	register, releaseControl := routeControlToOneshot(sess)
	sess.RunBackground(func(ctx context.Context) {
		defer releaseControl()
		oneshotSessionStore, ok := oneshotSessionStoreOrEmit(sessionStore, sess.EventSink(), runIdentity.ID)
		if !ok {
			return
		}

		deps := oneshot.Dependencies{
			ProjectRoot:      sess.ProjectRoot(),
			Identity:         runIdentity,
			Task:             strings.TrimSpace(task),
			Config:           sess.Config(),
			SessionStore:     oneshotSessionStore,
			RunnerFactory:    m.oneshotRunnerFactory(runIdentity),
			Events:           sess.EventSink(),
			RegisterControl:  register,
			InterruptFactory: context.WithCancel,
		}

		orchestrator, err := oneshot.NewOrchestrator(deps)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runIdentity.ID, err))
			return
		}

		runOrchestratorAndReport(sess.EventSink(), runIdentity.ID, "oneshot run failed", func() (oneshot.Manifest, error) {
			return orchestrator.Run(ctx)
		})
	})

	m.content.AppendLine(fmt.Sprintf("status: launching oneshot run for: %s", task))
	m.input.Reset()
	m.syncInputChrome()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeResumeOneshotAction(runID string) (tea.Model, tea.Cmd) {
	m, ok := m.prepareOneshotRun()
	if !ok {
		return m, nil
	}

	sessionStore := m.sessionStore

	sess, ok := sessionController(m.controller)
	if !ok {
		m.content.AppendLine("status: oneshot unavailable: controller does not support this action")
		m.oneshotRunning = false
		m.syncViewport()
		return m, nil
	}
	projectRoot := sess.ProjectRoot()

	register, releaseControl := routeControlToOneshot(sess)
	sess.RunBackground(func(ctx context.Context) {
		defer releaseControl()
		manifest, err := oneshot.ListRuns(projectRoot)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("resume oneshot failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runID, err))
			return
		}

		var targetRun *oneshot.ResumableRun
		for i := range manifest {
			if manifest[i].RunID == strings.TrimSpace(runID) {
				targetRun = &manifest[i]
				break
			}
		}

		if targetRun == nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot run %q not found", runID)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runID, nil))
			return
		}

		// Reconstruct RunIdentity from ResumableRun
		identity := oneshot.RunIdentity{
			ID:   targetRun.RunID,
			Slug: targetRun.Slug,
		}

		oneshotSessionStore, ok := oneshotSessionStoreOrEmit(sessionStore, sess.EventSink(), identity.ID)
		if !ok {
			return
		}

		deps := oneshot.Dependencies{
			ProjectRoot:      projectRoot,
			Identity:         identity,
			Task:             targetRun.Task,
			Config:           sess.Config(),
			SessionStore:     oneshotSessionStore,
			RunnerFactory:    m.oneshotRunnerFactory(identity),
			Events:           sess.EventSink(),
			RegisterControl:  register,
			InterruptFactory: context.WithCancel,
		}

		orchestrator, err := oneshot.NewOrchestrator(deps)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot resume failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(identity.ID, err))
			return
		}

		runOrchestratorAndReport(sess.EventSink(), identity.ID, "oneshot resume failed", func() (oneshot.Manifest, error) {
			return orchestrator.Resume(ctx)
		})
	})

	m.content.AppendLine(fmt.Sprintf("status: resuming oneshot run: %s", runID))
	m.input.Reset()
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}

// oneshotAllowedAction checks if the input is an allowlisted command during a oneshot run.
func oneshotAllowedAction(value string) bool {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "/exit", trimmed == "/thinking":
		return true
	case trimmed == "/accent", strings.HasPrefix(trimmed, "/accent "):
		return true
	default:
		return false
	}
}
