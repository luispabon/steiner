package oneshot

import (
	"context"
	"errors"
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
// phase through runPhase's normal error path instead of letting the run
// continue holding a lock it can no longer keep fresh.
func TestRunPhaseStopsWhenHeartbeatFails(t *testing.T) {
	runnerFactory := &recordingRunnerFactory{}
	orchestrator := &Orchestrator{deps: Dependencies{
		Config:        configWithEffectiveModels("fallback-model", nil),
		Events:        output.SinkFunc(func(output.Event) {}),
		RunnerFactory: runnerFactory,
	}}
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{}}
	// A lock path whose parent directory does not exist makes Chtimes fail.
	lock := &RunLock{path: filepath.Join(t.TempDir(), "missing", "lock")}

	err := orchestrator.runPhase(runPhaseParams{
		InterruptCtx: context.Background(),
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
		t.Fatalf("phase status = %q, want %q", got, PhaseStatusFailed)
	}
	if len(runnerFactory.calls) != 0 {
		t.Fatalf("phase runner started despite heartbeat failure: %v", runnerFactory.calls)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("want heartbeat error, got context cancellation")
	}
}
