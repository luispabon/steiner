package oneshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// loadPlanFixtures reads the deterministic planning artifacts the stubbed plan
// phase writes into the worktree, keeping the engine test free of large inline
// literals (see testdata/oneshot/plan).
func loadPlanFixtures(t *testing.T) (overview, plan []byte) {
	t.Helper()
	var err error
	overview, err = os.ReadFile(filepath.Join("testdata", "plan", "overview.md"))
	if err != nil {
		t.Fatalf("read overview fixture: %v", err)
	}
	plan, err = os.ReadFile(filepath.Join("testdata", "plan", "plan.yaml"))
	if err != nil {
		t.Fatalf("read plan fixture: %v", err)
	}
	return overview, plan
}

// fixturePhaseRunner is a deterministic, network-free phase runner. The plan
// phase writes the testdata planning artifacts; other phases succeed unless told
// to block on cancellation (used to exercise the SIGINT path).
type fixturePhaseRunner struct {
	phase             Phase
	planningPath      string
	overview          []byte
	plan              []byte
	skipPlanArtifacts bool
	blockOnCancel     bool
	started           chan struct{}
}

func (r fixturePhaseRunner) RunPhase(ctx context.Context, conversation []agent.Message, _ []string, _ func() []agent.SteerMessage) (RunResult, error) {
	if !r.skipPlanArtifacts {
		if err := os.MkdirAll(r.planningPath, 0o755); err != nil {
			return RunResult{}, err
		}
		switch r.phase {
		case PhasePlan:
			if err := os.WriteFile(filepath.Join(r.planningPath, "overview.md"), r.overview, 0o644); err != nil {
				return RunResult{}, err
			}
			if err := os.WriteFile(filepath.Join(r.planningPath, "plan.yaml"), r.plan, 0o644); err != nil {
				return RunResult{}, err
			}
		case PhaseImplement:
			if err := os.WriteFile(filepath.Join(r.planningPath, "execution.md"), []byte("execution\n"), 0o644); err != nil {
				return RunResult{}, err
			}
		case PhaseReview:
			if err := os.WriteFile(filepath.Join(r.planningPath, "review.md"), []byte("review\n"), 0o644); err != nil {
				return RunResult{}, err
			}
		}
	}
	if r.blockOnCancel {
		if r.started != nil {
			select {
			case r.started <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return RunResult{Conversation: conversation}, ctx.Err()
	}
	return RunResult{Conversation: conversation, Reply: string(r.phase)}, nil
}

type fixtureRunnerFactory struct {
	planningPath      string
	overview          []byte
	plan              []byte
	skipPlanArtifacts bool
	blockPhase        Phase
	started           chan struct{}
}

func (f *fixtureRunnerFactory) NewPhaseRunner(_ context.Context, phase Phase, _ string, _ tool.ApprovalResponder, _ config.AdvisorConfig) (PhaseRunner, error) {
	return fixturePhaseRunner{
		phase:             phase,
		planningPath:      f.planningPath,
		overview:          f.overview,
		plan:              f.plan,
		skipPlanArtifacts: f.skipPlanArtifacts,
		blockOnCancel:     f.blockPhase != "" && phase == f.blockPhase,
		started:           f.started,
	}, nil
}

// TestOneshotEndToEndHappyPathProducesReport drives plan -> implement -> review
// against the fixture runner, then assembles and writes the final report from
// the resulting manifest + real worktree git state.
func TestOneshotEndToEndHappyPathProducesReport(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity, err := NewRunIdentity("integration happy path")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}
	t.Cleanup(func() { _ = CleanupWorktree(context.Background(), projectRoot, identity) })

	overview, plan := loadPlanFixtures(t)
	planningPath := identity.PlanningPath(identity.WorktreePath(projectRoot))
	sessionStore := &recordingSessionStore{}
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "integration happy path",
		Config:        configWithEffectiveModels("m", nil),
		SessionStore:  sessionStore,
		RunnerFactory: &fixtureRunnerFactory{planningPath: planningPath, overview: overview, plan: plan},
		ManifestStore: NewManifestStore(identity.ManifestPath(projectRoot)),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	manifest, err := orch.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, phase := range phaseOrder() {
		if got := manifest.PhaseStatuses[phase]; got != PhaseStatusDone {
			t.Fatalf("phase %s status = %q, want %q", phase, got, PhaseStatusDone)
		}
	}
	if got, want := len(sessionStore.sessions), 3; got != want {
		t.Fatalf("persisted sessions = %d, want %d", got, want)
	}

	report, err := GenerateFinalReport(context.Background(), manifest, ReviewOutcome{
		Summary:      "all checks green",
		Passed:       true,
		ChangesMade:  []string{"added parser"},
		FilesChanged: []string{"parser.go"},
	})
	if err != nil {
		t.Fatalf("GenerateFinalReport: %v", err)
	}
	if got, want := report.Status, "success"; got != want {
		t.Fatalf("report status = %q, want %q", got, want)
	}
	if !report.Completion {
		t.Fatal("report completion = false, want true")
	}
	if got, want := len(report.PhaseSessions), 3; got != want {
		t.Fatalf("report phase sessions = %d, want %d", got, want)
	}
	if report.Failure != nil {
		t.Fatalf("happy-path report has failure section: %#v", report.Failure)
	}
	if strings.TrimSpace(report.ReportPath) == "" {
		t.Fatal("report path is empty")
	}
	if _, err := os.Stat(report.ReportPath); err != nil {
		t.Fatalf("report not written to disk: %v", err)
	}
	if !strings.HasPrefix(report.ReportPath, planningPath) {
		t.Fatalf("report path %q not under planning path %q", report.ReportPath, planningPath)
	}
}

// TestOneshotBoundaryFailureStopsCleanly verifies a missing planning artifact
// stops the run with a BoundaryError, releases the lock, and still yields a
// failure-shaped report describing what was left behind.
func TestOneshotBoundaryFailureStopsCleanly(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity, err := NewRunIdentity("integration boundary")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}
	t.Cleanup(func() { _ = CleanupWorktree(context.Background(), projectRoot, identity) })

	planningPath := identity.PlanningPath(identity.WorktreePath(projectRoot))
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "integration boundary",
		Config:        configWithEffectiveModels("m", nil),
		SessionStore:  &recordingSessionStore{},
		RunnerFactory: &fixtureRunnerFactory{planningPath: planningPath, skipPlanArtifacts: true},
		ManifestStore: NewManifestStore(identity.ManifestPath(projectRoot)),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	manifest, runErr := orch.Run(context.Background())
	var boundaryErr *BoundaryError
	if !errors.As(runErr, &boundaryErr) {
		t.Fatalf("Run error = %v, want BoundaryError", runErr)
	}
	if got := manifest.PhaseStatuses[PhasePlan]; got != PhaseStatusFailed {
		t.Fatalf("plan status = %q, want %q", got, PhaseStatusFailed)
	}
	if _, err := os.Stat(identity.LockPath(projectRoot)); !os.IsNotExist(err) {
		t.Fatalf("lock not released after boundary failure: %v", err)
	}

	report, err := GenerateFinalReport(context.Background(), manifest, ReviewOutcome{
		Passed:            false,
		Attempted:         "plan phase",
		Blocked:           "missing plan.yaml",
		ChangesLeftBehind: false,
	})
	if err != nil {
		t.Fatalf("GenerateFinalReport (failure): %v", err)
	}
	if got, want := report.Status, "failure"; got != want {
		t.Fatalf("report status = %q, want %q", got, want)
	}
	if report.Failure == nil {
		t.Fatal("failure report missing failure section")
	}
	if got, want := report.Failure.Blocked, "missing plan.yaml"; got != want {
		t.Fatalf("failure blocked = %q, want %q", got, want)
	}
}

// TestOneshotInterruptThenResumeCompletes interrupts a run mid-implement, then
// resumes from the manifest and drives the run to completion without re-running
// the already-complete plan phase.
func TestOneshotInterruptThenResumeCompletes(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity, err := NewRunIdentity("integration resume")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}
	t.Cleanup(func() { _ = CleanupWorktree(context.Background(), projectRoot, identity) })

	overview, plan := loadPlanFixtures(t)
	planningPath := identity.PlanningPath(identity.WorktreePath(projectRoot))
	manifestStore := NewManifestStore(identity.ManifestPath(projectRoot))

	started := make(chan struct{}, 1)
	var signalCancel context.CancelFunc
	firstOrch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "integration resume",
		Config:        configWithEffectiveModels("m", nil),
		SessionStore:  &recordingSessionStore{},
		RunnerFactory: &fixtureRunnerFactory{planningPath: planningPath, overview: overview, plan: plan, blockPhase: PhaseImplement, started: started},
		ManifestStore: manifestStore,
		InterruptFactory: func(parent context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(parent)
			signalCancel = cancel
			return ctx, cancel
		},
	})
	if err != nil {
		t.Fatalf("NewOrchestrator (first): %v", err)
	}

	runDone := make(chan error, 1)
	go func() {
		_, runErr := firstOrch.Run(context.Background())
		runDone <- runErr
	}()
	<-started      // implement phase has entered RunPhase and is blocking on cancel
	signalCancel() // SIGINT equivalent

	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first run error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(identity.LockPath(projectRoot)); !os.IsNotExist(err) {
		t.Fatalf("lock not released after interrupt: %v", err)
	}
	interrupted, err := manifestStore.Read()
	if err != nil {
		t.Fatalf("read manifest after interrupt: %v", err)
	}
	if got := interrupted.PhaseStatuses[PhasePlan]; got != PhaseStatusDone {
		t.Fatalf("plan status after interrupt = %q, want %q", got, PhaseStatusDone)
	}
	if got := interrupted.PhaseStatuses[PhaseReview]; got == PhaseStatusDone {
		t.Fatal("review marked done before resume")
	}

	resumeOrch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "integration resume",
		Config:        configWithEffectiveModels("m", nil),
		SessionStore:  &recordingSessionStore{},
		RunnerFactory: &fixtureRunnerFactory{planningPath: planningPath, overview: overview, plan: plan},
		ManifestStore: manifestStore,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator (resume): %v", err)
	}
	resumed, err := resumeOrch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for _, phase := range phaseOrder() {
		if got := resumed.PhaseStatuses[phase]; got != PhaseStatusDone {
			t.Fatalf("post-resume phase %s status = %q, want %q", phase, got, PhaseStatusDone)
		}
	}
}

// TestOneshotSameIDSecondRunBlockedByLock confirms the per-run lock is the
// safety primitive for same-id concurrency: while one run holds the lock, a
// second acquisition for the same id is refused, and the lock is reclaimable
// once released.
func TestOneshotSameIDSecondRunBlockedByLock(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity, err := NewRunIdentity("integration same id")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}

	lock, err := AcquireRunLock(projectRoot, identity)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	if _, err := AcquireRunLock(projectRoot, identity); err == nil {
		t.Fatal("second AcquireRunLock succeeded while lock held, want refusal")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	reacquired, err := AcquireRunLock(projectRoot, identity)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatalf("release re-acquired lock: %v", err)
	}
}

// TestOneshotWorktreeCommitSucceeds proves the load-bearing decoupling at the
// worktree level: a worktree provisioned from the worktree base accepts commits even
// though its .git and objects live under the parent repo. (The sandbox-writable
// -root half of this is covered by internal/sandbox.)
func TestOneshotWorktreeCommitSucceeds(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity, err := NewRunIdentity("integration commit")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}
	t.Cleanup(func() { _ = CleanupWorktree(context.Background(), projectRoot, identity) })

	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree: %v", err)
	}

	before := strings.TrimSpace(mustGitOutput(t, worktree.Path, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(worktree.Path, "feature.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	mustGitOutput(t, worktree.Path, "add", "feature.txt")
	mustGitOutput(t, worktree.Path, "commit", "-m", "feature: add file in worktree")
	after := strings.TrimSpace(mustGitOutput(t, worktree.Path, "rev-parse", "HEAD"))

	if before == after {
		t.Fatal("HEAD did not advance after worktree commit")
	}
	gotBranch := strings.TrimSpace(mustGitOutput(t, worktree.Path, "branch", "--show-current"))
	if want := identity.BranchName(); gotBranch != want {
		t.Fatalf("worktree branch = %q, want %q", gotBranch, want)
	}
}
