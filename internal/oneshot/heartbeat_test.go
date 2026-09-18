package oneshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestRunLockHeartbeatRefreshesLockFile(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	lock, err := acquireRunLock(dir, identity, time.Hour)
	if err != nil {
		t.Fatalf("acquireRunLock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = lock.Release()
	})

	path := identity.LockPath(dir)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("backdate lock: %v", err)
	}
	if err := lock.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if !info.ModTime().After(old) {
		t.Fatalf("lock mtime = %v, want refreshed after %v", info.ModTime(), old)
	}
}

func TestRunLockHeartbeatReportsMissingLock(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	lock, err := acquireRunLock(dir, identity, time.Hour)
	if err != nil {
		t.Fatalf("acquireRunLock failed: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if err := lock.Heartbeat(); err == nil {
		t.Fatal("Heartbeat on a released lock succeeded, want error")
	}
}

// TestRunPhaseStopsWhenHeartbeatFails proves a failed heartbeat terminates the
// phase through runPhase's normal failure path: the failed status is persisted
// for resume and the same indicator/transition are emitted, and the phase runner
// never starts.
func TestRunPhaseStopsWhenHeartbeatFails(t *testing.T) {
	store := NewManifestStore(filepath.Join(t.TempDir(), "manifest.json"))
	runnerFactory := &recordingRunnerFactory{}
	var events []output.Event
	orchestrator := &Orchestrator{deps: Dependencies{
		Config:        configWithEffectiveModels("fallback-model", nil),
		Events:        output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		RunnerFactory: runnerFactory,
	}}
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{}}
	// A lock path whose parent directory does not exist makes Chtimes fail.
	lock := &RunLock{path: filepath.Join(t.TempDir(), "missing", "lock")}

	err := orchestrator.runPhase(runPhaseParams{
		InterruptCtx: context.Background(),
		Store:        store,
		Manifest:     manifest,
		Lock:         lock,
		Phase:        PhasePlan,
	})
	if err == nil {
		t.Fatal("runPhase succeeded, want heartbeat error")
	}
	if !strings.Contains(err.Error(), "heartbeat run lock") {
		t.Fatalf("runPhase error = %v, want heartbeat failure", err)
	}
	if got := manifest.PhaseStatuses[PhasePlan]; got != PhaseStatusFailed {
		t.Fatalf("in-memory phase status = %q, want %q", got, PhaseStatusFailed)
	}

	persisted, readErr := store.Read()
	if readErr != nil {
		t.Fatalf("read persisted manifest: %v", readErr)
	}
	if got := persisted.PhaseStatuses[PhasePlan]; got != PhaseStatusFailed {
		t.Fatalf("persisted phase status = %q, want %q", got, PhaseStatusFailed)
	}

	var sawIndicator, sawTransition bool
	for _, event := range events {
		switch payload := event.Payload.(type) {
		case output.PhaseIndicatorEvent:
			if payload.Phase == string(PhasePlan) && payload.State == phaseIndicatorCancelled {
				sawIndicator = true
			}
		case output.PhaseTransitionEvent:
			if payload.To == string(PhasePlan) && payload.Status == phaseTransitionFailed {
				sawTransition = true
			}
		}
	}
	if !sawIndicator {
		t.Fatalf("no cancelled phase indicator emitted: %+v", events)
	}
	if !sawTransition {
		t.Fatalf("no failed phase transition emitted: %+v", events)
	}
	if len(runnerFactory.calls) != 0 {
		t.Fatalf("phase runner started despite heartbeat failure: %v", runnerFactory.calls)
	}
}
