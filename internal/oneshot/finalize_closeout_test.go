package oneshot

import (
	"context"
	"errors"
	"testing"
)

func TestFailedCloseoutIsPersistedListedAndRetriedOnResume(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "cl0se1", Slug: "closeout-retry"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")

	manifest.PhaseStatuses = map[Phase]PhaseStatus{
		PhasePlan:      PhaseStatusDone,
		PhaseImplement: PhaseStatusDone,
		PhaseReview:    PhaseStatusDone,
	}
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = "main"
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}
	orch.deps.Config.OneShot.AutoPR = true

	calls := 0
	runnerErr := errors.New("gh auth expired")
	orig := closeoutRunners[closeoutProviderGitHub]
	closeoutRunners[closeoutProviderGitHub] = func(context.Context, closeoutRequest) (string, string, error) {
		calls++
		if runnerErr != nil {
			return "", "", runnerErr
		}
		return "https://github.com/owner/repo/pull/7", "", nil
	}
	t.Cleanup(func() { closeoutRunners[closeoutProviderGitHub] = orig })

	// A run that finished its phases reaches closeout, which fails.
	orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))
	if calls != 1 {
		t.Fatalf("closeout calls = %d, want 1", calls)
	}

	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.ReportPath == "" {
		t.Error("persisted ReportPath is empty")
	}
	if persisted.CloseoutState != closeoutStateFailed {
		t.Errorf("CloseoutState = %q, want %q", persisted.CloseoutState, closeoutStateFailed)
	}
	if persisted.CloseoutNote == "" {
		t.Error("CloseoutNote is empty")
	}

	runs, err := ListResumableRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	found := false
	for _, run := range runs {
		if run.RunID == identity.ID {
			found = true
			if run.ResumePhase != phaseCloseout {
				t.Errorf("ResumePhase = %q, want %q", run.ResumePhase, phaseCloseout)
			}
		}
	}
	if !found {
		t.Fatalf("run %s missing from ListResumableRuns: %+v", identity.ID, runs)
	}

	runnerErr = nil
	updated, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if calls != 2 {
		t.Errorf("closeout calls after resume = %d, want 2", calls)
	}
	if updated.CloseoutState != closeoutStateCreated || updated.CloseoutURL == "" {
		t.Errorf("closeout state=%q url=%q, want created with url", updated.CloseoutState, updated.CloseoutURL)
	}
	factory := orch.deps.RunnerFactory.(*recordingRunnerFactory)
	factory.mu.Lock()
	phaseCalls := len(factory.calls)
	factory.mu.Unlock()
	if phaseCalls != 0 {
		t.Errorf("resume re-ran %d phases, want closeout only", phaseCalls)
	}

	runs, err = ListResumableRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	for _, run := range runs {
		if run.RunID == identity.ID {
			t.Errorf("run %s still listed after successful closeout", identity.ID)
		}
	}
}
