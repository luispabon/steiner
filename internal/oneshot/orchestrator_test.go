package oneshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

type recordedPhaseInput struct {
	conversation []agent.Message
	skillNames   []string
	steerCh      <-chan string
	modelAlias   string
	advisorCfg   config.AdvisorConfig
	approver     tool.ApprovalResponder
}

type recordingSessionStore struct {
	mu       sync.Mutex
	sessions []session.Session
}

func (s *recordingSessionStore) Save(sess session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, sess)
	return nil
}

type phaseRunnerStub struct {
	factory       *recordingRunnerFactory
	phase         Phase
	planningPath  string
	writePlan     bool
	blockOnCancel bool
	started       chan struct{}
}

func (r phaseRunnerStub) RunPhase(ctx context.Context, conversation []agent.Message, skillNames []string, steerCh <-chan string) (RunResult, error) {
	if r.factory != nil {
		r.factory.mu.Lock()
		if r.factory.calls == nil {
			r.factory.calls = make(map[Phase]recordedPhaseInput)
		}
		r.factory.calls[r.phase] = recordedPhaseInput{
			conversation: cloneAgentMessages(conversation),
			skillNames:   append([]string(nil), skillNames...),
			steerCh:      steerCh,
			modelAlias:   r.factory.calls[r.phase].modelAlias,
			advisorCfg:   r.factory.calls[r.phase].advisorCfg,
			approver:     r.factory.calls[r.phase].approver,
		}
		r.factory.mu.Unlock()
	}
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if r.writePlan && r.phase == PhasePlan {
		if err := os.MkdirAll(r.planningPath, 0o755); err != nil {
			return RunResult{}, err
		}
		if err := os.WriteFile(filepath.Join(r.planningPath, "overview.md"), []byte("overview\n"), 0o644); err != nil {
			return RunResult{}, err
		}
		if err := os.WriteFile(filepath.Join(r.planningPath, "plan.yaml"), []byte("steps: []\n"), 0o644); err != nil {
			return RunResult{}, err
		}
	}

	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:       1,
				Messages: cloneAgentMessages(conversation),
			},
		},
		NextGenerationID: 2,
	}

	if r.blockOnCancel {
		<-ctx.Done()
		return RunResult{Conversation: conversation, Lineage: lineage}, ctx.Err()
	}

	_ = skillNames
	_ = steerCh
	return RunResult{
		Conversation: conversation,
		Reply:        string(r.phase),
		Lineage:      lineage,
	}, nil
}

type recordingRunnerFactory struct {
	mu           sync.Mutex
	planningPath string
	runners      map[Phase]*phaseRunnerStub
	calls        map[Phase]recordedPhaseInput
}

func (f *recordingRunnerFactory) NewPhaseRunner(_ context.Context, phase Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (PhaseRunner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = make(map[Phase]recordedPhaseInput)
	}
	input := recordedPhaseInput{
		modelAlias: modelAlias,
		advisorCfg: advisorCfg,
		approver:   approver,
	}
	f.calls[phase] = input
	runner, ok := f.runners[phase]
	if !ok {
		return nil, errors.New("missing phase runner")
	}
	runner.phase = phase
	runner.planningPath = f.planningPath
	runner.factory = f
	return *runner, nil
}

func TestOrchestratorRunsAllPhasesAndPersistsSessions(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	planningPath := identity.PlanningPath(identity.WorktreePath(projectRoot))
	sessionStore := &recordingSessionStore{}
	events := make([]output.Event, 0, 12)
	runnerFactory := &recordingRunnerFactory{
		planningPath: planningPath,
		runners: map[Phase]*phaseRunnerStub{
			PhasePlan:      {writePlan: true},
			PhaseImplement: {},
			PhaseReview:    {},
		},
	}

	cfg := config.Config{
		DefaultModel: "fallback-model",
		Models: map[string]config.ModelConfig{
			"fallback-model": {},
		},
		Advisor: config.AdvisorConfig{
			Model:         "advisor-model",
			MaxUsesPerRun: 11,
			Enabled:       false,
		},
	}
	cfg.OneShot.Models = map[string]string{
		string(PhasePlan):      "plan-model",
		string(PhaseImplement): "implement-model",
	}

	steerCh := make(chan string, 1)
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        cfg,
		SessionStore:  sessionStore,
		RunnerFactory: runnerFactory,
		ManifestStore: NewManifestStore(identity.ManifestPath(projectRoot)),
		Events:        output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SteerCh:       steerCh,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}
	steerCh <- "stay focused"

	manifest, err := orch.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	worktreePath := identity.WorktreePath(projectRoot)
	if stat, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree not provisioned by Run: %v", err)
	} else if !stat.IsDir() {
		t.Fatalf("worktree path is not a directory: %s", worktreePath)
	}
	gotBranch := strings.TrimSpace(mustGitOutput(t, worktreePath, "branch", "--show-current"))
	if wantBranch := identity.BranchName(); gotBranch != wantBranch {
		t.Fatalf("worktree branch = %q, want %q", gotBranch, wantBranch)
	}

	if got, want := manifest.PhaseStatuses[PhasePlan], PhaseStatusDone; got != want {
		t.Fatalf("phase plan status = %q, want %q", got, want)
	}
	if got, want := manifest.PhaseStatuses[PhaseImplement], PhaseStatusDone; got != want {
		t.Fatalf("phase implement status = %q, want %q", got, want)
	}
	if got, want := manifest.PhaseStatuses[PhaseReview], PhaseStatusDone; got != want {
		t.Fatalf("phase review status = %q, want %q", got, want)
	}
	if got, want := manifest.PhaseSessionIDs[PhasePlan], sessionStore.sessions[0].ID; got != want {
		t.Fatalf("manifest phase plan session = %q, want %q", got, want)
	}
	if got, want := manifest.ModelSnapshot.PhaseModels[PhasePlan], "plan-model"; got != want {
		t.Fatalf("model snapshot plan = %q, want %q", got, want)
	}
	if got, want := manifest.ModelSnapshot.PhaseModels[PhaseReview], "fallback-model"; got != want {
		t.Fatalf("model snapshot review = %q, want %q", got, want)
	}
	if got, want := len(sessionStore.sessions), 3; got != want {
		t.Fatalf("saved sessions = %d, want %d", got, want)
	}
	for _, sess := range sessionStore.sessions {
		if got, want := sess.Group, identity.ID; got != want {
			t.Fatalf("session group = %q, want %q", got, want)
		}
	}
	if got, want := sessionStore.sessions[0].Model, "plan-model"; got != want {
		t.Fatalf("plan session model = %q, want %q", got, want)
	}
	if got, want := sessionStore.sessions[2].Model, "fallback-model"; got != want {
		t.Fatalf("review session model = %q, want %q", got, want)
	}

	runnerFactory.mu.Lock()
	planCall := runnerFactory.calls[PhasePlan]
	implementCall := runnerFactory.calls[PhaseImplement]
	reviewCall := runnerFactory.calls[PhaseReview]
	runnerFactory.mu.Unlock()

	if !planCall.advisorCfg.Enabled || planCall.advisorCfg.MaxUsesPerRun != advisorPlanBudget {
		t.Fatalf("plan advisor cfg = %#v, want enabled with budget %d", planCall.advisorCfg, advisorPlanBudget)
	}
	if !implementCall.advisorCfg.Enabled || implementCall.advisorCfg.MaxUsesPerRun != advisorImplementBudget {
		t.Fatalf("implement advisor cfg = %#v, want enabled with budget %d", implementCall.advisorCfg, advisorImplementBudget)
	}
	if !reviewCall.advisorCfg.Enabled || reviewCall.advisorCfg.MaxUsesPerRun != advisorReviewBudget {
		t.Fatalf("review advisor cfg = %#v, want enabled with budget %d", reviewCall.advisorCfg, advisorReviewBudget)
	}

	if got := len(events); got == 0 {
		t.Fatal("expected phase events to be emitted")
	}
	if got, want := events[0].Type, output.EventTypePhaseTransition; got != want {
		t.Fatalf("first event type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypePhaseIndicator; got != want {
		t.Fatalf("second event type = %q, want %q", got, want)
	}
	if got, want := len(planCall.conversation), 1; got != want {
		t.Fatalf("plan conversation len = %d, want %d", got, want)
	}
	if got := planCall.conversation[0].Content; !strings.Contains(got, "Planning artifacts:") || !strings.Contains(got, "Build the parser") {
		t.Fatalf("plan seed conversation = %q, want task and artifact pointers", got)
	}
	if planCall.steerCh == nil {
		t.Fatal("plan steer channel is nil")
	}
	if got, want := planCall.steerCh, steerCh; got != want {
		t.Fatalf("plan steer channel mismatch")
	}
}

func TestOrchestratorStopsOnBoundaryFailure(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	sessionStore := &recordingSessionStore{}
	runnerFactory := &recordingRunnerFactory{
		planningPath: identity.PlanningPath(identity.WorktreePath(projectRoot)),
		runners: map[Phase]*phaseRunnerStub{
			PhasePlan: {writePlan: false},
		},
	}

	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        config.Config{DefaultModel: "fallback-model"},
		SessionStore:  sessionStore,
		RunnerFactory: runnerFactory,
		ManifestStore: NewManifestStore(identity.ManifestPath(projectRoot)),
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}

	manifest, err := orch.Run(context.Background())
	if err == nil {
		t.Fatal("Run succeeded, want boundary failure")
	}
	var boundaryErr *BoundaryError
	if !errors.As(err, &boundaryErr) {
		t.Fatalf("Run error = %v, want BoundaryError", err)
	}
	if got, want := manifest.PhaseStatuses[PhasePlan], PhaseStatusFailed; got != want {
		t.Fatalf("phase plan status = %q, want %q", got, want)
	}
	if got, want := len(sessionStore.sessions), 1; got != want {
		t.Fatalf("saved sessions = %d, want %d", got, want)
	}
	if _, err := os.Stat(identity.LockPath(projectRoot)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after failure: %v", err)
	}
}

func TestOrchestratorCancelsAndReleasesLock(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	started := make(chan struct{}, 1)
	var signalCancel context.CancelFunc
	sessionStore := &recordingSessionStore{}
	runnerFactory := &recordingRunnerFactory{
		planningPath: identity.PlanningPath(identity.WorktreePath(projectRoot)),
		runners: map[Phase]*phaseRunnerStub{
			PhasePlan: {blockOnCancel: true, started: started},
		},
	}
	orch, err := NewOrchestrator(Dependencies{
		ProjectRoot:   projectRoot,
		Identity:      identity,
		Task:          "Build the parser",
		Config:        config.Config{DefaultModel: "fallback-model"},
		SessionStore:  sessionStore,
		RunnerFactory: runnerFactory,
		ManifestStore: NewManifestStore(identity.ManifestPath(projectRoot)),
		InterruptFactory: func(parent context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(parent)
			signalCancel = cancel
			return ctx, cancel
		},
	})
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}

	runDone := make(chan struct {
		manifest Manifest
		err      error
	}, 1)
	go func() {
		manifest, runErr := orch.Run(context.Background())
		runDone <- struct {
			manifest Manifest
			err      error
		}{manifest: manifest, err: runErr}
	}()

	<-started
	signalCancel()

	result := <-runDone
	if result.err == nil {
		t.Fatal("Run succeeded, want cancellation error")
	}
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", result.err)
	}
	if got, want := len(sessionStore.sessions), 1; got != want {
		t.Fatalf("saved sessions = %d, want %d", got, want)
	}
	if _, err := os.Stat(identity.LockPath(projectRoot)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after cancellation: %v", err)
	}
}

func TestOrchestratorConcurrentRunsGetDistinctWorktrees(t *testing.T) {
	projectRoot := setupGitRepo(t)
	id1, err := NewRunIdentity("concurrent task alpha")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}
	id2, err := NewRunIdentity("concurrent task beta")
	if err != nil {
		t.Fatalf("NewRunIdentity: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, id1)
		_ = CleanupWorktree(context.Background(), projectRoot, id2)
	})

	type result struct {
		manifest Manifest
		err      error
	}

	makeOrch := func(identity RunIdentity) *Orchestrator {
		planningPath := identity.PlanningPath(identity.WorktreePath(projectRoot))
		orch, err := NewOrchestrator(Dependencies{
			ProjectRoot:  projectRoot,
			Identity:     identity,
			Task:         "concurrent task",
			Config:       config.Config{DefaultModel: "m"},
			SessionStore: &recordingSessionStore{},
			RunnerFactory: &recordingRunnerFactory{
				planningPath: planningPath,
				runners: map[Phase]*phaseRunnerStub{
					PhasePlan:      {writePlan: true},
					PhaseImplement: {},
					PhaseReview:    {},
				},
			},
			ManifestStore:    NewManifestStore(identity.ManifestPath(projectRoot)),
			InterruptFactory: context.WithCancel,
		})
		if err != nil {
			t.Fatalf("NewOrchestrator: %v", err)
		}
		return orch
	}

	ch1 := make(chan result, 1)
	ch2 := make(chan result, 1)
	go func() {
		m, e := makeOrch(id1).Run(context.Background())
		ch1 <- result{m, e}
	}()
	go func() {
		m, e := makeOrch(id2).Run(context.Background())
		ch2 <- result{m, e}
	}()

	r1 := <-ch1
	r2 := <-ch2

	if r1.err != nil {
		t.Fatalf("run 1 failed: %v", r1.err)
	}
	if r2.err != nil {
		t.Fatalf("run 2 failed: %v", r2.err)
	}
	if r1.manifest.Branch == r2.manifest.Branch {
		t.Fatalf("concurrent runs share branch %q", r1.manifest.Branch)
	}
	if r1.manifest.WorktreePath == r2.manifest.WorktreePath {
		t.Fatalf("concurrent runs share worktree %q", r1.manifest.WorktreePath)
	}
}
