package oneshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

func TestRunLockOwnershipChecks(t *testing.T) {
	tests := []struct {
		name string
		// steal rewrites the lock file as another owner; nil leaves it ours.
		steal func(t *testing.T, path string, ours LockRecord)
		// lost is whether Heartbeat must report errLockLost.
		lost bool
		// fileKept is whether the file must still exist after Release.
		fileKept bool
	}{
		{name: "still owned", lost: false, fileKept: false},
		{
			name: "different owner",
			steal: func(t *testing.T, path string, ours LockRecord) {
				writeStolenLock(t, path, LockRecord{Owner: "someone-else", AcquiredAt: ours.AcquiredAt, UpdatedAt: ours.UpdatedAt})
			},
			lost: true, fileKept: true,
		},
		{
			name: "same run id reacquired later",
			steal: func(t *testing.T, path string, ours LockRecord) {
				writeStolenLock(t, path, LockRecord{Owner: ours.Owner, AcquiredAt: ours.AcquiredAt.Add(time.Second), UpdatedAt: ours.UpdatedAt})
			},
			lost: true, fileKept: true,
		},
		{
			name: "file removed",
			steal: func(t *testing.T, path string, _ LockRecord) {
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove lock: %v", err)
				}
			},
			lost: true, fileKept: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
			lock, err := acquireRunLock(dir, identity, time.Hour)
			if err != nil {
				t.Fatalf("acquireRunLock: %v", err)
			}
			path := identity.LockPath(dir)
			ours, _, err := readLockRecord(path)
			if err != nil {
				t.Fatalf("read lock: %v", err)
			}
			if tt.steal != nil {
				tt.steal(t, path, ours)
			}
			old := time.Now().Add(-time.Hour)
			if _, err := os.Stat(path); err == nil {
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatalf("backdate: %v", err)
				}
			}

			hbErr := lock.Heartbeat()
			if got := errors.Is(hbErr, errLockLost); got != tt.lost {
				t.Fatalf("Heartbeat error = %v, errLockLost = %v, want %v", hbErr, got, tt.lost)
			}
			if tt.lost && tt.fileKept {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat lock: %v", err)
				}
				if !info.ModTime().Equal(old) {
					t.Fatalf("lost Heartbeat touched foreign lock: mtime %v, want %v", info.ModTime(), old)
				}
			}
			if err := lock.Release(); err != nil {
				t.Fatalf("Release: %v", err)
			}
			_, statErr := os.Stat(path)
			if kept := statErr == nil; kept != tt.fileKept {
				t.Fatalf("lock file present after Release = %v, want %v", kept, tt.fileKept)
			}
		})
	}
}

func writeStolenLock(t *testing.T, path string, record LockRecord) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove lock: %v", err)
	}
	if err := writeLockExclusive(path, record); err != nil {
		t.Fatalf("write foreign lock: %v", err)
	}
}

// funcRunner adapts a function to PhaseRunner.
type funcRunner func(ctx context.Context) (RunResult, error)

func (f funcRunner) RunPhase(ctx context.Context, _ []agent.Message, _ []string, _ func() []agent.SteerMessage) (RunResult, error) {
	return f(ctx)
}

type funcRunnerFactory struct{ runner PhaseRunner }

func (f funcRunnerFactory) NewPhaseRunner(context.Context, Phase, string, tool.ApprovalResponder, config.AdvisorConfig) (PhaseRunner, error) {
	return f.runner, nil
}

func newHeartbeatTestOrchestrator(runner PhaseRunner, events *[]output.Event) *Orchestrator {
	return &Orchestrator{
		deps: Dependencies{
			Config:        configWithEffectiveModels("fallback-model", nil),
			Events:        output.SinkFunc(func(event output.Event) { *events = append(*events, event) }),
			RunnerFactory: funcRunnerFactory{runner: runner},
			SessionStore:  &recordingSessionStore{},
		},
		heartbeatInterval: 5 * time.Millisecond,
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func TestRunPhaseHeartbeatsLockWhileRunnerBlocks(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	lock, err := acquireRunLock(dir, identity, time.Hour)
	if err != nil {
		t.Fatalf("acquireRunLock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	path := identity.LockPath(dir)

	old := time.Now().Add(-time.Hour)
	var refreshed bool
	runner := funcRunner(func(context.Context) (RunResult, error) {
		// Backdate after phase start, then wait for the background heartbeat.
		if err := os.Chtimes(path, old, old); err != nil {
			return RunResult{}, err
		}
		refreshed = waitFor(t, 5*time.Second, func() bool {
			info, err := os.Stat(path)
			return err == nil && info.ModTime().After(old)
		})
		return RunResult{}, errors.New("runner done")
	})
	var events []output.Event
	o := newHeartbeatTestOrchestrator(runner, &events)
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{}, PhaseSessionIDs: map[Phase]string{}}
	store := NewManifestStore(filepath.Join(dir, "manifest.json"))

	err = o.runPhase(runPhaseParams{InterruptCtx: context.Background(), Store: store, Manifest: manifest, Lock: lock, Phase: PhasePlan})
	if err == nil {
		t.Fatal("runPhase succeeded, want runner error")
	}
	if !refreshed {
		t.Fatal("lock mtime never advanced while the phase runner was blocked")
	}

	// The heartbeat goroutine must be gone: backdating again must stick.
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	time.Sleep(40 * time.Millisecond) // eight heartbeat intervals
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if !info.ModTime().Equal(old) {
		t.Fatalf("heartbeat goroutine still running after runPhase returned: mtime %v", info.ModTime())
	}
}

func TestRunPhaseAbortsWhenLockLost(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	lock, err := acquireRunLock(dir, identity, time.Hour)
	if err != nil {
		t.Fatalf("acquireRunLock: %v", err)
	}
	path := identity.LockPath(dir)
	ours, _, err := readLockRecord(path)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}

	runner := funcRunner(func(ctx context.Context) (RunResult, error) {
		writeStolenLock(t, path, LockRecord{Owner: "other", AcquiredAt: ours.AcquiredAt, UpdatedAt: ours.UpdatedAt})
		select {
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		case <-time.After(5 * time.Second):
			return RunResult{}, errors.New("phase was not aborted after the lock was lost")
		}
	})
	var events []output.Event
	o := newHeartbeatTestOrchestrator(runner, &events)
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{}, PhaseSessionIDs: map[Phase]string{}}
	store := NewManifestStore(filepath.Join(dir, "manifest.json"))

	err = o.runPhase(runPhaseParams{InterruptCtx: context.Background(), Store: store, Manifest: manifest, Lock: lock, Phase: PhasePlan})
	if !errors.Is(err, errLockLost) {
		t.Fatalf("runPhase error = %v, want errLockLost", err)
	}
	if got := manifest.PhaseStatuses[PhasePlan]; got != PhaseStatusFailed {
		t.Fatalf("phase status = %q, want failed", got)
	}
	// The failed status is only in memory: the on-disk manifest must not have
	// been rewritten by the process that lost the lock.
	if disk, err := store.Read(); err == nil && disk.PhaseStatuses[PhasePlan] == PhaseStatusFailed {
		t.Fatalf("manifest on disk was overwritten with failed status after the lock was lost")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign lock was removed by Release: %v", err)
	}
}

func TestRunPhaseFinalManifestWriteFailureEmitsFailureEvents(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	planningPath := identity.PlanningPath(projectRoot)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	store := NewManifestStore(manifestPath)

	runner := funcRunner(func(context.Context) (RunResult, error) {
		if err := os.MkdirAll(planningPath, 0o755); err != nil {
			return RunResult{}, err
		}
		for _, name := range []string{"overview.md", "plan.yaml"} {
			if err := os.WriteFile(filepath.Join(planningPath, name), []byte("x\n"), 0o644); err != nil {
				return RunResult{}, err
			}
		}
		// Make the next manifest write fail: rename onto a directory errors.
		if err := os.Remove(manifestPath); err != nil {
			return RunResult{}, err
		}
		return RunResult{}, os.Mkdir(manifestPath, 0o755)
	})
	var events []output.Event
	o := newHeartbeatTestOrchestrator(runner, &events)
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{}, PhaseSessionIDs: map[Phase]string{}}

	err := o.runPhase(runPhaseParams{
		InterruptCtx: context.Background(), Store: store, Manifest: manifest,
		WorktreePath: projectRoot, PlanningPath: planningPath, Phase: PhasePlan,
	})
	if err == nil {
		t.Fatal("runPhase succeeded, want manifest write failure")
	}
	var sawIndicator, sawTransition bool
	for _, event := range events {
		switch payload := event.Payload.(type) {
		case output.PhaseIndicatorEvent:
			sawIndicator = sawIndicator || payload.State == phaseIndicatorCancelled
		case output.PhaseTransitionEvent:
			sawTransition = sawTransition || payload.Status == phaseTransitionFailed
		}
	}
	if !sawIndicator || !sawTransition {
		t.Fatalf("failure events missing (indicator=%v transition=%v): %+v", sawIndicator, sawTransition, events)
	}
}

type failingSessionStore struct{}

func (failingSessionStore) Save(session.Session) error { return errors.New("disk full") }

func TestRunPhaseLockLostWithSessionSaveFailureSkipsManifestWrite(t *testing.T) {
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	lock, err := acquireRunLock(dir, identity, time.Hour)
	if err != nil {
		t.Fatalf("acquireRunLock: %v", err)
	}
	path := identity.LockPath(dir)
	ours, _, err := readLockRecord(path)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}

	runner := funcRunner(func(ctx context.Context) (RunResult, error) {
		writeStolenLock(t, path, LockRecord{Owner: "other", AcquiredAt: ours.AcquiredAt, UpdatedAt: ours.UpdatedAt})
		<-ctx.Done()
		return RunResult{}, ctx.Err()
	})
	var events []output.Event
	o := newHeartbeatTestOrchestrator(runner, &events)
	o.deps.SessionStore = failingSessionStore{}
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{PhasePlan: PhaseStatusRunning}, PhaseSessionIDs: map[Phase]string{}}
	store := NewManifestStore(filepath.Join(dir, "manifest.json"))
	if err := store.Write(*manifest); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	err = o.runPhase(runPhaseParams{InterruptCtx: context.Background(), Store: store, Manifest: manifest, Lock: lock, Phase: PhasePlan})
	if err == nil {
		t.Fatal("runPhase succeeded, want an error")
	}
	disk, err := store.Read()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if got := disk.PhaseStatuses[PhasePlan]; got != PhaseStatusRunning {
		t.Fatalf("on-disk phase status = %q, want %q (untouched after lock loss)", got, PhaseStatusRunning)
	}
}
