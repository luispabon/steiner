package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type gitSnapshot struct {
	repoRoot      string
	branch        string
	dirty         bool
	modifiedFiles []gitModifiedFile
	ready         bool
}

type gitModifiedFile struct {
	Path    string
	Added   int
	Deleted int
}

type gitState struct {
	mu       sync.RWMutex
	startDir string
	snapshot gitSnapshot
}

func newGitState(startDir string) *gitState {
	if strings.TrimSpace(startDir) == "" {
		startDir, _ = os.Getwd()
	}
	return &gitState{startDir: startDir}
}

func (s *gitState) Snapshot() gitSnapshot {
	if s == nil {
		return gitSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *gitState) Branch() string {
	return s.Snapshot().branch
}

func (s *gitState) Dirty() bool {
	return s.Snapshot().dirty
}

func (s *gitState) RepoRoot() string {
	return s.Snapshot().repoRoot
}

func (s *gitState) Ready() bool {
	return s.Snapshot().ready
}

func (s *gitState) Refresh(ctx context.Context) gitSnapshot {
	if s == nil {
		return gitSnapshot{}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	snapshot := detectGitSnapshot(ctx, s.startDir)

	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()

	return snapshot
}

func detectGitSnapshot(ctx context.Context, startDir string) gitSnapshot {
	repoRoot, gitDir, ok := resolveGitRepo(startDir)
	if !ok {
		return gitSnapshot{}
	}

	branch := readGitBranch(gitDir)
	dirty := readGitDirty(ctx, repoRoot)
	modifiedFiles := readGitModifiedFiles(ctx, repoRoot)

	return gitSnapshot{
		repoRoot:      repoRoot,
		branch:        branch,
		dirty:         dirty,
		modifiedFiles: modifiedFiles,
		ready:         true,
	}
}

func resolveGitRepo(startDir string) (repoRoot, gitDir string, ok bool) {
	absStart := startDir
	if abs, err := filepath.Abs(startDir); err == nil {
		absStart = abs
	}

	for dir := absStart; ; dir = filepath.Dir(dir) {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		switch {
		case err == nil && info.IsDir():
			return dir, gitPath, true
		case err == nil:
			resolvedGitDir, err := readGitDirFile(gitPath, dir)
			if err != nil {
				return "", "", false
			}
			return dir, resolvedGitDir, true
		case errors.Is(err, os.ErrNotExist):
			// Keep walking up until we reach the filesystem root.
		default:
			return "", "", false
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
	}
}

func readGitDirFile(path, repoRoot string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", os.ErrNotExist
	}

	gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if gitDir == "" {
		return "", os.ErrNotExist
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	return filepath.Clean(gitDir), nil
}

func readGitBranch(gitDir string) string {
	headPath := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}

	head := strings.TrimSpace(string(data))
	if head == "" {
		return ""
	}

	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return ""
		}
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			branch = strings.TrimSpace(branch)
			if branch != "" {
				return branch
			}
		}
		return ref
	}

	fields := strings.Fields(head)
	if len(fields) == 0 {
		return ""
	}

	sha := fields[0]
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return "detached@" + sha
}

func readGitDirty(ctx context.Context, repoRoot string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

func readGitModifiedFiles(ctx context.Context, repoRoot string) []gitModifiedFile {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff", "--numstat", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	files := make([]gitModifiedFile, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		files = append(files, gitModifiedFile{
			Added:   parseGitNumstatCount(fields[0]),
			Deleted: parseGitNumstatCount(fields[1]),
			Path:    filepath.Clean(strings.TrimSpace(fields[2])),
		})
	}
	return files
}

func parseGitNumstatCount(value string) int {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return 0
	}
	count := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0
		}
		count = count*10 + int(ch-'0')
	}
	return count
}
