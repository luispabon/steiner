package oneshot

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRequiredArtifactsForPhasePlan(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	required := requiredArtifactsForPhase(PhasePlan, planningPath)

	expectedOverview := filepath.Join(planningPath, "overview.md")
	expectedPlan := filepath.Join(planningPath, "plan.yaml")

	if len(required) != 2 {
		t.Fatalf("plan phase required %d artifacts, want 2", len(required))
	}
	if required[0] != expectedOverview {
		t.Errorf("plan phase required[0] = %q, want %q", required[0], expectedOverview)
	}
	if required[1] != expectedPlan {
		t.Errorf("plan phase required[1] = %q, want %q", required[1], expectedPlan)
	}
}

func TestRequiredArtifactsForPhaseImplement(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	// The boundary check runs after the phase runner returns, so execution.md is
	// required once implement completes whether the phase is fresh or resumed
	// after a mid-implement failure: only the pre-run resume path skips it.
	required := requiredArtifactsForPhase(PhaseImplement, planningPath)

	expectedOverview := filepath.Join(planningPath, "overview.md")
	expectedPlan := filepath.Join(planningPath, "plan.yaml")
	expectedExecution := filepath.Join(planningPath, "execution.md")

	if len(required) != 3 {
		t.Fatalf("implement phase required %d artifacts, want 3", len(required))
	}
	if required[0] != expectedOverview {
		t.Errorf("implement phase required[0] = %q, want %q", required[0], expectedOverview)
	}
	if required[1] != expectedPlan {
		t.Errorf("implement phase required[1] = %q, want %q", required[1], expectedPlan)
	}
	if required[2] != expectedExecution {
		t.Errorf("implement phase required[2] = %q, want %q", required[2], expectedExecution)
	}
}

func TestRequiredArtifactsForPhaseReview(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	// Review runs only after implement completes, so execution.md is always
	// required alongside review.md.
	required := requiredArtifactsForPhase(PhaseReview, planningPath)

	expectedOverview := filepath.Join(planningPath, "overview.md")
	expectedPlan := filepath.Join(planningPath, "plan.yaml")
	expectedExecution := filepath.Join(planningPath, "execution.md")
	expectedReview := filepath.Join(planningPath, "review.md")

	if len(required) != 4 {
		t.Fatalf("review phase required %d artifacts, want 4", len(required))
	}
	if required[0] != expectedOverview {
		t.Errorf("review phase required[0] = %q, want %q", required[0], expectedOverview)
	}
	if required[1] != expectedPlan {
		t.Errorf("review phase required[1] = %q, want %q", required[1], expectedPlan)
	}
	if required[2] != expectedExecution {
		t.Errorf("review phase required[2] = %q, want %q", required[2], expectedExecution)
	}
	if required[3] != expectedReview {
		t.Errorf("review phase required[3] = %q, want %q", required[3], expectedReview)
	}
}

func TestPorcelainPaths(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{"unstaged first line", " M a/b.go\n", []string{"a/b.go"}},
		{"staged", "M  a/b.go\n", []string{"a/b.go"}},
		{"untracked", "?? new.go\n", []string{"new.go"}},
		{"rename", "R  old -> new\n", []string{"new"}},
		{"steiner first line", " M .steiner/x\n M a.go\n", []string{"a.go"}},
		{"steiner later line", " M a.go\n M .steiner/x\n?? .steiner/y\n", []string{"a.go"}},
		{"mixed", " M a/b.go\nM  c.go\n?? d.go\nR  e -> f.go\n", []string{"a/b.go", "c.go", "d.go", "f.go"}},
		{"empty", "", []string{}},
		{"blank lines", "\n M a.go\n\n", []string{"a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := porcelainPaths(tt.out)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("porcelainPaths(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

func TestDirtyPathsAndChangedFilesKeepUnstagedPaths(t *testing.T) {
	repo := setupLocalGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".steiner"), 0o755); err != nil {
		t.Fatalf("mkdir .steiner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".steiner", "x"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write .steiner/x: %v", err)
	}

	want := []string{"README.md"}
	dirty, err := dirtyPaths(context.Background(), repo)
	if err != nil {
		t.Fatalf("dirtyPaths failed: %v", err)
	}
	if !slices.Equal(dirty, want) {
		t.Fatalf("dirtyPaths = %q, want %q", dirty, want)
	}

	changed, ok, err := collectChangedFiles(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatalf("collectChangedFiles failed: %v", err)
	}
	if !ok || !slices.Equal(changed, want) {
		t.Fatalf("collectChangedFiles = %q (%t), want %q", changed, ok, want)
	}
}
