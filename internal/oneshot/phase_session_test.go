package oneshot

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

type inputRunner func(ctx context.Context, in PhaseRunInput) (RunResult, error)

func (f inputRunner) RunPhase(ctx context.Context, in PhaseRunInput) (RunResult, error) {
	return f(ctx, in)
}

func userMessages(texts ...string) []agent.Message {
	out := make([]agent.Message, len(texts))
	for i, text := range texts {
		out[i] = agent.Message{Role: agent.MessageRoleUser, Content: text}
	}
	return out
}

func newPhaseSessionFixture(t *testing.T, runner PhaseRunner) (*Orchestrator, *recordingSessionStore, *ManifestStore, runPhaseParams) {
	t.Helper()
	dir := t.TempDir()
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	lock, err := acquireRunLock(dir, identity, 0)
	if err != nil {
		t.Fatalf("acquireRunLock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	var events []output.Event
	o := newHeartbeatTestOrchestrator(runner, &events)
	sessions := &recordingSessionStore{}
	o.deps.SessionStore = sessions
	o.deps.Task = "build a parser"
	manifest := &Manifest{RunID: "run-1", PhaseStatuses: map[Phase]PhaseStatus{}, PhaseSessionIDs: map[Phase]string{}}
	store := NewManifestStore(filepath.Join(dir, "manifest.json"))
	return o, sessions, store, runPhaseParams{
		InterruptCtx: context.Background(),
		Store:        store,
		Manifest:     manifest,
		Lock:         lock,
		WorktreePath: dir,
		PlanningPath: filepath.Join(dir, "planning"),
		Phase:        PhasePlan,
	}
}

func TestRunPhaseCreatesSessionBeforeRunning(t *testing.T) {
	var (
		calls          int
		idOnDisk       string
		sessionsBefore int
		sessions       *recordingSessionStore
		store          *ManifestStore
	)
	runner := inputRunner(func(_ context.Context, in PhaseRunInput) (RunResult, error) {
		calls++
		disk, err := store.Read()
		if err != nil {
			t.Errorf("read manifest during phase: %v", err)
		}
		idOnDisk = disk.PhaseSessionIDs[PhasePlan]
		sessionsBefore = len(sessions.sessions)
		if in.Session.ID == "" || in.Session.Save == nil {
			t.Errorf("phase session = %+v, want id and save", in.Session)
			return RunResult{}, nil
		}
		snap := agent.DriverSnapshot{Conversation: userMessages("seed", "partial")}
		if err := in.Session.Save(context.Background(), snap); err != nil {
			t.Errorf("Session.Save: %v", err)
		}
		return RunResult{}, errors.New("model exploded")
	})
	o, sessions, store, params := newPhaseSessionFixture(t, runner)

	err := o.runPhase(params)
	if err == nil {
		t.Fatal("runPhase succeeded, want the runner error")
	}
	if calls != 1 {
		t.Fatalf("RunPhase calls = %d, want 1", calls)
	}
	if sessionsBefore != 1 {
		t.Fatalf("sessions saved before RunPhase = %d, want 1", sessionsBefore)
	}
	if len(sessions.sessions) != 1 {
		t.Fatalf("distinct sessions = %d, want 1 (no second session)", len(sessions.sessions))
	}
	saved := sessions.sessions[0]
	if idOnDisk != saved.ID {
		t.Fatalf("manifest session id during phase = %q, want %q", idOnDisk, saved.ID)
	}
	if got := params.Manifest.PhaseSessionIDs[PhasePlan]; got != saved.ID {
		t.Fatalf("manifest session id after failure = %q, want %q", got, saved.ID)
	}
	if got := saved.Lineage.Generations; len(got) != 1 || len(got[0].Messages) != 2 {
		t.Fatalf("partial session lineage = %+v, want the two saved messages kept", saved.Lineage)
	}
}

func TestRunPhaseFinalSaveUpdatesSameSession(t *testing.T) {
	runner := inputRunner(func(_ context.Context, _ PhaseRunInput) (RunResult, error) {
		return RunResult{Conversation: userMessages("seed", "done")}, nil
	})
	o, sessions, _, params := newPhaseSessionFixture(t, runner)
	// The phase writes no artifacts, so the boundary check fails after the
	// session has been finalised; the session assertions still hold.
	_ = o.runPhase(params)

	if len(sessions.sessions) != 1 {
		t.Fatalf("distinct sessions = %d, want 1", len(sessions.sessions))
	}
	if got := sessions.sessions[0].Lineage.Generations; len(got) != 1 || len(got[0].Messages) != 2 {
		t.Fatalf("final session lineage = %+v, want the result conversation", sessions.sessions[0].Lineage)
	}
}

type createFailingSessionStore struct{}

func (createFailingSessionStore) Save(session.Session) error { return errors.New("disk full") }

func TestRunPhaseSessionCreateFailureSkipsRunner(t *testing.T) {
	called := false
	runner := inputRunner(func(context.Context, PhaseRunInput) (RunResult, error) {
		called = true
		return RunResult{}, nil
	})
	o, _, _, params := newPhaseSessionFixture(t, runner)
	o.deps.SessionStore = createFailingSessionStore{}

	if err := o.runPhase(params); err == nil {
		t.Fatal("runPhase succeeded, want the session error")
	}
	if called {
		t.Fatal("RunPhase was called after the phase session could not be created")
	}
	if got := params.Manifest.PhaseStatuses[PhasePlan]; got != PhaseStatusFailed {
		t.Fatalf("phase status = %q, want failed", got)
	}
}

func TestPhaseSessionPersistsLedger(t *testing.T) {
	sessions := &recordingSessionStore{}
	o := &Orchestrator{deps: Dependencies{SessionStore: sessions, Identity: RunIdentity{ID: "abc123"}, Task: "t"}}
	ps, err := o.newPhaseSession(PhaseImplement, "m")
	if err != nil {
		t.Fatalf("newPhaseSession: %v", err)
	}
	entries := []agent.SubAgentLedgerEntry{{AgentID: "a1", AgentType: "code", ParentCallID: "c1", WorktreePath: "/wt/a1"}}
	steps := []struct {
		name string
		do   func() error
		want []agent.SubAgentLedgerEntry
	}{
		{"snapshot with outstanding ledger", func() error {
			return ps.save(context.Background(), agent.DriverSnapshot{Conversation: userMessages("x"), Ledger: entries})
		}, entries},
		{"final result save keeps ledger", func() error {
			return ps.saveResult(RunResult{Conversation: userMessages("x", "y")})
		}, entries},
		{"snapshot after delivery clears ledger", func() error {
			return ps.save(context.Background(), agent.DriverSnapshot{Conversation: userMessages("x", "y")})
		}, nil},
		{"final result save stays empty", func() error {
			return ps.saveResult(RunResult{Conversation: userMessages("x", "y")})
		}, nil},
	}
	for _, step := range steps {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := sessions.sessions[0].SubAgentLedger; !slices.Equal(got, step.want) {
			t.Fatalf("%s: ledger = %+v, want %+v", step.name, got, step.want)
		}
	}
}
