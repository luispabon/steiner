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

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
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
		// Match the new-run marker Run writes so the run is not legacy.
		FinalizationVersion: finalizationVersion,
		ReportState:         reportStatePending,
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

func TestOrchestratorResumeReturnsLockReleaseError(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "resume-release", Slug: "resume-release"}
	_, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	releaseErr := errors.New("resume release failed")
	orch.deps.RunLockFactory = testRunLockFactory(releaseErr)
	_, err := orch.Resume(context.Background())
	if !errors.Is(err, releaseErr) {
		t.Fatalf("Resume error = %v, want release error", err)
	}
	if _, storeErr := store.Read(); storeErr != nil {
		t.Fatalf("manifest store read failed after Resume: %v", storeErr)
	}
}

func TestOrchestratorResumeJoinsPrimaryAndLockReleaseErrors(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "resume-primary", Slug: "resume-primary"}
	_, _, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	releaseErr := errors.New("resume release failed")
	orch.deps.RunLockFactory = testRunLockFactory(releaseErr)
	primaryErr := errors.New("resume primary failed")
	orch.deps.RunnerFactory = PhaseRunnerFactoryFunc(func(context.Context, Phase, string, tool.ApprovalResponder, config.AdvisorConfig) (PhaseRunner, error) {
		return nil, primaryErr
	})
	_, err := orch.Resume(context.Background())
	if !errors.Is(err, releaseErr) {
		t.Fatalf("Resume error = %v, want release error", err)
	}
	if !errors.Is(err, primaryErr) {
		t.Fatalf("Resume error = %v, want primary error", err)
	}
}

func testRunLockFactory(releaseErr error) func(string, RunIdentity) (*RunLock, error) {
	return func(projectRoot string, runIdentity RunIdentity) (*RunLock, error) {
		lockPath := runIdentity.LockPath(projectRoot)
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(lockPath, []byte("test"), 0o644); err != nil {
			return nil, err
		}
		return &RunLock{path: lockPath, releaseFunc: func() error { return releaseErr }}, nil
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
		// Match the new-run marker Run writes so the fixture is a versioned
		// manifest rather than a legacy one.
		FinalizationVersion: finalizationVersion,
		ReportState:         reportStatePending,
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
		// Match the new-run marker Run writes so the run is not legacy.
		FinalizationVersion: finalizationVersion,
		ReportState:         reportStatePending,
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
		// A settled run: written report and a recorded closeout, so it is not
		// listed as resumable.
		FinalizationVersion: finalizationVersion,
		ReportState:         reportStateWritten,
		ReportPath:          filepath.Join(completeIdentity.PlanningPath(completeIdentity.WorktreePath(projectRoot)), finalReportFileName),
		CloseoutState:       closeoutStateCreated,
		CreatedAt:           now.Add(-3 * time.Hour),
		UpdatedAt:           now.Add(-2 * time.Minute),
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

type loadingSessionStore struct {
	*recordingSessionStore
	seeded map[string]session.Session
}

func (s loadingSessionStore) Load(id string) (session.Session, error) {
	if sess, ok := s.seeded[id]; ok {
		return sess, nil
	}
	return session.Session{}, os.ErrNotExist
}

func TestResumeReportsOrphanedWorktreesAndRestartsPhase(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "orphan123", Slug: "orphan-report"}
	manifest, store, recording, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseSessionIDs = map[Phase]string{PhaseImplement: "old-session"}
	if err := store.Write(manifest); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
	orch.deps.SessionStore = loadingSessionStore{
		recordingSessionStore: recording,
		seeded: map[string]session.Session{"old-session": {ID: "old-session", SubAgentLedger: []agent.SubAgentLedgerEntry{
			{AgentID: "a1", AgentType: "code", ParentCallID: "c1", WorktreePath: "/wt/a1"},
			{AgentID: "a2", AgentType: "research", ParentCallID: "c2"},
		}}},
	}
	var messages []string
	orch.deps.Events = output.SinkFunc(func(event output.Event) {
		if payload, ok := event.Payload.(output.PhaseIndicatorEvent); ok && payload.State == phaseIndicatorOrphaned {
			messages = append(messages, payload.Message)
		}
	})

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "/wt/a1") || !strings.Contains(messages[0], "a1") {
		t.Fatalf("orphan reports = %q, want one entry naming a1 and /wt/a1", messages)
	}
	if got := updated.PhaseStatuses[PhaseImplement]; got != PhaseStatusDone {
		t.Fatalf("implement status = %q, want restarted phase done", got)
	}
	if got := updated.PhaseSessionIDs[PhaseImplement]; got == "old-session" || got == "" {
		t.Fatalf("implement session id = %q, want a fresh session", got)
	}
}

// finalizedResumeFixture provisions a run whose phases are all done and whose
// final report is written, so resume starts at finalization. auto_pr is enabled
// and origin points at GitHub so closeout reaches the stubbed runner.
func finalizedResumeFixture(t *testing.T, identity RunIdentity) (*ManifestStore, *Orchestrator) {
	t.Helper()
	projectRoot := setupGitRepo(t)
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	manifest.ReportState = reportStateWritten
	manifest.ReportPath = filepath.Join(identity.PlanningPath(manifest.WorktreePath), finalReportFileName)
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	return store, orch
}

// markCloseoutUnknown records an unresolved closeout outcome for the run.
func markCloseoutUnknown(t *testing.T, store *ManifestStore) {
	t.Helper()
	manifest, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	manifest.CloseoutState = closeoutStateUnknown
	manifest.CloseoutIntent = ""
	manifest.CloseoutNote = "closeout outcome unknown; manual resolution required"
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
}

func TestResumeRefusesUnknownCloseoutWithoutResolution(t *testing.T) {
	identity := RunIdentity{ID: "unkn0wn1", Slug: "unknown-closeout"}
	store, orch := finalizedResumeFixture(t, identity)
	markCloseoutUnknown(t, store)
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	_, err := orch.Resume(context.Background())
	if err == nil {
		t.Fatal("Resume on an unknown closeout outcome returned nil error")
	}
	// finalizeRun's manual-resolution error names the only accepted resolution
	// path, so the operator must record the outcome or authorise a retry.
	if !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "steiner oneshot --resolve") {
		t.Fatalf("Resume error = %q, want the manual-resolution error", err)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0", *closeoutCalls)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateUnknown {
		t.Errorf("CloseoutState = %q, want it to stay %q", persisted.CloseoutState, closeoutStateUnknown)
	}
	if persisted.CloseoutIntent != "" {
		t.Errorf("CloseoutIntent = %q, want it to stay empty", persisted.CloseoutIntent)
	}
}

func TestResumeRegeneratesPendingReportForUnknownCloseout(t *testing.T) {
	identity := RunIdentity{ID: "unkn0wn2", Slug: "unknown-report"}
	store, orch := finalizedResumeFixture(t, identity)
	loading, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	loading.CloseoutState = closeoutStateUnknown
	loading.CloseoutIntent = ""
	loading.ReportState = reportStatePending
	loading.ReportPath = ""
	if err := store.Write(loading); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	if _, err := orch.Resume(context.Background()); err == nil {
		t.Fatal("Resume with an unknown outcome and a pending report returned nil error")
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.ReportState != reportStateWritten || persisted.ReportPath == "" {
		t.Errorf("report state = %q path = %q, want the report written", persisted.ReportState, persisted.ReportPath)
	}
	if _, statErr := os.Stat(persisted.ReportPath); statErr != nil {
		t.Errorf("stat regenerated report: %v", statErr)
	}
	if persisted.CloseoutState != closeoutStateUnknown {
		t.Errorf("CloseoutState = %q, want %q", persisted.CloseoutState, closeoutStateUnknown)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0", *closeoutCalls)
	}
}

func TestResumeRetriesAfterAuthorisedResolution(t *testing.T) {
	identity := RunIdentity{ID: "auth0rized", Slug: "authorised-retry"}
	store, orch := finalizedResumeFixture(t, identity)
	loading, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	loading.CloseoutState = closeoutStateRetryAuthorized
	loading.CloseoutIntent = ""
	if err := store.Write(loading); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume after an authorised retry failed: %v", err)
	}
	if *closeoutCalls != 1 {
		t.Errorf("closeout calls = %d, want 1", *closeoutCalls)
	}
	if updated.CloseoutState != closeoutStateCreated {
		t.Errorf("CloseoutState = %q, want %q", updated.CloseoutState, closeoutStateCreated)
	}
	if updated.CloseoutURL == "" {
		t.Error("CloseoutURL is empty after a successful retry")
	}
}

func TestResolveCloseoutRecordsURLClearsAmbiguity(t *testing.T) {
	identity := RunIdentity{ID: "res0lve1", Slug: "resolve-url"}
	store, orch := finalizedResumeFixture(t, identity)
	markCloseoutUnknown(t, store)
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	const recorded = "https://github.com/owner/repo/pull/9"
	resolved, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRecordURL, URL: "  " + recorded + "\n"})
	if err != nil {
		t.Fatalf("ResolveCloseout failed: %v", err)
	}
	if resolved.CloseoutState != closeoutStateCreated {
		t.Errorf("CloseoutState = %q, want %q", resolved.CloseoutState, closeoutStateCreated)
	}
	if resolved.CloseoutURL != recorded {
		t.Errorf("CloseoutURL = %q, want %q", resolved.CloseoutURL, recorded)
	}
	if resolved.CloseoutIntent != "" {
		t.Errorf("CloseoutIntent = %q, want it cleared", resolved.CloseoutIntent)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateCreated || persisted.CloseoutURL != recorded {
		t.Errorf("persisted closeout state = %q url = %q, want created with the recorded url", persisted.CloseoutState, persisted.CloseoutURL)
	}

	_, err = orch.Resume(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already complete") {
		t.Fatalf("Resume after a recorded resolution error = %v, want already complete", err)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0", *closeoutCalls)
	}
}

func TestResolveCloseoutAuthorisesOneRetry(t *testing.T) {
	identity := RunIdentity{ID: "res0lve2", Slug: "resolve-retry"}
	store, orch := finalizedResumeFixture(t, identity)
	markCloseoutUnknown(t, store)
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	resolved, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRetry})
	if err != nil {
		t.Fatalf("ResolveCloseout failed: %v", err)
	}
	if resolved.CloseoutState != closeoutStateRetryAuthorized {
		t.Fatalf("CloseoutState = %q, want %q", resolved.CloseoutState, closeoutStateRetryAuthorized)
	}
	if *closeoutCalls != 0 {
		t.Fatalf("closeout calls after resolving = %d, want 0", *closeoutCalls)
	}

	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume after an authorised retry failed: %v", err)
	}
	if *closeoutCalls != 1 {
		t.Fatalf("closeout calls = %d, want exactly 1", *closeoutCalls)
	}
	if updated.CloseoutState != closeoutStateCreated {
		t.Errorf("CloseoutState = %q, want %q", updated.CloseoutState, closeoutStateCreated)
	}

	if _, err := orch.Resume(context.Background()); err == nil {
		t.Fatal("second Resume after a created closeout returned nil error")
	}
	if *closeoutCalls != 1 {
		t.Errorf("closeout calls = %d, want no further call", *closeoutCalls)
	}
}

func TestResolveCloseoutRejectsResolvedRun(t *testing.T) {
	identity := RunIdentity{ID: "res0lve3", Slug: "resolve-resolved"}
	store, orch := finalizedResumeFixture(t, identity)
	manifest, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	manifest.CloseoutState = closeoutStateCreated
	manifest.CloseoutURL = "https://github.com/owner/repo/pull/7"
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	_, err = ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRetry})
	if err == nil {
		t.Fatal("ResolveCloseout on a resolved run returned nil error")
	}
	if !strings.Contains(err.Error(), identity.ID) || !strings.Contains(err.Error(), closeoutStateCreated) {
		t.Fatalf("ResolveCloseout error = %q, want it to name %s and state %q", err, identity.ID, closeoutStateCreated)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateCreated {
		t.Errorf("CloseoutState = %q, want it to stay %q", persisted.CloseoutState, closeoutStateCreated)
	}
}

func TestResolveCloseoutRejectsInvalidURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "unparseable", url: "not-a-url"},
		{name: "unsupported scheme", url: "ftp://example.com/x"},
		{name: "empty", url: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			identity := RunIdentity{ID: "r3solvebad", Slug: "resolve-bad-url"}
			store, orch := finalizedResumeFixture(t, identity)
			markCloseoutUnknown(t, store)

			_, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRecordURL, URL: c.url})
			if err == nil {
				t.Fatalf("ResolveCloseout with URL %q returned nil error", c.url)
			}
			if !strings.HasPrefix(err.Error(), "resolve closeout:") {
				t.Errorf("ResolveCloseout error = %q, want a resolve closeout prefix", err)
			}
			persisted, readErr := store.Read()
			if readErr != nil {
				t.Fatalf("store.Read failed: %v", readErr)
			}
			if persisted.CloseoutState != closeoutStateUnknown {
				t.Errorf("CloseoutState = %q, want it to stay %q", persisted.CloseoutState, closeoutStateUnknown)
			}
		})
	}
}

func TestResolveCloseoutRejectsConcurrentLock(t *testing.T) {
	identity := RunIdentity{ID: "res0lve4", Slug: "resolve-lock"}
	store, orch := finalizedResumeFixture(t, identity)
	markCloseoutUnknown(t, store)
	orch.deps.RunLockFactory = func(string, RunIdentity) (*RunLock, error) {
		return nil, errLockHeld
	}

	_, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRetry})
	if !errors.Is(err, errLockHeld) {
		t.Fatalf("ResolveCloseout error = %v, want errLockHeld", err)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateUnknown {
		t.Errorf("CloseoutState = %q, want it to stay %q", persisted.CloseoutState, closeoutStateUnknown)
	}
}

func TestResolveCloseoutRejectsVersionedRunAwaitingNothing(t *testing.T) {
	identity := RunIdentity{ID: "res0lve5", Slug: "resolve-nothing"}
	store, orch := finalizedResumeFixture(t, identity)

	_, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRetry})
	if err == nil {
		t.Fatal("ResolveCloseout on a versioned run with nothing pending returned nil error")
	}
	if !strings.Contains(err.Error(), identity.ID) {
		t.Errorf("ResolveCloseout error = %q, want it to name %s", err, identity.ID)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != "" || persisted.CloseoutIntent != "" {
		t.Errorf("closeout state = %q intent = %q, want both unchanged", persisted.CloseoutState, persisted.CloseoutIntent)
	}
}

func TestResolveCloseoutRejectsIncompleteRun(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "res0lve6", Slug: "resolve-incomplete"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	// A legacy run killed mid-phase: no finalization marker and no closeout state,
	// so its closeout outcome looks unknown even though none ever ran.
	manifest.CurrentPhase = PhaseImplement
	manifest.PhaseStatuses = map[Phase]PhaseStatus{
		PhasePlan:      PhaseStatusDone,
		PhaseImplement: PhaseStatusRunning,
		PhaseReview:    PhaseStatusPending,
	}
	manifest.FinalizationVersion = 0
	manifest.ReportState = ""
	manifest.ReportPath = ""
	manifest.CloseoutState = ""
	manifest.CloseoutIntent = ""
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/9", "", nil
	})

	_, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRecordURL, URL: "https://github.com/owner/repo/pull/9"})
	if err == nil {
		t.Fatal("ResolveCloseout on a run with an incomplete phase returned nil error")
	}
	if !strings.Contains(err.Error(), string(PhaseImplement)) || !strings.Contains(err.Error(), "--resume") {
		t.Errorf("ResolveCloseout error = %q, want it to name the incomplete phase %q and --resume", err, PhaseImplement)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.FinalizationVersion != 0 || persisted.CloseoutState != "" || persisted.CloseoutURL != "" {
		t.Errorf("manifest version = %d state = %q url = %q, want the resolution to leave it unchanged", persisted.FinalizationVersion, persisted.CloseoutState, persisted.CloseoutURL)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0", *closeoutCalls)
	}

	// The refused resolution must not have settled the run: resume still runs the
	// remaining phases instead of reporting it complete.
	resumed, resumeErr := orch.Resume(context.Background())
	if resumeErr != nil && strings.Contains(resumeErr.Error(), "already complete") {
		t.Fatalf("Resume after a refused resolution error = %v, want it to continue the phases", resumeErr)
	}
	if got := resumed.PhaseStatuses[PhaseReview]; got != PhaseStatusDone {
		t.Errorf("resumed review status = %q, want the remaining phases finished", got)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 with auto_pr disabled", *closeoutCalls)
	}
}

func TestResolveCloseoutRejectsPendingReport(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "res0lve7", Slug: "resolve-no-report"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	// Every phase is done, but a legacy run never wrote its final report.
	manifest.CurrentPhase = PhaseReview
	manifest.PhaseStatuses = allPhasesDone()
	manifest.FinalizationVersion = 0
	manifest.ReportState = ""
	manifest.ReportPath = ""
	manifest.CloseoutState = ""
	manifest.CloseoutIntent = ""
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	_, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRecordURL, URL: "https://github.com/owner/repo/pull/9"})
	if err == nil {
		t.Fatal("ResolveCloseout on a run without a written report returned nil error")
	}
	if !strings.Contains(err.Error(), "final report") || !strings.Contains(err.Error(), "--resume") {
		t.Errorf("ResolveCloseout error = %q, want it to name the missing final report and --resume", err)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.FinalizationVersion != 0 || persisted.CloseoutState != "" || persisted.CloseoutURL != "" || persisted.ReportPath != "" {
		t.Errorf("manifest version = %d state = %q url = %q report = %q, want the resolution to leave it unchanged", persisted.FinalizationVersion, persisted.CloseoutState, persisted.CloseoutURL, persisted.ReportPath)
	}
}

func TestCloseoutExternalAttemptGatesOnPreRunState(t *testing.T) {
	cases := []struct {
		name       string
		autoPR     bool
		completion bool
		want       bool
		wantNote   string
	}{
		{name: "auto pr disabled", autoPR: false, completion: true, want: false, wantNote: "oneshot.auto_pr is disabled"},
		{name: "review did not pass", autoPR: true, completion: false, want: false, wantNote: "review did not pass"},
		{name: "auto pr enabled and review passed", autoPR: true, completion: true, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := configWithEffectiveModels("fallback-model", nil)
			cfg.OneShot.AutoPR = c.autoPR
			attempts, note := closeoutExternalAttempt(cfg, FinalReport{Completion: c.completion})
			if attempts != c.want {
				t.Errorf("closeoutExternalAttempt() = %t, want %t", attempts, c.want)
			}
			if note != c.wantNote {
				t.Errorf("closeoutExternalAttempt() note = %q, want %q", note, c.wantNote)
			}
		})
	}
}

func TestFinalizeRunSkipsCloseoutIntentWhenNoExternalCallIsPossible(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "sk1pintent", Slug: "skip-intent"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	// oneshot.auto_pr stays disabled, so closeout reaches no provider runner and
	// an interrupted finalization cannot have opened a PR/MR.
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/9", "", nil
	})

	if err := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath)); err != nil {
		t.Fatalf("finalizeRun failed: %v", err)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 with auto_pr disabled", *closeoutCalls)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateSkipped {
		t.Errorf("CloseoutState = %q, want %q", persisted.CloseoutState, closeoutStateSkipped)
	}
	if persisted.CloseoutNote != "oneshot.auto_pr is disabled" {
		t.Errorf("CloseoutNote = %q, want the auto_pr note", persisted.CloseoutNote)
	}
	if persisted.CloseoutIntent != "" {
		t.Errorf("CloseoutIntent = %q, want none for a skipped closeout", persisted.CloseoutIntent)
	}
	data, err := os.ReadFile(identity.ManifestPath(projectRoot))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(data), "closeout_intent") {
		t.Errorf("persisted manifest = %s, want no closeout intent for a closeout that cannot reach a provider", data)
	}
}
