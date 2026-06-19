package oneshot

import (
	"path/filepath"
	"testing"
)

func TestRequiredArtifactsForPhasePlan(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	required := requiredArtifactsForPhase(PhasePlan, planningPath, false)

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

func TestRequiredArtifactsForPhaseImplementFreshRun(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	// Fresh run (not resuming) should require execution.md
	required := requiredArtifactsForPhase(PhaseImplement, planningPath, false)

	expectedOverview := filepath.Join(planningPath, "overview.md")
	expectedPlan := filepath.Join(planningPath, "plan.yaml")
	expectedExecution := filepath.Join(planningPath, "execution.md")

	if len(required) != 3 {
		t.Fatalf("implement phase fresh run required %d artifacts, want 3", len(required))
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

func TestRequiredArtifactsForPhaseImplementResume(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	// Resume (phaseIsResuming=true) should NOT require execution.md
	required := requiredArtifactsForPhase(PhaseImplement, planningPath, true)

	expectedOverview := filepath.Join(planningPath, "overview.md")
	expectedPlan := filepath.Join(planningPath, "plan.yaml")
	expectedExecution := filepath.Join(planningPath, "execution.md")

	if len(required) != 2 {
		t.Fatalf("implement phase resume required %d artifacts, want 2 (no execution.md)", len(required))
	}
	if required[0] != expectedOverview {
		t.Errorf("implement phase required[0] = %q, want %q", required[0], expectedOverview)
	}
	if required[1] != expectedPlan {
		t.Errorf("implement phase required[1] = %q, want %q", required[1], expectedPlan)
	}

	// Verify execution.md is not in the list
	for _, artifact := range required {
		if artifact == expectedExecution {
			t.Fatalf("implement phase resume unexpectedly requires execution.md")
		}
	}
}

func TestRequiredArtifactsForPhaseReviewFreshRun(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	// Review always requires execution.md and review.md, regardless of resuming
	required := requiredArtifactsForPhase(PhaseReview, planningPath, false)

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

func TestRequiredArtifactsForPhaseReviewResume(t *testing.T) {
	t.Parallel()

	planningPath := "/tmp/planning"
	// Review always requires execution.md and review.md, even on resume
	required := requiredArtifactsForPhase(PhaseReview, planningPath, true)

	expectedOverview := filepath.Join(planningPath, "overview.md")
	expectedPlan := filepath.Join(planningPath, "plan.yaml")
	expectedExecution := filepath.Join(planningPath, "execution.md")
	expectedReview := filepath.Join(planningPath, "review.md")

	if len(required) != 4 {
		t.Fatalf("review phase resume required %d artifacts, want 4", len(required))
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
