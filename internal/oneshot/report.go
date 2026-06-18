package oneshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	finalReportFileName  = "final-report.json"
	finalReportItemLimit = 8
	finalReportTextLimit = 240
)

// GitSnapshot captures the git state used to describe the run closeout.
type GitSnapshot struct {
	Branch       string   `json:"branch,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	ShortCommit  string   `json:"short_commit,omitempty"`
	Dirty        bool     `json:"dirty"`
	FilesChanged []string `json:"files_changed,omitempty"`
}

// ValidationRun captures a validation command and its result.
type ValidationRun struct {
	Command string `json:"command"`
	Result  string `json:"result,omitempty"`
	Passed  bool   `json:"passed"`
}

// ReviewFixIteration captures one review remediation pass.
type ReviewFixIteration struct {
	Iteration int    `json:"iteration"`
	Summary   string `json:"summary,omitempty"`
	Result    string `json:"result,omitempty"`
}

// ReviewOutcome captures the review phase output used to shape the final report.
type ReviewOutcome struct {
	Summary             string               `json:"summary,omitempty"`
	Passed              bool                 `json:"passed"`
	ChangesMade         []string             `json:"changes_made,omitempty"`
	FilesChanged        []string             `json:"files_changed,omitempty"`
	Validation          []ValidationRun      `json:"validation,omitempty"`
	ReviewFixIterations []ReviewFixIteration `json:"review_fix_iterations,omitempty"`
	Assumptions         []string             `json:"assumptions,omitempty"`
	Risks               []string             `json:"risks,omitempty"`
	UnresolvedIssues    []string             `json:"unresolved_issues,omitempty"`
	Attempted           string               `json:"attempted,omitempty"`
	Blocked             string               `json:"blocked,omitempty"`
	ChangesLeftBehind   bool                 `json:"changes_left_behind"`
	LeftBehindSummary   string               `json:"left_behind_summary,omitempty"`
}

// PhaseSessionReport captures the durable session id for a oneshot phase.
type PhaseSessionReport struct {
	Phase     Phase  `json:"phase"`
	SessionID string `json:"session_id"`
}

// FailureReport captures the failure-shape closeout details.
type FailureReport struct {
	Attempted         string `json:"attempted,omitempty"`
	Blocked           string `json:"blocked,omitempty"`
	ChangesLeftBehind bool   `json:"changes_left_behind"`
	LeftBehindSummary string `json:"left_behind_summary,omitempty"`
}

// FinalReport is the bounded structured oneshot closeout artifact.
type FinalReport struct {
	RunID               string               `json:"run_id"`
	Slug                string               `json:"slug,omitempty"`
	TaskSummary         string               `json:"task_summary"`
	Branch              string               `json:"branch"`
	WorktreePath        string               `json:"worktree_path"`
	PlanningPath        string               `json:"planning_path"`
	ReportPath          string               `json:"report_path,omitempty"`
	CurrentPhase        Phase                `json:"current_phase,omitempty"`
	Status              string               `json:"status"`
	Completion          bool                 `json:"completion"`
	PhaseSessions       []PhaseSessionReport `json:"phase_sessions,omitempty"`
	Git                 GitSnapshot          `json:"git"`
	ChangesMade         []string             `json:"changes_made,omitempty"`
	FilesChanged        []string             `json:"files_changed,omitempty"`
	Validation          []ValidationRun      `json:"validation,omitempty"`
	ReviewOutcome       string               `json:"review_outcome,omitempty"`
	ReviewFixIterations []ReviewFixIteration `json:"review_fix_iterations,omitempty"`
	Assumptions         []string             `json:"assumptions,omitempty"`
	Risks               []string             `json:"risks,omitempty"`
	UnresolvedIssues    []string             `json:"unresolved_issues,omitempty"`
	Failure             *FailureReport       `json:"failure,omitempty"`
	GeneratedAt         time.Time            `json:"generated_at"`
}

// GenerateFinalReport assembles, writes, and returns the final oneshot report.
func GenerateFinalReport(ctx context.Context, manifest Manifest, review ReviewOutcome) (FinalReport, error) {
	if strings.TrimSpace(manifest.RunID) == "" {
		return FinalReport{}, fmt.Errorf("generate final report: manifest run id is required")
	}
	if strings.TrimSpace(manifest.WorktreePath) == "" {
		return FinalReport{}, fmt.Errorf("generate final report: manifest worktree path is required")
	}
	git, err := CollectGitSnapshot(ctx, manifest.WorktreePath)
	if err != nil {
		return FinalReport{}, err
	}

	report := AssembleFinalReport(manifest, git, review)
	if _, err := WriteFinalReport(&report); err != nil {
		return FinalReport{}, err
	}

	return report, nil
}

// CollectGitSnapshot reads the git state needed to describe a oneshot run.
func CollectGitSnapshot(ctx context.Context, worktreePath string) (GitSnapshot, error) {
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return GitSnapshot{}, fmt.Errorf("collect git snapshot: worktree path is required")
	}

	branch, err := gitOutput(ctx, worktreePath, "branch", "--show-current")
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("collect git snapshot branch: %w", err)
	}
	commit, err := gitOutput(ctx, worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("collect git snapshot commit: %w", err)
	}
	shortCommit, err := gitOutput(ctx, worktreePath, "rev-parse", "--short", "HEAD")
	if err != nil {
		return GitSnapshot{}, fmt.Errorf("collect git snapshot short commit: %w", err)
	}
	filesChanged, dirty, err := collectChangedFiles(ctx, worktreePath)
	if err != nil {
		return GitSnapshot{}, err
	}

	return GitSnapshot{
		Branch:       strings.TrimSpace(branch),
		Commit:       strings.TrimSpace(commit),
		ShortCommit:  strings.TrimSpace(shortCommit),
		Dirty:        dirty,
		FilesChanged: filesChanged,
	}, nil
}

// AssembleFinalReport combines manifest, git, and review data into a structured report.
func AssembleFinalReport(manifest Manifest, git GitSnapshot, review ReviewOutcome) FinalReport {
	phaseSessions := phaseSessionReports(manifest.PhaseSessionIDs)
	filesChanged := mergeBoundedStrings(git.FilesChanged, review.FilesChanged)
	changesMade := sanitizeStrings(review.ChangesMade, finalReportItemLimit)
	validation := sanitizeValidationRuns(review.Validation)
	fixIterations := sanitizeFixIterations(review.ReviewFixIterations)
	assumptions := sanitizeStrings(review.Assumptions, finalReportItemLimit)
	risks := sanitizeStrings(review.Risks, finalReportItemLimit)
	unresolved := sanitizeStrings(review.UnresolvedIssues, finalReportItemLimit)

	status := "failure"
	if review.Passed {
		status = "success"
	}

	reviewOutcome := strings.TrimSpace(review.Summary)
	if reviewOutcome == "" {
		if review.Passed {
			reviewOutcome = "review passed"
		} else {
			reviewOutcome = "review failed"
		}
	}

	report := FinalReport{
		RunID:               strings.TrimSpace(manifest.RunID),
		Slug:                strings.TrimSpace(manifest.Slug),
		TaskSummary:         compactText(manifest.Task),
		Branch:              pickFirst(strings.TrimSpace(git.Branch), strings.TrimSpace(manifest.Branch)),
		WorktreePath:        strings.TrimSpace(manifest.WorktreePath),
		PlanningPath:        planningPathForManifest(manifest),
		CurrentPhase:        manifest.CurrentPhase,
		Status:              status,
		Completion:          review.Passed,
		PhaseSessions:       phaseSessions,
		Git:                 GitSnapshot{Branch: strings.TrimSpace(git.Branch), Commit: strings.TrimSpace(git.Commit), ShortCommit: strings.TrimSpace(git.ShortCommit), Dirty: git.Dirty, FilesChanged: sanitizeStrings(git.FilesChanged, finalReportItemLimit)},
		ChangesMade:         changesMade,
		FilesChanged:        filesChanged,
		Validation:          validation,
		ReviewOutcome:       compactText(reviewOutcome),
		ReviewFixIterations: fixIterations,
		Assumptions:         assumptions,
		Risks:               risks,
		UnresolvedIssues:    unresolved,
		GeneratedAt:         time.Now().UTC(),
	}

	if !review.Passed || review.Attempted != "" || review.Blocked != "" || review.ChangesLeftBehind || review.LeftBehindSummary != "" {
		report.Failure = &FailureReport{
			Attempted:         compactText(review.Attempted),
			Blocked:           compactText(review.Blocked),
			ChangesLeftBehind: review.ChangesLeftBehind,
			LeftBehindSummary: compactText(review.LeftBehindSummary),
		}
	}

	return report
}

// WriteFinalReport persists the report to the run planning folder and returns the path.
func WriteFinalReport(report *FinalReport) (string, error) {
	if report == nil {
		return "", fmt.Errorf("write final report: report is required")
	}
	if strings.TrimSpace(report.PlanningPath) == "" {
		return "", fmt.Errorf("write final report: planning path is required")
	}

	path := reportFilePath(report.PlanningPath)
	report.ReportPath = path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create final report directory: %w", err)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal final report: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-final-report-*")
	if err != nil {
		return "", fmt.Errorf("create final report temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write final report temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close final report temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("rename final report temp file: %w", err)
	}
	return path, nil
}

func planningPathForManifest(manifest Manifest) string {
	return filepath.Join(strings.TrimSpace(manifest.WorktreePath), steinerDirName, plansDirName, "oneshot-"+strings.TrimSpace(manifest.RunID))
}

func reportFilePath(planningPath string) string {
	return filepath.Join(strings.TrimSpace(planningPath), finalReportFileName)
}

func phaseSessionReports(sessions map[Phase]string) []PhaseSessionReport {
	if len(sessions) == 0 {
		return nil
	}

	phases := make([]Phase, 0, len(sessions))
	for phase, sessionID := range sessions {
		if strings.TrimSpace(sessionID) == "" {
			continue
		}
		phases = append(phases, phase)
	}
	sort.Slice(phases, func(i, j int) bool {
		return phaseRank(phases[i]) < phaseRank(phases[j])
	})

	out := make([]PhaseSessionReport, 0, len(phases))
	for _, phase := range phases {
		out = append(out, PhaseSessionReport{
			Phase:     phase,
			SessionID: compactText(sessions[phase]),
		})
	}
	return out
}

func phaseRank(phase Phase) int {
	switch phase {
	case PhasePlan:
		return 0
	case PhaseImplement:
		return 1
	case PhaseReview:
		return 2
	default:
		return 99
	}
}

func collectChangedFiles(ctx context.Context, worktreePath string) ([]string, bool, error) {
	baseCommit, err := gitOutput(ctx, worktreePath, "merge-base", "HEAD", defaultWorktreeStartPoint)
	if err != nil {
		return nil, false, fmt.Errorf("collect git snapshot base commit: %w", err)
	}

	diffOut, err := gitOutput(ctx, worktreePath, "diff", "--name-only", "--diff-filter=ACMRTUXB", strings.TrimSpace(baseCommit)+"...HEAD")
	if err != nil {
		return nil, false, fmt.Errorf("collect git snapshot diff: %w", err)
	}

	statusOut, err := gitOutput(ctx, worktreePath, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, false, fmt.Errorf("collect git snapshot status: %w", err)
	}

	changed := make([]string, 0, 8)
	seen := make(map[string]struct{})
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || strings.HasPrefix(path, ".steiner/") || path == ".steiner" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		changed = append(changed, path)
	}

	for _, line := range strings.Split(strings.TrimSpace(diffOut), "\n") {
		add(line)
	}
	for _, line := range strings.Split(strings.TrimSpace(statusOut), "\n") {
		add(porcelainPath(line))
	}

	return sanitizeStrings(changed, finalReportItemLimit), len(changed) > 0, nil
}

func sanitizeValidationRuns(runs []ValidationRun) []ValidationRun {
	if len(runs) == 0 {
		return nil
	}
	out := make([]ValidationRun, 0, minInt(len(runs), finalReportItemLimit))
	for _, run := range runs {
		if len(out) >= finalReportItemLimit {
			break
		}
		out = append(out, ValidationRun{
			Command: compactText(run.Command),
			Result:  compactText(run.Result),
			Passed:  run.Passed,
		})
	}
	return out
}

func sanitizeFixIterations(iterations []ReviewFixIteration) []ReviewFixIteration {
	if len(iterations) == 0 {
		return nil
	}
	out := make([]ReviewFixIteration, 0, minInt(len(iterations), finalReportItemLimit))
	for _, iteration := range iterations {
		if len(out) >= finalReportItemLimit {
			break
		}
		out = append(out, ReviewFixIteration{
			Iteration: iteration.Iteration,
			Summary:   compactText(iteration.Summary),
			Result:    compactText(iteration.Result),
		})
	}
	return out
}

func sanitizeStrings(values []string, limit int) []string {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	out := make([]string, 0, minInt(len(values), limit))
	for _, value := range values {
		if len(out) >= limit {
			break
		}
		value = compactText(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func mergeBoundedStrings(values ...[]string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0, finalReportItemLimit)
	for _, list := range values {
		for _, value := range list {
			value = compactText(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
			if len(merged) >= finalReportItemLimit {
				return merged
			}
		}
	}
	return merged
}

func compactText(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > finalReportTextLimit {
		return string(runes[:finalReportTextLimit])
	}
	return value
}

func pickFirst(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
