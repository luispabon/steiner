package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// gitErrorLogger is a package-level hook for logging git errors.
// The TUI model sets this to emit error events.
var gitErrorLogger func(error)

func logGitError(err error) {
	if gitErrorLogger != nil {
		gitErrorLogger(err)
	}
}

type gitSnapshot struct {
	repoRoot      string
	branch        string
	dirty         bool
	ahead         int
	modifiedFiles []gitModifiedFile
	ready         bool
}

type gitModifiedFile struct {
	Status  string
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
	ahead := readGitAhead(ctx, repoRoot)
	modifiedFiles := readGitModifiedFiles(ctx, repoRoot)

	return gitSnapshot{
		repoRoot:      repoRoot,
		branch:        branch,
		dirty:         dirty,
		ahead:         ahead,
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
	out, err := readGitStatusPorcelain(ctx, repoRoot)
	if err != nil {
		return false
	}
	return len(out) > 0
}

func readGitModifiedFiles(ctx context.Context, repoRoot string) []gitModifiedFile {
	statusLines, err := readGitStatusPorcelain(ctx, repoRoot)
	if err != nil {
		return nil
	}

	numstatCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff", "--numstat", "HEAD")
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		logGitError(fmt.Errorf("git diff --numstat: %w", err))
	}

	type counts struct{ added, deleted int }
	countMap := make(map[string]counts)
	for _, line := range strings.Split(strings.TrimSpace(string(numstatOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(fields[2]))
		countMap[path] = counts{
			added:   parseGitNumstatCount(fields[0]),
			deleted: parseGitNumstatCount(fields[1]),
		}
	}

	var files []gitModifiedFile
	seen := make(map[string]bool)
	for _, line := range statusLines {
		status, path, ok := parseGitStatusLine(line)
		if !ok || seen[path] {
			continue
		}
		seen[path] = true

		c := countMap[path]
		files = append(files, gitModifiedFile{
			Status:  status,
			Path:    path,
			Added:   c.added,
			Deleted: c.deleted,
		})
	}
	return files
}

func readGitStatusPorcelain(ctx context.Context, repoRoot string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		logGitError(fmt.Errorf("git status --porcelain: %w", err))
		return nil, err
	}

	text := strings.TrimRight(string(out), "\r\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func parseGitStatusLine(line string) (string, string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 3 {
		return "", "", false
	}

	code := line[:2]
	path := strings.TrimSpace(line[3:])
	if path == "" {
		return "", "", false
	}
	if strings.Contains(path, " -> ") {
		parts := strings.SplitN(path, " -> ", 2)
		path = strings.TrimSpace(parts[1])
	}
	path = filepath.Clean(path)

	switch {
	case code == "??":
		return "U", path, true
	case strings.Contains(code, "U"):
		return "U", path, true
	case strings.Contains(code, "D"):
		return "D", path, true
	case strings.Contains(code, "A"), strings.Contains(code, "R"), strings.Contains(code, "C"):
		return "A", path, true
	default:
		return "M", path, true
	}
}

func readGitAhead(ctx context.Context, repoRoot string) int {
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-list", "--count", "@{u}..HEAD").Output()
	if err != nil {
		logGitError(fmt.Errorf("git rev-list --count: %w", err))
		return 0
	}
	return parseGitNumstatCount(strings.TrimSpace(string(out)))
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
