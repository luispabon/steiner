package oneshot

import (
	"path/filepath"
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
