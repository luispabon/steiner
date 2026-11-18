package oneshot

import (
	"context"
	"errors"
	"testing"
)

func TestFailedCloseoutIsPersistedAsUnknownOutcomeAndNotRetried(t *testing.T) {
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

	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "", "", errors.New("gh auth expired")
	})

	// A run that finished its phases reaches closeout, which fails. The outcome
	// is ambiguous, so it is recorded as unknown with the intent still pending.
	finalizeErr := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))
	if finalizeErr == nil {
		t.Fatal("finalizeRun with a failing closeout returned nil error")
	}
	if *closeoutCalls != 1 {
		t.Fatalf("closeout calls = %d, want 1", *closeoutCalls)
	}

	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.ReportPath == "" {
		t.Error("persisted ReportPath is empty")
	}
	if persisted.ReportState != reportStateWritten {
		t.Errorf("persisted ReportState = %q, want %q", persisted.ReportState, reportStateWritten)
	}
	if persisted.CloseoutState != closeoutStateUnknown {
		t.Errorf("CloseoutState = %q, want %q", persisted.CloseoutState, closeoutStateUnknown)
	}
	if persisted.CloseoutNote == "" {
		t.Error("CloseoutNote is empty")
	}
	if persisted.CloseoutIntent != closeoutIntentPending {
		t.Errorf("CloseoutIntent = %q, want %q", persisted.CloseoutIntent, closeoutIntentPending)
	}

	// An unknown outcome is not offered as an automatic retry. The explicit
	// resolution entry point (steiner oneshot --resolve) and its listing status
	// arrive with the resume/list classification task.
	runs, err := ListResumableRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	for _, run := range runs {
		if run.RunID == identity.ID {
			t.Errorf("run %s is listed as retryable (%q), want manual resolution only", identity.ID, run.Status)
		}
	}

	// Neither a resume nor a repeated finalization may call closeout again.
	if _, err := orch.Resume(context.Background()); err == nil {
		t.Fatal("Resume on an unknown closeout outcome returned nil error")
	}
	if err := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath)); err == nil {
		t.Fatal("repeated finalizeRun on an unknown closeout outcome returned nil error")
	}
	if *closeoutCalls != 1 {
		t.Errorf("closeout calls = %d, want 1: an unknown outcome must never be retried automatically", *closeoutCalls)
	}

	factory := orch.deps.RunnerFactory.(*recordingRunnerFactory)
	factory.mu.Lock()
	phaseCalls := len(factory.calls)
	factory.mu.Unlock()
	if phaseCalls != 0 {
		t.Errorf("resume re-ran %d phases, want none", phaseCalls)
	}
}
