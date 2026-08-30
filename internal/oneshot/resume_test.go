package oneshot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestOrchestratorResumeSkipsCompletedPhasesAndReclaimsStaleLock(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	planningPath := identity.PlanningPath(worktree.Path)
	if err := os.MkdirAll(planningPath, 0o755); err != nil {
		t.Fatalf("MkdirAll planning path failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planningPath, "overview.md"), []byte("overview\n"), 0o644); err != nil {
		t.Fatalf("write overview failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planningPath, "plan.yaml"), []byte("steps: []\n"), 0o644); err != nil {
		t.Fatalf("write plan failed: %v", err)
	}

	store := NewManifestStore(identity.ManifestPath(projectRoot))
	manifest := Manifest{
		RunID:        identity.ID,
		Slug:         identity.Slug,
		Task:         "Build the parser",
		Branch:       identity.BranchName(),
		WorktreePath: worktree.Path,
		ModelSnapshot: ModelSnapshot{
			DefaultModel: "fallback-model",
			PhaseModels: map[Phase]string{
				PhasePlan:      "plan-model",
				PhaseImplement: "implement-model",
				PhaseReview:    "review-model",
			},
		},
		CurrentPhase: PhaseImplement,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusDone,
			PhaseImplement: PhaseStatusFailed,
			PhaseReview:    PhaseStatusPending,
		},
		PhaseSessionIDs: map[Phase]string{
			PhasePlan: "plan-session",
		},
		CreatedAt: time.Unix(10, 0).UTC(),
		UpdatedAt: time.Unix(20, 0).UTC(),
	}
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	lockPath := identity.LockPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("MkdirAll lock path failed: %v", err)
	}
	stale := LockRecord{
		Owner:      "stale-owner",
		AcquiredAt: time.Now().Add(-2 * time.Hour).UTC(),
		UpdatedAt:  time.Now().Add(-2 * time.Hour).UTC(),
	}
	lockData, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale lock failed: %v", err)
	}
	if err := os.WriteFile(lockPath, lockData, 0o644); err != nil {
		t.Fatalf("write stale lock failed: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("Chtimes stale lock failed: %v", err)
	}

	sessionStore := &recordingSessionStore{}
	runnerFactory := &recordingRunnerFactory{
		planningPath: planningPath,
		runners: map[Phase]*phaseRunnerStub{
			PhaseImplement: {writePlan: true},
			PhaseReview:    {writePlan: true},
		},
	}
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        configWithEffectiveModels("fallback-model", nil),
		SessionStore:  sessionStore,
		RunnerFactory: runnerFactory,
		ManifestStore: store,
		Events:        output.SinkFunc(func(output.Event) {}),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if got, want := updated.PhaseStatuses[PhasePlan], PhaseStatusDone; got != want {
		t.Fatalf("phase plan status = %q, want %q", got, want)
	}
	if got, want := updated.PhaseStatuses[PhaseImplement], PhaseStatusDone; got != want {
		t.Fatalf("phase implement status = %q, want %q", got, want)
	}
	if got, want := updated.PhaseStatuses[PhaseReview], PhaseStatusDone; got != want {
		t.Fatalf("phase review status = %q, want %q", got, want)
	}
	if got, want := updated.PhaseSessionIDs[PhasePlan], "plan-session"; got != want {
		t.Fatalf("plan session id = %q, want %q", got, want)
	}
	if got := len(sessionStore.sessions); got != 2 {
		t.Fatalf("saved sessions = %d, want 2", got)
	}

	runnerFactory.mu.Lock()
	_, planCalled := runnerFactory.calls[PhasePlan]
	_, implementCalled := runnerFactory.calls[PhaseImplement]
	_, reviewCalled := runnerFactory.calls[PhaseReview]
	runnerFactory.mu.Unlock()
	if planCalled {
		t.Fatal("plan phase was re-run, want it skipped")
	}
	if !implementCalled || !reviewCalled {
		t.Fatalf("implementCalled=%t reviewCalled=%t, want both true", implementCalled, reviewCalled)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after resume: %v", err)
	}
}

func TestResumePreservesExistingWorktreeBase(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "preserve123", Slug: "preserve-base"}
	manifest, store, sessionStore, orch := setupResumeTestFixture(t, projectRoot, identity)
	if err := os.WriteFile(filepath.Join(manifest.WorktreePath, "preserved.txt"), []byte("preserved\n"), 0o644); err != nil {
		t.Fatalf("write preserved file: %v", err)
	}
	mustGitOutput(t, manifest.WorktreePath, "add", "preserved.txt")
	mustGitOutput(t, manifest.WorktreePath, "commit", "-m", "test: preserve worktree base")
	base := strings.TrimSpace(mustGitOutput(t, manifest.WorktreePath, "rev-parse", "HEAD"))
	// Seed a base that differs from the branch's origin/main start point. A broken preserve step would re-infer origin/main and fail the equality assertion. The real base also keeps the resume report leg working.
	manifest.WorktreeBase = " \t" + base + "\n"
	if err := store.Write(manifest); err != nil {
		t.Fatalf("rewrite manifest with worktree base: %v", err)
	}

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if got := updated.WorktreeBase; got != base {
		t.Fatalf("WorktreeBase = %q, want %q", got, base)
	}
	if updated.ReportPath == "" {
		t.Fatal("ReportPath is empty, want generated final report")
	}
	if got := len(sessionStore.sessions); got != 2 {
		t.Fatalf("saved sessions = %d, want 2", got)
	}
}

func TestResumeLegacyManifestInfersBaseFromProjectCheckout(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "legacy123", Slug: "legacy-base"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)

	worktreePath := manifest.WorktreePath
	featurePath := filepath.Join(worktreePath, "feature.txt")
	if err := os.WriteFile(featurePath, []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGitTest(t, worktreePath, "add", "feature.txt")
	runGitTest(t, worktreePath, "commit", "-m", "feature: add file")

	// Legacy manifest inference must use the project checkout, not the advanced worktree HEAD.
	if err := store.Write(manifest); err != nil {
		t.Fatalf("rewrite legacy manifest: %v", err)
	}
	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	projectHead := strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	worktreeHead := strings.TrimSpace(mustGitOutput(t, worktreePath, "rev-parse", "HEAD"))
	if updated.WorktreeBase != projectHead {
		t.Fatalf("WorktreeBase = %q, want project HEAD %q", updated.WorktreeBase, projectHead)
	}
	if updated.WorktreeBase == worktreeHead {
		t.Fatalf("WorktreeBase = worktree HEAD %q, want project checkout base", worktreeHead)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	if persisted.WorktreeBase != projectHead {
		t.Fatalf("persisted WorktreeBase = %q, want %q", persisted.WorktreeBase, projectHead)
	}
}

func TestOrchestratorResumeLegacyManifestReprovisionsMissingWorktreeFromProjectCheckout(t *testing.T) {
	projectRoot := setupLocalGitRepo(t)
	identity := RunIdentity{ID: "legacy-reprovision123", Slug: "legacy-reprovision"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	if got := manifest.WorktreeBase; got != "" {
		t.Fatalf("legacy WorktreeBase = %q, want empty", got)
	}
	projectHead := strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))

	if err := os.RemoveAll(manifest.WorktreePath); err != nil {
		t.Fatalf("RemoveAll worktree failed: %v", err)
	}
	runGitTest(t, projectRoot, "worktree", "prune")
	runGitTest(t, projectRoot, "branch", "-D", identity.BranchName())
	manifest.CurrentPhase = PhasePlan
	manifest.PhaseStatuses = map[Phase]PhaseStatus{
		PhasePlan:      PhaseStatusPending,
		PhaseImplement: PhaseStatusPending,
		PhaseReview:    PhaseStatusPending,
	}
	if err := store.Write(manifest); err != nil {
		t.Fatalf("rewrite legacy manifest: %v", err)
	}

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if got, want := updated.WorktreeBase, projectHead; got != want {
		t.Fatalf("WorktreeBase = %q, want project HEAD %q", got, want)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	if got, want := persisted.WorktreeBase, projectHead; got != want {
		t.Fatalf("persisted WorktreeBase = %q, want %q", got, want)
	}
	if got := strings.TrimSpace(mustGitOutput(t, manifest.WorktreePath, "rev-parse", "HEAD")); got != projectHead {
		t.Fatalf("reprovisioned worktree HEAD = %q, want project HEAD %q", got, projectHead)
	}
}

func setupResumeTestFixture(t *testing.T, projectRoot string, identity RunIdentity) (Manifest, *ManifestStore, *recordingSessionStore, *Orchestrator) {
	t.Helper()
	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})
	planningPath := identity.PlanningPath(worktree.Path)
	if err := os.MkdirAll(planningPath, 0o755); err != nil {
		t.Fatalf("MkdirAll planning path failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planningPath, "overview.md"), []byte("overview\n"), 0o644); err != nil {
		t.Fatalf("write overview failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planningPath, "plan.yaml"), []byte("steps: []\n"), 0o644); err != nil {
		t.Fatalf("write plan failed: %v", err)
	}

	manifest := Manifest{
		RunID:        identity.ID,
		Slug:         identity.Slug,
		Task:         "Build the parser",
		Branch:       identity.BranchName(),
		WorktreePath: worktree.Path,
		CurrentPhase: PhaseImplement,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusDone,
			PhaseImplement: PhaseStatusFailed,
			PhaseReview:    PhaseStatusPending,
		},
	}
	store := NewManifestStore(identity.ManifestPath(projectRoot))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	runnerFactory := &recordingRunnerFactory{
		planningPath: planningPath,
		runners: map[Phase]*phaseRunnerStub{
			PhasePlan:      {writePlan: true},
			PhaseImplement: {writePlan: true},
			PhaseReview:    {writePlan: true},
		},
	}
	sessionStore := &recordingSessionStore{}
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        configWithEffectiveModels("fallback-model", nil),
		SessionStore:  sessionStore,
		RunnerFactory: runnerFactory,
		ManifestStore: store,
		Events:        output.SinkFunc(func(output.Event) {}),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}
	return manifest, store, sessionStore, orch
}

func TestOrchestratorResumeRefusesLiveLock(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	planningPath := identity.PlanningPath(worktree.Path)
	if err := os.MkdirAll(planningPath, 0o755); err != nil {
		t.Fatalf("MkdirAll planning path failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planningPath, "overview.md"), []byte("overview\n"), 0o644); err != nil {
		t.Fatalf("write overview failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planningPath, "plan.yaml"), []byte("steps: []\n"), 0o644); err != nil {
		t.Fatalf("write plan failed: %v", err)
	}

	store := NewManifestStore(identity.ManifestPath(projectRoot))
	manifest := Manifest{
		RunID:        identity.ID,
		Slug:         identity.Slug,
		Task:         "Build the parser",
		Branch:       identity.BranchName(),
		WorktreePath: worktree.Path,
		CurrentPhase: PhasePlan,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusRunning,
			PhaseImplement: PhaseStatusPending,
			PhaseReview:    PhaseStatusPending,
		},
	}
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	lock, err := AcquireRunLock(projectRoot, identity)
	if err != nil {
		t.Fatalf("AcquireRunLock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = lock.Release()
	})

	sessionStore := &recordingSessionStore{}
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        configWithEffectiveModels("fallback-model", nil),
		SessionStore:  sessionStore,
		RunnerFactory: &recordingRunnerFactory{planningPath: planningPath, runners: map[Phase]*phaseRunnerStub{}},
		ManifestStore: store,
		Events:        output.SinkFunc(func(output.Event) {}),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}

	_, err = orch.Resume(context.Background())
	if !errors.Is(err, errLockHeld) {
		t.Fatalf("Resume error = %v, want errLockHeld", err)
	}
}

func TestOrchestratorResumeReprovisionsMissingWorktreeFromStoredBase(t *testing.T) {
	projectRoot := setupLocalGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	base := worktree.StartPoint
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	store := NewManifestStore(identity.ManifestPath(projectRoot))
	manifest := Manifest{
		RunID:        identity.ID,
		Slug:         identity.Slug,
		Task:         "Build the parser",
		Branch:       identity.BranchName(),
		WorktreePath: worktree.Path,
		WorktreeBase: base,
		CurrentPhase: PhasePlan,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusPending,
			PhaseImplement: PhaseStatusPending,
			PhaseReview:    PhaseStatusPending,
		},
	}
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	if err := os.RemoveAll(worktree.Path); err != nil {
		t.Fatalf("RemoveAll worktree failed: %v", err)
	}
	runGitTest(t, projectRoot, "worktree", "prune")
	runGitTest(t, projectRoot, "branch", "-D", identity.BranchName())
	runGitTest(t, projectRoot, "checkout", "-b", "advance")
	if err := os.WriteFile(filepath.Join(projectRoot, "advanced.txt"), []byte("advanced\n"), 0o644); err != nil {
		t.Fatalf("write advanced file failed: %v", err)
	}
	runGitTest(t, projectRoot, "add", "advanced.txt")
	runGitTest(t, projectRoot, "commit", "-m", "test: advance project checkout")
	projectHead := strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if projectHead == base {
		t.Fatal("project checkout did not advance beyond stored base")
	}

	sessionStore := &recordingSessionStore{}
	runnerFactory := &recordingRunnerFactory{
		planningPath: identity.PlanningPath(worktree.Path),
		runners: map[Phase]*phaseRunnerStub{
			PhasePlan:      {writePlan: true},
			PhaseImplement: {writePlan: true},
			PhaseReview:    {writePlan: true},
		},
	}
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        configWithEffectiveModels("fallback-model", nil),
		SessionStore:  sessionStore,
		RunnerFactory: runnerFactory,
		ManifestStore: store,
		Events:        output.SinkFunc(func(output.Event) {}),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if got, want := updated.WorktreeBase, base; got != want {
		t.Fatalf("WorktreeBase = %q, want stored base %q", got, want)
	}
	if got := strings.TrimSpace(mustGitOutput(t, worktree.Path, "rev-parse", "HEAD")); got != base {
		t.Fatalf("reprovisioned worktree HEAD = %q, want stored base %q", got, base)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, "advanced.txt")); !os.IsNotExist(err) {
		t.Fatalf("reprovisioned worktree contains advanced checkout file: %v", err)
	}
}

func TestListResumableRuns(t *testing.T) {
	projectRoot := setupGitRepo(t)
	now := time.Now().UTC()

	writeManifest := func(identity RunIdentity, manifest Manifest) {
		t.Helper()
		stateDir := identity.StateDir(projectRoot)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("MkdirAll state dir failed: %v", err)
		}
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatalf("marshal manifest failed: %v", err)
		}
		if err := os.WriteFile(identity.ManifestPath(projectRoot), data, 0o644); err != nil {
			t.Fatalf("write manifest failed: %v", err)
		}
	}

	staleIdentity := RunIdentity{ID: "aaa111", Slug: "alpha"}
	writeManifest(staleIdentity, Manifest{
		RunID:        staleIdentity.ID,
		Slug:         staleIdentity.Slug,
		Task:         "Alpha",
		Branch:       staleIdentity.BranchName(),
		WorktreePath: staleIdentity.WorktreePath(projectRoot),
		CurrentPhase: PhaseImplement,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusDone,
			PhaseImplement: PhaseStatusFailed,
			PhaseReview:    PhaseStatusPending,
		},
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-90 * time.Minute),
	})
	staleLockPath := staleIdentity.LockPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(staleLockPath), 0o755); err != nil {
		t.Fatalf("MkdirAll stale lock dir failed: %v", err)
	}
	staleLock := LockRecord{
		Owner:      "stale-owner",
		AcquiredAt: now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	}
	staleLockData, err := json.Marshal(staleLock)
	if err != nil {
		t.Fatalf("marshal stale lock failed: %v", err)
	}
	if err := os.WriteFile(staleLockPath, staleLockData, 0o644); err != nil {
		t.Fatalf("write stale lock failed: %v", err)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(staleLockPath, old, old); err != nil {
		t.Fatalf("Chtimes stale lock failed: %v", err)
	}

	absentIdentity := RunIdentity{ID: "bbb222", Slug: "beta"}
	writeManifest(absentIdentity, Manifest{
		RunID:        absentIdentity.ID,
		Slug:         absentIdentity.Slug,
		Task:         "Beta",
		Branch:       absentIdentity.BranchName(),
		WorktreePath: absentIdentity.WorktreePath(projectRoot),
		CurrentPhase: PhasePlan,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusDone,
			PhaseImplement: PhaseStatusRunning,
			PhaseReview:    PhaseStatusPending,
		},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-30 * time.Minute),
	})

	completeIdentity := RunIdentity{ID: "ccc333", Slug: "gamma"}
	writeManifest(completeIdentity, Manifest{
		RunID:        completeIdentity.ID,
		Slug:         completeIdentity.Slug,
		Task:         "Gamma",
		Branch:       completeIdentity.BranchName(),
		WorktreePath: completeIdentity.WorktreePath(projectRoot),
		CurrentPhase: PhaseReview,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusDone,
			PhaseImplement: PhaseStatusDone,
			PhaseReview:    PhaseStatusDone,
		},
		CreatedAt: now.Add(-3 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Minute),
	})

	liveIdentity := RunIdentity{ID: "ddd444", Slug: "delta"}
	writeManifest(liveIdentity, Manifest{
		RunID:        liveIdentity.ID,
		Slug:         liveIdentity.Slug,
		Task:         "Delta",
		Branch:       liveIdentity.BranchName(),
		WorktreePath: liveIdentity.WorktreePath(projectRoot),
		CurrentPhase: PhaseImplement,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusDone,
			PhaseImplement: PhaseStatusFailed,
			PhaseReview:    PhaseStatusPending,
		},
		CreatedAt: now.Add(-4 * time.Hour),
		UpdatedAt: now.Add(-10 * time.Minute),
	})
	liveLock, err := AcquireRunLock(projectRoot, liveIdentity)
	if err != nil {
		t.Fatalf("AcquireRunLock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = liveLock.Release()
	})

	runs, err := ListResumableRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	if got, want := len(runs), 2; got != want {
		t.Fatalf("ListResumableRuns len = %d, want %d", got, want)
	}
	if got, want := runs[0].RunID, absentIdentity.ID; got != want {
		t.Fatalf("runs[0].RunID = %q, want %q", got, want)
	}
	if got, want := runs[0].LockState, lockStateAbsent.String(); got != want {
		t.Fatalf("runs[0].LockState = %q, want %q", got, want)
	}
	if got, want := runs[0].ResumePhase, PhaseImplement; got != want {
		t.Fatalf("runs[0].ResumePhase = %q, want %q", got, want)
	}
	if got := runs[0].Status; !strings.Contains(got, "resume at implement") {
		t.Fatalf("runs[0].Status = %q, want resume phase", got)
	}
	if got, want := runs[1].RunID, staleIdentity.ID; got != want {
		t.Fatalf("runs[1].RunID = %q, want %q", got, want)
	}
	if got, want := runs[1].LockState, lockStateStale.String(); got != want {
		t.Fatalf("runs[1].LockState = %q, want %q", got, want)
	}
}
