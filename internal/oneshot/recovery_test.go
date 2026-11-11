package oneshot

import (
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

func TestCloseoutRetryableOnlyForFinishedPhasesAndFailedCloseout(t *testing.T) {
	cases := []struct {
		name     string
		statuses map[Phase]PhaseStatus
		state    string
		want     bool
	}{
		{
			name:     "failed closeout after done phases",
			statuses: allPhasesDone(),
			state:    closeoutStateFailed,
			want:     true,
		},
		{
			name: "failed closeout after skipped phases",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusSkipped, PhaseImplement: PhaseStatusSkipped, PhaseReview: PhaseStatusSkipped,
			},
			state: closeoutStateFailed,
			want:  true,
		},
		{
			name: "incomplete phase blocks closeout retry",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatusFailed, PhaseReview: PhaseStatusDone,
			},
			state: closeoutStateFailed,
			want:  false,
		},
		{
			name: "unknown phase status blocks closeout retry",
			statuses: map[Phase]PhaseStatus{
				PhasePlan: PhaseStatusDone, PhaseImplement: PhaseStatus("interrupted"), PhaseReview: PhaseStatusDone,
			},
			state: closeoutStateFailed,
			want:  false,
		},
		{name: "created closeout is terminal", statuses: allPhasesDone(), state: closeoutStateCreated, want: false},
		{name: "missing closeout state is terminal", statuses: allPhasesDone(), state: "", want: false},
		{name: "skipped closeout is terminal", statuses: allPhasesDone(), state: closeoutStateSkipped, want: false},
		{name: "unsupported closeout is terminal", statuses: allPhasesDone(), state: closeoutStateUnsupported, want: false},
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

	orch.finalizeRun(context.Background(), store, &manifest, planningPath)

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
	if joined := strings.Join(*messages, "\n"); !strings.Contains(joined, "generate report:") {
		t.Errorf("phase indicator messages = %q, want one naming generate report", *messages)
	}
}

func TestFinalizeRunCloseoutSurvivesManifestWriteFailure(t *testing.T) {
	cases := []struct {
		name        string
		closeoutErr error
		wantState   string
		wantURL     bool
		wantNote    string
		wantEvent   string
	}{
		{
			name:      "created closeout",
			wantState: closeoutStateCreated,
			wantURL:   true,
			wantEvent: "write manifest with closeout result",
		},
		{
			name:        "failed closeout",
			closeoutErr: errors.New("gh auth expired"),
			wantState:   closeoutStateFailed,
			wantNote:    "gh auth expired",
			wantEvent:   "write manifest with failed closeout",
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

			// Replace the manifest with a directory so every later atomic write
			// fails; the pre-failure state stays readable from the backup.
			manifestPath := identity.ManifestPath(projectRoot)
			backupPath := manifestPath + ".before"
			if err := os.Rename(manifestPath, backupPath); err != nil {
				t.Fatalf("move manifest aside failed: %v", err)
			}
			if err := os.Mkdir(manifestPath, 0o755); err != nil {
				t.Fatalf("create manifest path blocker failed: %v", err)
			}

			runGitTest(t, projectRoot, "remote", "set-url", "origin", "git@github.com:owner/repo.git")
			orch.deps.Config.OneShot.AutoPR = true
			closeoutCalls := stubGitHubCloseout(t, func(context.Context, closeoutRequest) (string, string, error) {
				if c.closeoutErr != nil {
					return "", "", c.closeoutErr
				}
				return "https://github.com/owner/repo/pull/7", "", nil
			})
			messages := capturePhaseIndicatorMessages(orch)

			orch.finalizeRun(context.Background(), store, &manifest, identity.PlanningPath(manifest.WorktreePath))

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
			if backup.ReportPath != "" || backup.CloseoutState != "" {
				t.Errorf("pre-failure manifest gained report path %q closeout state %q", backup.ReportPath, backup.CloseoutState)
			}

			joined := strings.Join(*messages, "\n")
			for _, want := range []string{"write manifest with report path", c.wantEvent} {
				if !strings.Contains(joined, want) {
					t.Errorf("phase indicator messages = %q, want one containing %q", *messages, want)
				}
			}
		})
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
	wantErr := fmt.Sprintf("closeout for %s failed again: gh auth expired", identity.ID)
	if !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("Resume error = %q, want it to contain %q", err, wantErr)
	}
	if *closeoutCalls != 0 {
		t.Errorf("closeout calls = %d, want 0 when the report cannot be regenerated", *closeoutCalls)
	}
	if joined := strings.Join(*messages, "\n"); !strings.Contains(joined, "generate report:") {
		t.Errorf("phase indicator messages = %q, want one naming generate report", *messages)
	}

	persisted, err := store.Read()
	if err != nil {
		t.Fatalf("store.Read failed: %v", err)
	}
	if persisted.CloseoutState != closeoutStateFailed || persisted.CloseoutNote != "gh auth expired" {
		t.Errorf("persisted closeout state = %q note = %q, want failed with the original note", persisted.CloseoutState, persisted.CloseoutNote)
	}
	if persisted.ReportPath != "" {
		t.Errorf("persisted ReportPath = %q, want empty", persisted.ReportPath)
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
