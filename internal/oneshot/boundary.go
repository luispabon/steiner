package oneshot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BoundaryError reports why a phase boundary check failed.
type BoundaryError struct {
	Phase            Phase
	MissingArtifacts []string
	DirtyPaths       []string
}

func (e *BoundaryError) Error() string {
	parts := make([]string, 0, 2)
	if len(e.MissingArtifacts) > 0 {
		parts = append(parts, fmt.Sprintf("missing artifacts: %s", strings.Join(e.MissingArtifacts, ", ")))
	}
	if len(e.DirtyPaths) > 0 {
		parts = append(parts, fmt.Sprintf("dirty tree entries: %s", strings.Join(e.DirtyPaths, ", ")))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("phase %s boundary failed", e.Phase)
	}
	return fmt.Sprintf("phase %s boundary failed: %s", e.Phase, strings.Join(parts, "; "))
}

// requiredArtifactsForPhase returns the planning artifacts that must exist on
// disk once the given phase completes. Artifacts accumulate across phases: the
// plan phase produces overview.md and plan.yaml, implement adds execution.md,
// and review adds review.md.
func requiredArtifactsForPhase(phase Phase, planningPath string) []string {
	required := []string{
		filepath.Join(planningPath, "overview.md"),
		filepath.Join(planningPath, "plan.yaml"),
	}
	switch phase {
	case PhaseImplement:
		required = append(required, filepath.Join(planningPath, "execution.md"))
	case PhaseReview:
		required = append(required,
			filepath.Join(planningPath, "execution.md"),
			filepath.Join(planningPath, "review.md"),
		)
	}
	return required
}

// CheckBoundary enforces the mechanical worktree contract for a phase.
func CheckBoundary(ctx context.Context, phase Phase, worktreePath string, requiredArtifacts []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	missing := missingArtifacts(requiredArtifacts)
	dirty, err := dirtyPaths(ctx, worktreePath)
	if err != nil {
		return err
	}
	if len(missing) == 0 && len(dirty) == 0 {
		return nil
	}
	return &BoundaryError{
		Phase:            phase,
		MissingArtifacts: missing,
		DirtyPaths:       dirty,
	}
}

func missingArtifacts(requiredArtifacts []string) []string {
	var missing []string
	for _, artifact := range requiredArtifacts {
		artifact = filepath.Clean(strings.TrimSpace(artifact))
		if artifact == "." || artifact == "" {
			continue
		}
		if _, err := os.Stat(artifact); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, artifact)
			}
		}
	}
	return missing
}

func dirtyPaths(ctx context.Context, worktreePath string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain=v1", "--untracked-files=all")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git status: %w", err)
		}
		return nil, fmt.Errorf("git status: %w: %s", err, msg)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	dirty := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path := porcelainPath(line)
		if path == "" || strings.HasPrefix(path, ".steiner/") || path == ".steiner" {
			continue
		}
		dirty = append(dirty, path)
	}
	return dirty, nil
}

func porcelainPath(line string) string {
	if len(line) <= 3 {
		return ""
	}
	payload := strings.TrimSpace(line[3:])
	if payload == "" {
		return ""
	}
	if idx := strings.Index(payload, " -> "); idx >= 0 {
		return strings.TrimSpace(payload[idx+4:])
	}
	return payload
}
