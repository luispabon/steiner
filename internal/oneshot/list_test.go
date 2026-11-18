package oneshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResumableRunStatusLabelsFinalizationWork(t *testing.T) {
	cases := []struct {
		name  string
		phase Phase
		state lockState
		want  string
	}{
		{name: "pending report", phase: phaseReport, state: lockStateAbsent, want: "finish report"},
		{name: "pending report with stale lock", phase: phaseReport, state: lockStateStale, want: "finish report (stale lock)"},
		{name: "unknown closeout", phase: phaseResolve, state: lockStateAbsent, want: "resolve closeout"},
		{name: "unknown closeout with stale lock", phase: phaseResolve, state: lockStateStale, want: "resolve closeout (stale lock)"},
		{name: "authorised retry", phase: phaseCloseout, state: lockStateAbsent, want: "retry closeout"},
		{name: "authorised retry with stale lock", phase: phaseCloseout, state: lockStateStale, want: "retry closeout (stale lock)"},
		{name: "incomplete phase", phase: PhaseReview, state: lockStateAbsent, want: "resume at review"},
		{name: "incomplete phase with stale lock", phase: PhaseReview, state: lockStateStale, want: "resume at review (stale lock)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resumableRunStatus(c.phase, c.state); got != c.want {
				t.Errorf("resumableRunStatus(%q, %q) = %q, want %q", c.phase, c.state, got, c.want)
			}
		})
	}
}

func TestListResumableRunsClassifiesFinalizationWork(t *testing.T) {
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
	newManifest := func(identity RunIdentity, state string, reportState string) Manifest {
		return Manifest{
			RunID:               identity.ID,
			Slug:                identity.Slug,
			Task:                identity.Slug,
			Branch:              identity.BranchName(),
			WorktreePath:        identity.WorktreePath(projectRoot),
			CurrentPhase:        PhaseReview,
			PhaseStatuses:       allPhasesDone(),
			FinalizationVersion: finalizationVersion,
			ReportState:         reportState,
			ReportPath:          filepath.Join(identity.PlanningPath(identity.WorktreePath(projectRoot)), finalReportFileName),
			CloseoutState:       state,
			CreatedAt:           now.Add(-time.Hour),
			UpdatedAt:           now,
		}
	}

	reportIdentity := RunIdentity{ID: "rpt001", Slug: "pending-report"}
	reportManifest := newManifest(reportIdentity, "", reportStatePending)
	reportManifest.ReportPath = ""
	writeManifest(reportIdentity, reportManifest)

	resolveIdentity := RunIdentity{ID: "res001", Slug: "unknown-closeout"}
	writeManifest(resolveIdentity, newManifest(resolveIdentity, closeoutStateUnknown, reportStateWritten))

	retryIdentity := RunIdentity{ID: "ret001", Slug: "authorised-retry"}
	writeManifest(retryIdentity, newManifest(retryIdentity, closeoutStateRetryAuthorized, reportStateWritten))

	completeIdentity := RunIdentity{ID: "cmp001", Slug: "settled"}
	writeManifest(completeIdentity, newManifest(completeIdentity, closeoutStateCreated, reportStateWritten))

	runs, err := ListResumableRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	want := map[string]struct {
		phase  Phase
		status string
	}{
		reportIdentity.ID:  {phase: phaseReport, status: "finish report"},
		resolveIdentity.ID: {phase: phaseResolve, status: "resolve closeout"},
		retryIdentity.ID:   {phase: phaseCloseout, status: "retry closeout"},
	}
	if got := len(runs); got != len(want) {
		t.Fatalf("ListResumableRuns returned %d runs, want %d: %+v", got, len(want), runs)
	}
	seen := make(map[string]bool, len(runs))
	for _, run := range runs {
		expected, ok := want[run.RunID]
		if !ok {
			t.Errorf("run %s listed as phase %q status %q, want it skipped as settled", run.RunID, run.ResumePhase, run.Status)
			continue
		}
		seen[run.RunID] = true
		if run.ResumePhase != expected.phase {
			t.Errorf("run %s ResumePhase = %q, want %q", run.RunID, run.ResumePhase, expected.phase)
		}
		if run.Status != expected.status {
			t.Errorf("run %s Status = %q, want %q", run.RunID, run.Status, expected.status)
		}
	}
	for runID := range want {
		if !seen[runID] {
			t.Errorf("run %s is missing from the listing", runID)
		}
	}
}
