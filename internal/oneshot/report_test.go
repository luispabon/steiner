package oneshot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssembleAndWriteFinalReportSuccess(t *testing.T) {
	manifest := Manifest{
		RunID:        "abc123",
		Slug:         "build-parser",
		Task:         "Build the parser",
		Branch:       "oneshot/build-parser-abc123",
		WorktreePath: filepath.Join(t.TempDir(), "worktree"),
		CurrentPhase: PhaseReview,
		PhaseSessionIDs: map[Phase]string{
			PhaseReview:    "session-review",
			PhasePlan:      "session-plan",
			PhaseImplement: "session-implement",
		},
	}
	git := GitSnapshot{
		Branch:       "oneshot/build-parser-abc123",
		Commit:       strings.Repeat("a", 40),
		ShortCommit:  strings.Repeat("a", 7),
		Dirty:        false,
		FilesChanged: []string{"internal/oneshot/report.go", "internal/oneshot/report_test.go"},
	}
	review := ReviewOutcome{
		Summary: "review passed",
		Passed:  true,
		ChangesMade: []string{
			"assembled a bounded final report",
			"wrote the report to the run planning folder",
		},
		FilesChanged: []string{"internal/oneshot/report.go"},
		Validation: []ValidationRun{
			{Command: "go test ./internal/oneshot/...", Result: "ok", Passed: true},
		},
		ReviewFixIterations: []ReviewFixIteration{
			{Iteration: 1, Summary: "tightened validation wording", Result: "ok"},
		},
		Assumptions:      []string{"review outcome is already final"},
		Risks:            []string{"report size is bounded"},
		UnresolvedIssues: []string{"none"},
	}

	report := AssembleFinalReport(manifest, git, review)
	if got, want := report.Status, "success"; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
	if !report.Completion {
		t.Fatal("Completion = false, want true")
	}
	if got, want := len(report.PhaseSessions), 3; got != want {
		t.Fatalf("PhaseSessions len = %d, want %d", got, want)
	}
	if got, want := report.PhaseSessions[0].Phase, PhasePlan; got != want {
		t.Fatalf("PhaseSessions[0].Phase = %q, want %q", got, want)
	}
	if got, want := report.PhaseSessions[2].SessionID, "session-review"; got != want {
		t.Fatalf("PhaseSessions[2].SessionID = %q, want %q", got, want)
	}

	path, err := WriteFinalReport(&report)
	if err != nil {
		t.Fatalf("WriteFinalReport failed: %v", err)
	}
	if got, want := path, report.ReportPath; got != want {
		t.Fatalf("report path = %q, want %q", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var stored FinalReport
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if got, want := stored.Branch, manifest.Branch; got != want {
		t.Fatalf("Branch = %q, want %q", got, want)
	}
	if got, want := stored.WorktreePath, manifest.WorktreePath; got != want {
		t.Fatalf("WorktreePath = %q, want %q", got, want)
	}
	if got, want := stored.Validation[0].Command, "go test ./internal/oneshot/..."; got != want {
		t.Fatalf("Validation[0].Command = %q, want %q", got, want)
	}
	if stored.Failure != nil {
		t.Fatalf("Failure = %#v, want nil", stored.Failure)
	}
}

func TestGenerateFinalReportFailureShape(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}
	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	if err := os.WriteFile(filepath.Join(worktree.Path, "local.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	manifest := Manifest{
		RunID:        identity.ID,
		Slug:         identity.Slug,
		Task:         "Build the parser",
		Branch:       identity.BranchName(),
		WorktreePath: worktree.Path,
		WorktreeBase: worktree.StartPoint,
		PhaseSessionIDs: map[Phase]string{
			PhasePlan:      "plan-session",
			PhaseImplement: "implement-session",
		},
	}

	report, err := GenerateFinalReport(context.Background(), manifest, ReviewOutcome{
		Summary:           "review blocked",
		Passed:            false,
		Attempted:         "rerun validation and address findings",
		Blocked:           "boundary artifacts missing",
		ChangesLeftBehind: true,
		LeftBehindSummary: "dirty worktree contains an untracked file",
		Validation: []ValidationRun{
			{Command: "go test ./internal/oneshot/...", Result: "failed", Passed: false},
		},
	})
	if err != nil {
		t.Fatalf("GenerateFinalReport failed: %v", err)
	}

	if report.Completion {
		t.Fatal("Completion = true, want false")
	}
	if report.Failure == nil {
		t.Fatal("Failure = nil, want failure report")
	}
	if got, want := report.Failure.Attempted, "rerun validation and address findings"; got != want {
		t.Fatalf("Failure.Attempted = %q, want %q", got, want)
	}
	if got, want := report.Failure.Blocked, "boundary artifacts missing"; got != want {
		t.Fatalf("Failure.Blocked = %q, want %q", got, want)
	}
	if !report.Failure.ChangesLeftBehind {
		t.Fatal("Failure.ChangesLeftBehind = false, want true")
	}
	if got := report.ReportPath; got == "" {
		t.Fatal("ReportPath is empty")
	}
	if _, err := os.Stat(report.ReportPath); err != nil {
		t.Fatalf("report file missing: %v", err)
	}
}

func TestGenerateFinalReportUsesBaseInLocalOnlyRepo(t *testing.T) {
	projectRoot := setupLocalGitRepo(t)
	identity := RunIdentity{ID: "local123", Slug: "local-only"}
	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	if err := os.WriteFile(filepath.Join(worktree.Path, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGitTest(t, worktree.Path, "add", "feature.txt")
	runGitTest(t, worktree.Path, "commit", "-m", "feature: add file")

	report, err := GenerateFinalReport(context.Background(), Manifest{
		RunID:        identity.ID,
		Slug:         identity.Slug,
		Branch:       identity.BranchName(),
		WorktreePath: worktree.Path,
		WorktreeBase: worktree.StartPoint,
	}, ReviewOutcome{Passed: true, FilesChanged: []string{"feature.txt"}})
	if err != nil {
		t.Fatalf("GenerateFinalReport failed: %v", err)
	}
	if !containsString(report.FilesChanged, "feature.txt") {
		t.Fatalf("report files changed = %v, want feature.txt", report.FilesChanged)
	}
	if !containsString(report.Git.FilesChanged, "feature.txt") {
		t.Fatalf("git snapshot files changed = %v, want feature.txt", report.Git.FilesChanged)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
