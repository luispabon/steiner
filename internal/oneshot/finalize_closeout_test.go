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

	closeoutFailures := 0
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		closeoutFailures++
		if closeoutFailures == 1 {
			return "", "", errors.New("gh auth expired")
		}
		return "https://github.com/owner/repo/pull/9", "", nil
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

	// An unknown outcome is listed for manual resolution, never as an automatic
	// closeout retry.
	runs, err := ListResumableRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	listed := false
	for _, run := range runs {
		if run.RunID != identity.ID {
			continue
		}
		listed = true
		if run.ResumePhase != phaseResolve || run.Status != "resolve closeout" {
			t.Errorf("run listed as phase %q status %q, want resolve closeout", run.ResumePhase, run.Status)
		}
	}
	if !listed {
		t.Fatalf("run %s is not listed for manual resolution", identity.ID)
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

	// The explicit resolution authorises exactly one further closeout attempt.
	resolved, err := ResolveCloseout(context.Background(), orch.deps, Resolution{Kind: ResolutionRetry})
	if err != nil {
		t.Fatalf("ResolveCloseout failed: %v", err)
	}
	if resolved.CloseoutState != closeoutStateRetryAuthorized {
		t.Fatalf("resolved CloseoutState = %q, want %q", resolved.CloseoutState, closeoutStateRetryAuthorized)
	}
	if _, err := orch.Resume(context.Background()); err != nil {
		t.Fatalf("Resume after an authorised retry failed: %v", err)
	}
	if *closeoutCalls != 2 {
		t.Fatalf("closeout calls after the authorised retry = %d, want 2", *closeoutCalls)
	}
	retried, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if retried.CloseoutState != closeoutStateCreated || retried.CloseoutURL == "" {
		t.Errorf("retried closeout state = %q url = %q, want created with a url", retried.CloseoutState, retried.CloseoutURL)
	}
	if _, err := orch.Resume(context.Background()); err == nil {
		t.Fatal("Resume after a created closeout returned nil error")
	}
	if *closeoutCalls != 2 {
		t.Errorf("closeout calls = %d, want no further call", *closeoutCalls)
	}
}
