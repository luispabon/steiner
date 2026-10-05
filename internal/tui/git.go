package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

var getWorkingDir = os.Getwd

type gitRefreshDoneMsg struct{}

func gitRefreshCmd(gs *gitState) tea.Cmd {
	return func() tea.Msg {
		gs.Refresh(context.Background())
		return gitRefreshDoneMsg{}
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
	err      error
}

func newGitState(startDir string) *gitState {
	state := &gitState{}
	if strings.TrimSpace(startDir) == "" {
		cwd, err := getWorkingDir()
		if err != nil {
			state.recordError(fmt.Errorf("resolve working directory: %w", err))
			return state
		}
		startDir = cwd
	}
	state.startDir = startDir
	return state
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

	snapshot := detectGitSnapshot(ctx, s.startDir, s.recordError)

	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()

	return snapshot
}

func (s *gitState) recordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *gitState) takeError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.err
	s.err = nil
	return err
}

func detectGitSnapshot(ctx context.Context, startDir string, logError func(error)) gitSnapshot {
	repoRoot, ok := resolveGitRepo(startDir)
	if !ok {
		return gitSnapshot{}
	}

	status, err := readGitStatus(ctx, repoRoot)
	if err != nil {
		if logError != nil {
			logError(err)
		}
		return gitSnapshot{repoRoot: repoRoot, ready: true}
	}

	files := status.files
	if status.oid != gitInitialOID && len(files) > 0 {
		counts, err := readGitNumstat(ctx, repoRoot)
		if err != nil && logError != nil {
			logError(err)
		}
		applyGitCounts(files, counts)
	}
	sortGitModifiedFiles(files)

	return gitSnapshot{
		repoRoot:      repoRoot,
		branch:        status.branch(),
		dirty:         len(files) > 0,
		ahead:         status.ahead,
		modifiedFiles: files,
		ready:         true,
	}
}

func resolveGitRepo(startDir string) (repoRoot string, ok bool) {
	absStart := startDir
	if abs, err := filepath.Abs(startDir); err == nil {
		absStart = abs
	}

	for dir := absStart; ; dir = filepath.Dir(dir) {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		switch {
		case err == nil && info.IsDir():
			return dir, true
		case err == nil:
			if _, err := readGitDirFile(gitPath, dir); err != nil {
				return "", false
			}
			return dir, true
		case errors.Is(err, os.ErrNotExist):
			// Keep walking up until we reach the filesystem root.
		default:
			return "", false
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
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
