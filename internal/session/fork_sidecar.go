package session

import (
	"context"
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
// pointing at the parent, which still exists. Call it before saving the fork,
// because the saved record must name the copied sidecars.
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

	// copyForkFile and copyForkRecallFiles inspect only their leaf, so the
	// parent session directory is checked here before anything is read through
	// it.
	if err := checkForkDir(filepath.Dir(parentHistory)); err != nil {
		return forked, fmt.Errorf("fork sidecars: %w", err)
	}

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
		copyHistory:     historyCopied,
		parentHistory:   parentHistory,
		forkHistory:     forkHistory,
		parentRecallDir: parentRecallDir,
		forkRecallDir:   forkRecallDir,
		copiedRecall:    copiedRecall,
	}), nil
}

// ErrForkIndexed reports that DiscardForkSidecars refused an id the store index
// holds. A fork that Save indexed before a later step failed is saved and
// loadable, so callers keep it instead of treating the fork as lost.
var ErrForkIndexed = errors.New("fork session is indexed")

// DiscardForkSidecars removes the session file and session directory of an
// unindexed fork whose Save failed, so a fork whose sidecars were copied before
// the save leaves nothing behind. It returns an error wrapping ErrForkIndexed
// for an id in the index, which belongs to a saved session, and validates the id
// like the rest of the store.
func (s *Store) DiscardForkSidecars(forkID string) error {
	dir, err := safeSessionDir(s.dir, forkID)
	if err != nil {
		return fmt.Errorf("discard fork sidecars: validate session id: %w", err)
	}
	sessionPath, err := safeSessionPath(s.dir, forkID)
	if err != nil {
		return fmt.Errorf("discard fork sidecars: validate session id: %w", err)
	}

	lock, err := s.acquireIndex(context.Background())
	if err != nil {
		return fmt.Errorf("discard fork sidecars: %w", err)
	}
	defer s.releaseIndex(lock)

	for _, entry := range s.index {
		if entry.ID == forkID {
			return fmt.Errorf("discard fork sidecars: session %q: %w; delete it instead", forkID, ErrForkIndexed)
		}
	}
	// Remove the session file first: a leftover directory holds only sidecars,
	// while a leftover session file would still load.
	if err := os.Remove(sessionPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove fork session file: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("discard fork sidecars: remove %s: %w", dir, err)
	}
	return nil
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
			if err := ensureForkRecallDir(toDir); err != nil {
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

// ensureForkRecallDir prepares the fork session directory and then the recall
// directory inside it. ensureForkDir checks only the directory it is given, so
// checking both keeps a symlinked session directory from being followed when no
// history is copied.
func ensureForkRecallDir(toDir string) error {
	if err := ensureForkDir(filepath.Dir(toDir)); err != nil {
		return err
	}
	return ensureForkDir(toDir)
}

// ensureForkDir creates dir with dirMode when missing, rejects a symlinked or
// non-directory path, and sets an existing directory to dirMode exactly.
func ensureForkDir(dir string) error {
	// Check before MkdirAll so a symlinked fork directory is never taken as the
	// place to create files, and again after it so a swap in between is caught.
	if err := checkForkDir(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := checkForkDir(dir); err != nil {
		return err
	}
	if err := chmod(dir, dirMode); err != nil {
		return fmt.Errorf("tighten %s permissions: %w", dir, err)
	}
	return nil
}

// checkForkDir rejects a symlinked or non-directory path without following it.
// A missing path is not an error, so a parent with no sidecars and a fork
// directory that does not exist yet both pass.
func checkForkDir(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; replace it with a real directory", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}
