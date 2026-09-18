package oneshot

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	steinerDirName      = ".steiner"
	oneshotStateDirName = "oneshot"
	worktreeDirName     = "worktrees"
	plansDirName        = "plans"
)

// maxSlugBytes caps slug length at a filesystem-safe size.
const maxSlugBytes = 48

// RunIdentity binds a oneshot run id to a normalized feature slug.
type RunIdentity struct {
	ID   string
	Slug string
}

// NewRunIdentity generates a new run identity from the task description.
func NewRunIdentity(task string) (RunIdentity, error) {
	id, err := generateRunID()
	if err != nil {
		return RunIdentity{}, err
	}
	return RunIdentity{
		ID:   id,
		Slug: SlugFromTask(task),
	}, nil
}

// SlugFromTask normalizes a task title into a filesystem-safe slug.
func SlugFromTask(task string) string {
	task = strings.ToLower(strings.TrimSpace(task))
	if task == "" {
		return "run"
	}

	var b strings.Builder
	b.Grow(len(task))
	lastDash := false
	for _, r := range task {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "run"
	}
	if len(slug) > maxSlugBytes {
		slug = strings.Trim(truncateAtRuneBoundary(slug, maxSlugBytes), "-")
	}
	if slug == "" {
		return "run"
	}
	return slug
}

// truncateAtRuneBoundary returns the longest prefix of s no longer than maxBytes
// bytes without splitting a multi-byte UTF-8 rune.
func truncateAtRuneBoundary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// BranchName returns the provisioned git branch for the run.
func (r RunIdentity) BranchName() string {
	return "oneshot/" + r.Slug + "-" + r.ID
}

// WorktreePath returns the worktree checkout path under the project root.
func (r RunIdentity) WorktreePath(projectRoot string) string {
	return filepath.Join(projectRoot, steinerDirName, worktreeDirName, "oneshot-"+r.ID)
}

// PlanningPath returns the planning-artifact directory inside the run worktree.
func (r RunIdentity) PlanningPath(worktreePath string) string {
	return filepath.Join(worktreePath, steinerDirName, plansDirName, "oneshot-"+r.ID)
}

// StateDir returns the durable run-state directory under the project root.
func (r RunIdentity) StateDir(projectRoot string) string {
	return filepath.Join(projectRoot, steinerDirName, oneshotStateDirName, r.ID)
}

// ManifestPath returns the durable manifest location for the run.
func (r RunIdentity) ManifestPath(projectRoot string) string {
	return filepath.Join(r.StateDir(projectRoot), "run.json")
}

// LockPath returns the lock file location for the run.
func (r RunIdentity) LockPath(projectRoot string) string {
	return filepath.Join(r.StateDir(projectRoot), "run.lock")
}

func generateRunID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return fmt.Sprintf("%x", buf[:]), nil
}

func ensureSteinerProjectDir(workDir string) error {
	steinerDir := filepath.Join(workDir, steinerDirName)
	if err := os.MkdirAll(steinerDir, 0o755); err != nil {
		return fmt.Errorf("create .steiner directory: %w", err)
	}

	gitignorePath := filepath.Join(steinerDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat .steiner/.gitignore: %w", err)
		}
		if err := os.WriteFile(gitignorePath, []byte("*\n"), 0o644); err != nil {
			return fmt.Errorf("create .steiner/.gitignore: %w", err)
		}
	}

	return nil
}
