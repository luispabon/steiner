package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/fsatomic"
)

// ForkSidecars copies parentID's history archive and recall files into forked's
// session directory and rewrites the fork's inherited references to the copies:
// the history path in inherited summary notes, and the read path inside elision
// markers in the fork's latest generation.
//
// Absent parent files copy nothing. Only a complete copy rewrites anything, so a
// fork whose sidecars could not all be copied is returned unchanged and keeps
// pointing at the parent, which still exists. Call it before Save, because
// saving the fork can evict the parent.
func (s *Store) ForkSidecars(parentID string, forked Session) (Session, error) {
	if parentID == forked.ID {
		return forked, fmt.Errorf("fork sidecars: parent and fork share session id %q", parentID)
	}
	parentHistory, err := s.HistoryPath(parentID)
	if err != nil {
		return forked, fmt.Errorf("fork sidecars: %w", err)
	}
	forkHistory, err := s.HistoryPath(forked.ID)
	if err != nil {
		return forked, fmt.Errorf("fork sidecars: %w", err)
	}
	parentRecallDir, err := s.RecallDir(parentID)
	if err != nil {
		return forked, fmt.Errorf("fork sidecars: %w", err)
	}
	forkRecallDir, err := s.RecallDir(forked.ID)
	if err != nil {
		return forked, fmt.Errorf("fork sidecars: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	historyCopied, err := copyForkFile(parentHistory, forkHistory)
	if err != nil {
		return forked, fmt.Errorf("fork sidecars: copy history: %w", err)
	}
	copiedRecall, err := copyForkRecallFiles(parentRecallDir, forkRecallDir)
	if err != nil {
		return forked, fmt.Errorf("fork sidecars: copy recall: %w", err)
	}
	if !historyCopied && len(copiedRecall) == 0 {
		return forked, nil
	}
	return rewriteForkReferences(forked, forkReferenceRewrite{
		parentHistory:   parentHistory,
		forkHistory:     forkHistory,
		parentRecallDir: parentRecallDir,
		forkRecallDir:   forkRecallDir,
		copiedRecall:    copiedRecall,
	}), nil
}

// copyForkFile copies from to to atomically and reports whether from exists. A
// missing source copies nothing. A symlinked or non-regular source fails the
// copy instead of following it.
func copyForkFile(from, to string) (bool, error) {
	info, err := os.Lstat(from)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("inspect %s: %w", from, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return false, fmt.Errorf("%s is a symlink; replace it with a regular file", from)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", from)
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", from, err)
	}
	if err := ensureForkDir(filepath.Dir(to)); err != nil {
		return false, err
	}
	if err := fsatomic.WriteFile(to, data, fileMode); err != nil {
		return false, err
	}
	return true, nil
}

// copyForkRecallFiles copies every recall file in fromDir to toDir and returns
// the copied file names, which are the recall names without their extension. A
// missing source directory copies nothing. A symlinked directory or file, or a
// non-regular file, fails the copy instead of being followed.
func copyForkRecallFiles(fromDir, toDir string) (map[string]bool, error) {
	info, err := os.Lstat(fromDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("inspect %s: %w", fromDir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; replace it with a real directory", fromDir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", fromDir)
	}
	entries, err := os.ReadDir(fromDir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", fromDir, err)
	}

	copied := make(map[string]bool, len(entries))
	dirReady := false
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), recallFileExt)
		if !ok {
			continue
		}
		from := filepath.Join(fromDir, entry.Name())
		info, err := os.Lstat(from)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", from, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink; replace it with a regular file", from)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", from)
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", from, err)
		}
		if !dirReady {
			if err := ensureForkDir(toDir); err != nil {
				return nil, err
			}
			dirReady = true
		}
		if err := fsatomic.WriteFile(filepath.Join(toDir, entry.Name()), data, fileMode); err != nil {
			return nil, err
		}
		copied[name] = true
	}
	return copied, nil
}

// ensureForkDir creates dir with dirMode when missing, rejects a symlinked or
// non-directory existing path, and sets an existing directory to dirMode exactly.
func ensureForkDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; replace it with a real directory", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory; move it aside so steiner can create it", dir)
	}
	if err := chmod(dir, dirMode); err != nil {
		return fmt.Errorf("tighten %s permissions: %w", dir, err)
	}
	return nil
}
