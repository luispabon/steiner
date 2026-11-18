package oneshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func allPhasesDone() map[Phase]PhaseStatus {
	return map[Phase]PhaseStatus{
		PhasePlan:      PhaseStatusDone,
		PhaseImplement: PhaseStatusDone,
		PhaseReview:    PhaseStatusDone,
	}
}

// capturePhaseIndicatorMessages records every phase indicator message emitted
// through the orchestrator's event sink.
func capturePhaseIndicatorMessages(orch *Orchestrator) *[]string {
	messages := &[]string{}
	orch.deps.Events = output.SinkFunc(func(event output.Event) {
		if payload, ok := event.Payload.(output.PhaseIndicatorEvent); ok {
			*messages = append(*messages, payload.Message)
		}
	})
	return messages
}

// stubGitHubCloseout swaps the GitHub closeout runner and returns a counter of
// calls made through it.
func stubGitHubCloseout(t *testing.T, runner closeoutRunner) *int {
	t.Helper()
	calls := 0
	orig := closeoutRunners[closeoutProviderGitHub]
	closeoutRunners[closeoutProviderGitHub] = func(ctx context.Context, req closeoutRequest) (string, string, error) {
		calls++
		return runner(ctx, req)
	}
	t.Cleanup(func() { closeoutRunners[closeoutProviderGitHub] = orig })
	return &calls
}

func TestFirstIncompletePhaseClassifiesEveryStatus(t *testing.T) {
	cases := []struct {
		name      string
		statuses  map[Phase]PhaseStatus
		wantPhase Phase
		wantOK    bool
	}{
		{
			name: "pending phases start at plan",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusPending, PhaseImplement: PhaseStatusPending, PhaseReview: PhaseStatusPending,
			},
			wantPhase: PhasePlan,
			wantOK:    true,
		},
		{
			name: "running phase is incomplete",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatusRunning, PhaseReview: PhaseStatusPending,
			},
			wantPhase: PhaseImplement,
			wantOK:    true,
		},
		{
			name: "failed phase is incomplete",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusFailed, PhaseImplement: PhaseStatusPending, PhaseReview: PhaseStatusPending,
			},
			wantPhase: PhasePlan,
			wantOK:    true,
		},
		{
			name:      "done and skipped phases are finished",
			statuses:  map[Phase]PhaseStatus{PhasePlan: PhaseStatusSkipped, PhaseImplement: PhaseStatusDone, PhaseReview: PhaseStatusSkipped},
			wantPhase: "",
			wantOK:    false,
		},
		{
			name: "unknown status is incomplete",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatus("interrupted"), PhaseReview: PhaseStatusDone,
			},
			wantPhase: PhaseImplement,
			wantOK:    true,
		},
		{
			name:      "empty status is incomplete",
			statuses:  map[Phase]PhaseStatus{PhasePlan: PhaseStatus("")},
			wantPhase: PhasePlan,
			wantOK:    true,
		},
		{
			name:      "missing phase entry is incomplete",
			statuses:  map[Phase]PhaseStatus{PhasePlan: PhaseStatusDone},
			wantPhase: PhaseImplement,
			wantOK:    true,
		},
		{
			name:      "empty manifest starts at plan",
			statuses:  map[Phase]PhaseStatus{},
			wantPhase: PhasePlan,
			wantOK:    true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPhase, gotOK := firstIncompletePhase(Manifest{PhaseStatuses: c.statuses})
			if gotOK != c.wantOK || gotPhase != c.wantPhase {
				t.Errorf("firstIncompletePhase = (%q, %t), want (%q, %t)", gotPhase, gotOK, c.wantPhase, c.wantOK)
			}
		})
	}
}

func TestCloseoutRetryableOnlyForFinishedPhasesAndAuthorisedRetry(t *testing.T) {
	cases := []struct {
		name     string
		statuses map[Phase]PhaseStatus
		state    string
		want     bool
	}{
		{
			name:     "authorised retry after done phases",
			statuses: allPhasesDone(),
			state:    closeoutStateRetryAuthorized,
			want:     true,
		},
		{
			name: "authorised retry after skipped phases",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusSkipped, PhaseImplement: PhaseStatusSkipped, PhaseReview: PhaseStatusSkipped,
			},
			state: closeoutStateRetryAuthorized,
			want:  true,
		},
		{
			name: "incomplete phase blocks closeout retry",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatusFailed, PhaseReview: PhaseStatusDone,
			},
			state: closeoutStateRetryAuthorized,
			want:  false,
		},
		{
			name: "unknown phase status blocks closeout retry",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatus("interrupted"), PhaseReview: PhaseStatusDone,
			},
			state: closeoutStateRetryAuthorized,
			want:  false,
		},
		// A legacy failed state is ambiguous: it may hide a created request, so
		// only an explicit operator resolution can authorise another attempt.
		{name: "legacy failed closeout is ambiguous", statuses: allPhasesDone(), state: closeoutStateFailed, want: false},
		{name: "created closeout is terminal", statuses: allPhasesDone(), state: closeoutStateCreated, want: false},
		{name: "missing closeout state is terminal", statuses: allPhasesDone(), state: "", want: false},
		{name: "skipped closeout is terminal", statuses: allPhasesDone(), state: closeoutStateSkipped, want: false},
		{name: "unsupported closeout is terminal", statuses: allPhasesDone(), state: closeoutStateUnsupported, want: false},
		{name: "unknown closeout outcome is not retried automatically", statuses: allPhasesDone(), state: closeoutStateUnknown, want: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			manifest := Manifest{PhaseStatuses: c.statuses, CloseoutState: c.state}
			if got := closeoutRetryable(manifest); got != c.want {
				t.Errorf("closeoutRetryable = %t, want %t", got, c.want)
			}
		})
	}
}

func TestFinalizeRunLegacyFailedOutcomeRequiresResolution(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "legacyfail", Slug: "legacy-failed"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	// A legacy manifest recorded a failed closeout before the finalization
	// fields existed, so its outcome cannot be proven either way.
	manifest.FinalizationVersion = 0
	manifest.ReportState = ""
	manifest.ReportPath = filepath.Join(identity.PlanningPath(manifest.WorktreePath), finalReportFileName)
	manifest.CloseoutState = closeoutStateFailed
	manifest.CloseoutNote = "gh auth expired"
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})
	planningPath := identity.PlanningPath(manifest.WorktreePath)

	// Both attempts must abort: the first records the unknown state that the
	// report write alone would have hidden, the second must still see it.
	for attempt := 1; attempt <= 2; attempt++ {
		loaded, err := store.Read()
		if err != nil {
			t.Fatalf("store.Read failed: %v", err)
		}
		if err := orch.finalizeRun(context.Background(), store, &loaded, planningPath); err == nil {
			t.Fatalf("finalizeRun attempt %d on a legacy failed outcome returned nil error", attempt)
		}

		persisted, err := store.Read()
		if err != nil {
			t.Fatalf("store.Read failed: %v", err)
		}
		if persisted.CloseoutState != closeoutStateUnknown {
			t.Fatalf("attempt %d CloseoutState = %q, want %q", attempt, persisted.CloseoutState, closeoutStateUnknown)
		}
		if persisted.CloseoutNote != "closeout outcome unknown; manual resolution required" {
			t.Errorf("attempt %d CloseoutNote = %q, want the manual-resolution note", attempt, persisted.CloseoutNote)
		}
		if persisted.ReportState != reportStateWritten || persisted.ReportPath == "" {
			t.Errorf("attempt %d report state = %q path = %q, want a written report", attempt, persisted.ReportState, persisted.ReportPath)
		}
		if *closeoutCalls != 0 {
			t.Fatalf("attempt %d closeout calls = %d, want 0", attempt, *closeoutCalls)
		}
	}
}

func TestFinalizeRunLegacyMissingStateRequiresResolution(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "legacynone", Slug: "legacy-missing"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	manifest.FinalizationVersion = 0
	manifest.ReportState = ""
	manifest.ReportPath = ""
	manifest.CloseoutState = ""
	manifest.CloseoutIntent = ""
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	if err := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath)); err == nil {
		t.Fatal("finalizeRun on a legacy manifest with no closeout state returned nil error")
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateUnknown {
		t.Errorf("CloseoutState = %q, want %q", persisted.CloseoutState, closeoutStateUnknown)
	}
	if persisted.ReportState != reportStateWritten || persisted.ReportPath == "" {
		t.Errorf("report state = %q path = %q, want the report written", persisted.ReportState, persisted.ReportPath)
	}
	if _, statErr := os.Stat(persisted.ReportPath); statErr != nil {
		t.Errorf("stat persisted report: %v", statErr)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 for an unknown outcome", *closeoutCalls)
	}
}

func TestResumeRecoversFromSkippedUnknownAndMissingPhaseStatuses(t *testing.T) {
	cases := []struct {
		name         string
		statuses     map[Phase]PhaseStatus
		wantRuns     []Phase
		wantStatuses map[Phase]PhaseStatus
	}{
		{
			name: "skipped phase stays skipped",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusSkipped, PhaseImplement: PhaseStatusFailed, PhaseReview: PhaseStatusPending,
			},
			wantRuns: []Phase{PhaseImplement, PhaseReview},
			wantStatuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusSkipped, PhaseImplement: PhaseStatusDone, PhaseReview: PhaseStatusDone,
			},
		},
		{
			name: "unknown status restarts phase",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatus("interrupted"), PhaseReview: PhaseStatusPending,
			},
			wantRuns:     []Phase{PhaseImplement, PhaseReview},
			wantStatuses: allPhasesDone(),
		},
		{
			name:         "missing status restarts phase",
			statuses:     map[Phase]PhaseStatus{PhasePlan: PhaseStatusDone},
			wantRuns:     []Phase{PhaseImplement, PhaseReview},
			wantStatuses: allPhasesDone(),
		},
		{
			name: "running status restarts phase",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatusRunning, PhaseReview: PhaseStatusPending,
			},
			wantRuns:     []Phase{PhaseImplement, PhaseReview},
			wantStatuses: allPhasesDone(),
		},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			projectRoot := setupGitRepo(t)
			identity := RunIdentity{ID: fmt.Sprintf("recover%d", i), Slug: "recover"}
			manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
			manifest.PhaseStatuses = c.statuses
			manifest.CurrentPhase = PhasePlan
			if err := store.Write(manifest); err != nil {
				t.Fatalf("store.Write failed: %v", err)
			}

			updated, err := orch.Resume(context.Background())
			if err != nil {
				t.Fatalf("Resume failed: %v", err)
			}

			factory := orch.deps.RunnerFactory.(*recordingRunnerFactory)
			factory.mu.Lock()
			called := make(map[Phase]bool, len(factory.calls))
			for phase := range factory.calls {
				called[phase] = true
			}
			factory.mu.Unlock()
			for _, phase := range phaseOrder() {
				want := slices.Contains(c.wantRuns, phase)
				if called[phase] != want {
					t.Errorf("phase %s ran = %t, want %t", phase, called[phase], want)
				}
			}

			for _, phase := range phaseOrder() {
				if got, want := updated.PhaseStatuses[phase], c.wantStatuses[phase]; got != want {
					t.Errorf("resumed %s status = %q, want %q", phase, got, want)
				}
			}
			persisted, err := store.Read()
			if err != nil {
				t.Fatalf("store.Read failed: %v", err)
			}
			for _, phase := range phaseOrder() {
				if got, want := persisted.PhaseStatuses[phase], c.wantStatuses[phase]; got != want {
					t.Errorf("persisted %s status = %q, want %q", phase, got, want)
				}
			}
		})
	}
}

func TestResumeRefusesCompletedRunWithoutRetryableCloseout(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "c0mplete", Slug: "complete"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = map[Phase]PhaseStatus{
		PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatusSkipped, PhaseReview: PhaseStatusDone,
	}
	manifest.CurrentPhase = PhaseReview
	// A completed run needs a written report; a pending one is finished by the
	// next resume instead of being rejected as complete.
	manifest.ReportState = reportStateWritten
	manifest.ReportPath = filepath.Join(identity.PlanningPath(manifest.WorktreePath), finalReportFileName)
	manifest.CloseoutState = closeoutStateCreated
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	_, err := orch.Resume(context.Background())
	if err == nil {
		t.Fatal("Resume on a completed run returned nil error")
	}
	if !strings.Contains(err.Error(), "already complete") || !strings.Contains(err.Error(), identity.ID) {
		t.Fatalf("Resume error = %q, want it to name %s as already complete", err, identity.ID)
	}

	factory := orch.deps.RunnerFactory.(*recordingRunnerFactory)
	factory.mu.Lock()
	runs := len(factory.calls)
	factory.mu.Unlock()
	if runs != 0 {
		t.Errorf("completed run restarted %d phases, want 0", runs)
	}
}

func TestFinalizeRunSkipsCloseoutWhenFinalReportWriteFails(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "n0report", Slug: "no-report"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	planningPath := identity.PlanningPath(manifest.WorktreePath)
	if err := os.MkdirAll(filepath.Join(planningPath, finalReportFileName), 0o755); err != nil {
		t.Fatalf("create report path blocker failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})
	messages := capturePhaseIndicatorMessages(orch)

	finalizeErr := orch.finalizeRun(context.Background(), store, &manifest, planningPath)
	if finalizeErr == nil {
		t.Fatal("finalizeRun with a blocked final report returned nil error")
	}
	if !strings.Contains(finalizeErr.Error(), "generate final report") {
		t.Errorf("finalizeRun error = %q, want a final report generation failure", finalizeErr)
	}

	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 when report generation fails", *closeoutCalls)
	}
	if manifest.ReportPath != "" {
		t.Errorf("ReportPath = %q, want empty", manifest.ReportPath)
	}
	if manifest.CloseoutState != "" {
		t.Errorf("CloseoutState = %q, want empty", manifest.CloseoutState)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.ReportPath != "" || persisted.CloseoutState != "" {
		t.Errorf("persisted report path = %q closeout state = %q, want both empty", persisted.ReportPath, persisted.CloseoutState)
	}
	if persisted.ReportState != reportStatePending || persisted.CloseoutIntent != "" {
		t.Errorf("persisted report state = %q closeout intent = %q, want pending and empty", persisted.ReportState, persisted.CloseoutIntent)
	}
	// The injected directory blocks the atomic rename, so the event must name
	// the report write failure, not an unrelated git snapshot failure.
	wantMessage := "generate report: write final report: rename temp file:"
	if joined := strings.Join(*messages, "\n"); !strings.Contains(joined, wantMessage) {
		t.Errorf("phase indicator messages = %q, want one containing %q", *messages, wantMessage)
	}
}

// blockManifestWrites replaces the manifest file with a directory so every
// later atomic write fails; the last durable state stays readable from
// backupPath.
func blockManifestWrites(manifestPath, backupPath string) error {
	if err := os.Rename(manifestPath, backupPath); err != nil {
		return err
	}
	return os.Mkdir(manifestPath, 0o755)
}

func TestFinalizeRunCloseoutSurvivesManifestWriteFailure(t *testing.T) {
	cases := []struct {
		name        string
		closeoutErr error
		wantState   string
		wantURL     bool
		wantNote    string
		wantEvents  []string
	}{
		{
			name:       "created closeout",
			wantState:  closeoutStateCreated,
			wantURL:    true,
			wantEvents: []string{"persist closeout result"},
		},
		{
			name:        "failed closeout",
			closeoutErr: errors.New("gh auth expired"),
			wantState:   closeoutStateUnknown,
			wantNote:    "gh auth expired",
			wantEvents:  []string{"closeout: gh auth expired", "persist unknown closeout state"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			projectRoot := setupGitRepo(t)
			identity := RunIdentity{ID: "writeloss", Slug: "write-loss"}
			manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
			manifest.PhaseStatuses = allPhasesDone()
			manifest.CurrentPhase = PhaseReview
			manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
			if err := store.Write(manifest); err != nil {
				t.Fatalf("store.Write failed: %v", err)
			}

			manifestPath := identity.ManifestPath(projectRoot)
			backupPath := manifestPath + ".before"

			runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
			orch.deps.Config.OneShot.AutoPR = true
			var sabotageErr error
			closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
				// Fail only the write that records the closeout outcome, so the
				// report and intent writes must already have landed.
				if err := blockManifestWrites(manifestPath, backupPath); err != nil {
					sabotageErr = err
					return "", "", err
				}
				if c.closeoutErr != nil {
					return "", "", c.closeoutErr
				}
				return "https://github.com/owner/repo/pull/7", "", nil
			})
			messages := capturePhaseIndicatorMessages(orch)

			finalizeErr := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))
			if sabotageErr != nil {
				t.Fatalf("manifest sabotage failed: %v", sabotageErr)
			}
			if finalizeErr == nil {
				t.Fatal("finalizeRun with an unpersistable result returned nil error")
			}
			if c.closeoutErr != nil && !errors.Is(finalizeErr, c.closeoutErr) {
				t.Errorf("finalizeRun error = %q, want it to wrap %q", finalizeErr, c.closeoutErr)
			}
			if *closeoutCalls != 1 {
				t.Errorf("closeout calls = %d, want 1", *closeoutCalls)
			}
			if manifest.CloseoutState != c.wantState {
				t.Errorf("in-memory CloseoutState = %q, want %q", manifest.CloseoutState, c.wantState)
			}
			if gotURL := manifest.CloseoutURL != ""; gotURL != c.wantURL {
				t.Errorf("in-memory CloseoutURL = %q, want set = %t", manifest.CloseoutURL, c.wantURL)
			}
			if manifest.CloseoutNote != c.wantNote {
				t.Errorf("in-memory CloseoutNote = %q, want %q", manifest.CloseoutNote, c.wantNote)
			}

			backup, err := NewManifestStore(backupPath).Read()
			if err != nil {
				t.Fatalf("read pre-failure manifest failed: %v", err)
			}
			if backup.ReportState != reportStateWritten || backup.ReportPath == "" {
				t.Errorf("pre-failure report state = %q path = %q, want a written report", backup.ReportState, backup.ReportPath)
			}
			if backup.CloseoutIntent != closeoutIntentPending {
				t.Errorf("pre-failure closeout intent = %q, want %q", backup.CloseoutIntent, closeoutIntentPending)
			}
			if backup.CloseoutState != "" {
				t.Errorf("pre-failure closeout state = %q, want empty", backup.CloseoutState)
			}

			joined := strings.Join(*messages, "\n")
			for _, want := range c.wantEvents {
				if !strings.Contains(joined, want) {
					t.Errorf("phase indicator messages = %q, want one containing %q", *messages, want)
				}
			}
		})
	}
}

func TestFinalizeRunPersistsReportBeforeCloseout(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "0rder1", Slug: "report-first"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		onDisk, err := store.Read()
		if err != nil {
			t.Errorf("read manifest from closeout: %v", err)
			return "", "", err
		}
		if onDisk.FinalizationVersion != finalizationVersion {
			t.Errorf("on-disk FinalizationVersion at closeout = %d, want %d", onDisk.FinalizationVersion, finalizationVersion)
		}
		if onDisk.ReportState != reportStateWritten {
			t.Errorf("on-disk ReportState at closeout = %q, want %q", onDisk.ReportState, reportStateWritten)
		}
		if onDisk.ReportPath == "" {
			t.Error("on-disk ReportPath at closeout is empty")
		}
		if onDisk.CloseoutIntent != closeoutIntentPending {
			t.Errorf("on-disk CloseoutIntent at closeout = %q, want %q", onDisk.CloseoutIntent, closeoutIntentPending)
		}
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	if err := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath)); err != nil {
		t.Fatalf("finalizeRun failed: %v", err)
	}

	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateCreated || persisted.CloseoutURL == "" {
		t.Errorf("closeout state = %q url = %q, want created with a url", persisted.CloseoutState, persisted.CloseoutURL)
	}
	if persisted.CloseoutIntent != "" {
		t.Errorf("CloseoutIntent = %q, want it cleared after a recorded result", persisted.CloseoutIntent)
	}
}

func TestFinalizeRunDoesNotCallCloseoutWhenManifestWriteFails(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "n0write", Slug: "no-write"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	// No finalization state can be persisted, so closeout must never run: the
	// first persistence failure aborts before the intent write.
	manifestPath := identity.ManifestPath(projectRoot)
	if err := blockManifestWrites(manifestPath, manifestPath+".before"); err != nil {
		t.Fatalf("block manifest writes failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})
	messages := capturePhaseIndicatorMessages(orch)

	finalizeErr := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))
	if finalizeErr == nil {
		t.Fatal("finalizeRun with an unpersistable manifest returned nil error")
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 when finalization state cannot be persisted", *closeoutCalls)
	}
	if manifest.CloseoutState != "" || manifest.CloseoutURL != "" {
		t.Errorf("closeout state = %q url = %q, want both empty", manifest.CloseoutState, manifest.CloseoutURL)
	}
	if joined := strings.Join(*messages, "\n"); !strings.Contains(joined, "persist report state") {
		t.Errorf("phase indicator messages = %q, want one containing %q", *messages, "persist report state")
	}
}

func TestFinalizeRunUnknownOutcomeIsNotRetried(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "unkn0wn", Slug: "unknown-outcome"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	closeoutFailure := errors.New("gh auth expired")
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "", "", closeoutFailure
	})

	firstErr := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))
	if firstErr == nil {
		t.Fatal("finalizeRun with a failing closeout returned nil error")
	}
	if !errors.Is(firstErr, closeoutFailure) {
		t.Errorf("finalizeRun error = %q, want it to wrap the closeout failure", firstErr)
	}

	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateUnknown {
		t.Errorf("CloseoutState = %q, want %q", persisted.CloseoutState, closeoutStateUnknown)
	}
	if persisted.CloseoutIntent != closeoutIntentPending {
		t.Errorf("CloseoutIntent = %q, want %q", persisted.CloseoutIntent, closeoutIntentPending)
	}
	if persisted.ReportState != reportStateWritten {
		t.Errorf("ReportState = %q, want %q", persisted.ReportState, reportStateWritten)
	}

	secondErr := orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))
	if secondErr == nil {
		t.Fatal("second finalizeRun on an unknown outcome returned nil error")
	}
	if !strings.Contains(secondErr.Error(), "unknown") {
		t.Errorf("second finalizeRun error = %q, want it to report an unknown outcome", secondErr)
	}
	if *closeoutCalls != 1 {
		t.Errorf("closeout calls = %d, want 1: an unknown outcome must not be retried automatically", *closeoutCalls)
	}
}

func TestFinalizeRunKeepsFinalReportOnResultWriteFailure(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "keeprep0rt", Slug: "keep-report"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	planningPath := identity.PlanningPath(manifest.WorktreePath)
	reportPath := filepath.Join(planningPath, finalReportFileName)
	manifestPath := identity.ManifestPath(projectRoot)
	backupPath := manifestPath + ".before"

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	var reportBefore []byte
	var sabotageErr error
	stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		data, err := os.ReadFile(reportPath)
		if err != nil {
			sabotageErr = err
			return "", "", err
		}
		reportBefore = data
		if err := blockManifestWrites(manifestPath, backupPath); err != nil {
			sabotageErr = err
			return "", "", err
		}
		return "https://github.com/owner/repo/pull/7", "", nil
	})

	finalizeErr := orch.finalizeRun(context.Background(), store, &manifest, planningPath)
	if sabotageErr != nil {
		t.Fatalf("manifest sabotage failed: %v", sabotageErr)
	}
	if finalizeErr == nil {
		t.Fatal("finalizeRun with an unpersistable closeout result returned nil error")
	}
	if !strings.Contains(finalizeErr.Error(), "persist closeout result") {
		t.Errorf("finalizeRun error = %q, want a closeout result persistence failure", finalizeErr)
	}
	if len(reportBefore) == 0 {
		t.Fatal("final report was empty when closeout ran")
	}
	reportAfter, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read final report after closeout failed: %v", err)
	}
	if !bytes.Equal(reportBefore, reportAfter) {
		t.Error("final report changed after a closeout result write failure")
	}

	backup, err := NewManifestStore(backupPath).Read()
	if err != nil {
		t.Fatalf("read pre-failure manifest failed: %v", err)
	}
	if backup.CloseoutIntent != closeoutIntentPending {
		t.Errorf("on-disk CloseoutIntent = %q, want the intent to stay %q", backup.CloseoutIntent, closeoutIntentPending)
	}
	if backup.ReportState != reportStateWritten {
		t.Errorf("on-disk ReportState = %q, want %q", backup.ReportState, reportStateWritten)
	}
}

func TestFailureReportDoesNotOverwriteWrittenFinalReport(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "failureguard", Slug: "failure-guard"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	manifest.WorktreeBase = strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))

	reportPath := filepath.Join(identity.PlanningPath(manifest.WorktreePath), finalReportFileName)
	written := []byte(`{"run_id":"failureguard","status":"success"}`)
	if err := os.WriteFile(reportPath, written, 0o600); err != nil {
		t.Fatalf("write final report failed: %v", err)
	}
	manifest.ReportPath = reportPath
	manifest.ReportState = reportStateWritten
	manifest.FinalizationVersion = finalizationVersion
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	orch.tryFailureReport(context.Background(), store, &manifest)

	got, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read final report failed: %v", err)
	}
	if !bytes.Equal(got, written) {
		t.Errorf("final report was rewritten as %q, want %q", got, written)
	}
	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.ReportPath != reportPath {
		t.Errorf("persisted ReportPath = %q, want %q", persisted.ReportPath, reportPath)
	}
}

func TestResumeCloseoutRetrySurfacesReportFailureAndKeepsFailedState(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "retryreport", Slug: "retry-report"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = allPhasesDone()
	manifest.CurrentPhase = PhaseReview
	// A non-empty but unknown base stops Resume from inferring one and makes
	// report generation fail inside the closeout-only retry.
	manifest.WorktreeBase = strings.Repeat("d", 40)
	manifest.CloseoutState = closeoutStateFailed
	manifest.CloseoutNote = "gh auth expired"
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
	orch.deps.Config.OneShot.AutoPR = true
	closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
		return "https://github.com/owner/repo/pull/7", "", nil
	})
	messages := capturePhaseIndicatorMessages(orch)

	_, err := orch.Resume(context.Background())
	if err == nil {
		t.Fatal("Resume with a report failure returned nil error")
	}
	// The closeout-only retry regenerates the report before it touches the
	// closeout, so the failure surfaces as a finalization error.
	if !strings.Contains(err.Error(), "generate final report") {
		t.Fatalf("Resume error = %q, want a final report generation failure", err)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 when the report cannot be regenerated", *closeoutCalls)
	}
	// The bogus worktree base is the injected cause: report generation fails
	// while collecting the git snapshot's base commit, before closeout runs.
	wantMessage := fmt.Sprintf("generate report: collect git snapshot base commit: git merge-base HEAD %s:", manifest.WorktreeBase)
	if joined := strings.Join(*messages, "\n"); !strings.Contains(joined, wantMessage) {
		t.Errorf("phase indicator messages = %q, want one containing %q", *messages, wantMessage)
	}

	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateFailed || persisted.CloseoutNote != "gh auth expired" {
		t.Errorf("persisted closeout state = %q note = %q, want the legacy failed state with the original note", persisted.CloseoutState, persisted.CloseoutNote)
	}
	if persisted.ReportPath != "" {
		t.Errorf("persisted ReportPath = %q, want empty", persisted.ReportPath)
	}
	if persisted.CloseoutIntent != "" {
		t.Errorf("persisted CloseoutIntent = %q, want empty", persisted.CloseoutIntent)
	}
	factory := orch.deps.RunnerFactory.(*recordingRunnerFactory)
	factory.mu.Lock()
	runs := len(factory.calls)
	factory.mu.Unlock()
	if runs != 0 {
		t.Errorf("closeout retry restarted %d phases, want 0", runs)
	}
}

// hookedPhaseRunner runs afterRun once the wrapped runner returns.
type hookedPhaseRunner struct {
	inner    PhaseRunner
	afterRun func() error
}

func (r hookedPhaseRunner) RunPhase(ctx context.Context, in PhaseRunInput) (RunResult, error) {
	result, err := r.inner.RunPhase(ctx, in)
	if hookErr := r.afterRun(); hookErr != nil {
		return result, hookErr
	}
	return result, err
}

func TestResumeRecoversPhaseWhoseFinalStatusWriteFailed(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "stuckrun", Slug: "stuck-run"}
	manifest, store, _, orch := setupResumeTestFixture(t, projectRoot, identity)
	manifest.PhaseStatuses = map[Phase]PhaseStatus{
		PhasePlan:      PhaseStatusDone,
		PhaseImplement: PhaseStatusPending,
		PhaseReview:    PhaseStatusPending,
	}
	manifest.CurrentPhase = PhasePlan
	if err := store.Write(manifest); err != nil {
		t.Fatalf("store.Write failed: %v", err)
	}

	// Fail exactly the write that records a finished phase: swap the manifest
	// for a directory after the phase runner returns. The last persisted state
	// stays readable from the renamed file.
	manifestPath := identity.ManifestPath(projectRoot)
	runningPath := manifestPath + ".running"
	baseFactory := orch.deps.RunnerFactory
	phaseRuns := map[Phase]int{}
	sabotaged := false
	var sabotageErr error
	orch.deps.RunnerFactory = PhaseRunnerFactoryFunc(func(ctx context.Context, phase Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (PhaseRunner, error) {
		phaseRuns[phase]++
		inner, err := baseFactory.NewPhaseRunner(ctx, phase, modelAlias, approver, advisorCfg)
		if err != nil {
			return nil, err
		}
		return hookedPhaseRunner{inner: inner, afterRun: func() error {
			if phase != PhaseImplement || sabotaged {
				return nil
			}
			sabotaged = true
			if err := os.Rename(manifestPath, runningPath); err != nil {
				sabotageErr = err
				return err
			}
			if err := os.Mkdir(manifestPath, 0o755); err != nil {
				sabotageErr = err
				return err
			}
			return nil
		}}, nil
	})

	first, err := orch.Resume(context.Background())
	if err == nil {
		t.Fatal("Resume with a failing final phase write returned nil error")
	}
	if sabotageErr != nil {
		t.Fatalf("manifest sabotage failed: %v", sabotageErr)
	}
	if !strings.Contains(err.Error(), "write manifest") {
		t.Fatalf("Resume error = %q, want a manifest write failure", err)
	}
	if got := first.PhaseStatuses[PhaseImplement]; got != PhaseStatusFailed {
		t.Errorf("in-memory implement status = %q, want %q", got, PhaseStatusFailed)
	}

	persistedRunning, err := NewManifestStore(runningPath).Read()
	if err != nil {
		t.Fatalf("read persisted running manifest failed: %v", err)
	}
	if got := persistedRunning.PhaseStatuses[PhaseImplement]; got != PhaseStatusRunning {
		t.Errorf("persisted implement status = %q, want %q", got, PhaseStatusRunning)
	}
	if persistedRunning.CurrentPhase != PhaseImplement {
		t.Errorf("persisted current phase = %q, want %q", persistedRunning.CurrentPhase, PhaseImplement)
	}

	// Restore the last durable manifest and resume from the running status.
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest blocker failed: %v", err)
	}
	if err := os.Rename(runningPath, manifestPath); err != nil {
		t.Fatalf("restore manifest failed: %v", err)
	}

	second, err := orch.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume after restore failed: %v", err)
	}
	if got := second.PhaseStatuses[PhaseImplement]; got != PhaseStatusDone {
		t.Errorf("restarted implement status = %q, want %q", got, PhaseStatusDone)
	}
	if got := second.PhaseStatuses[PhaseReview]; got != PhaseStatusDone {
		t.Errorf("review status = %q, want %q", got, PhaseStatusDone)
	}
	if got := phaseRuns[PhaseImplement]; got != 2 {
		t.Errorf("implement runs = %d, want 2 (before and after recovery)", got)
	}
	if got := phaseRuns[PhaseReview]; got != 1 {
		t.Errorf("review runs = %d, want 1", got)
	}
	if _, reRanPlan := phaseRuns[PhasePlan]; reRanPlan {
		t.Error("plan re-ran, want the done phase skipped")
	}

	final, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	for _, phase := range phaseOrder() {
		if got := final.PhaseStatuses[phase]; got != PhaseStatusDone {
			t.Errorf("final persisted %s status = %q, want %q", phase, got, PhaseStatusDone)
		}
	}
}
