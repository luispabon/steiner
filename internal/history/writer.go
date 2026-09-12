// Package history persists local prompt history.
package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxEntries is the number of most recent prompts retained on disk.
const maxEntries = 50

// fileLocker abstracts platform-specific file locking.
type fileLocker interface {
	lock(fd uintptr) error
	unlock(fd uintptr) error
}

// Writer persists prompt history to a local file shared by concurrent
// steiner processes.
type Writer struct {
	mu     sync.Mutex // serialises calls within this process
	path   string
	locker fileLocker
}

// NewWriter opens or creates a history file at the given path.
func NewWriter(path string) (*Writer, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open history file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close history file: %w", err)
	}
	return &Writer{path: path, locker: newFileLocker()}, nil
}

// lockPath returns the path of the stable sibling file used for locking.
// flock binds to the inode at open time, and the data file's inode changes
// on every rename (see trim), so the lock must live on a stable sibling file
// rather than the data file itself.
func (w *Writer) lockPath() string {
	return w.path + ".lock"
}

// withLock serialises fn against this process (via mu) and against other
// processes (via a cross-process flock on the stable sibling lock file).
func (w *Writer) withLock(fn func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := os.OpenFile(w.lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open history lock file: %w", err)
	}

	if err := w.locker.lock(f.Fd()); err != nil {
		_ = f.Close() //nolint:errcheck // best-effort close on lock failure
		return fmt.Errorf("lock history: %w", err)
	}

	fnErr := fn()

	var unlockErr error
	if err := w.locker.unlock(f.Fd()); err != nil {
		unlockErr = fmt.Errorf("unlock history: %w", err)
	}
	var closeErr error
	if err := f.Close(); err != nil {
		closeErr = fmt.Errorf("close history lock file: %w", err)
	}

	return errors.Join(fnErr, unlockErr, closeErr)
}

// Record appends a prompt to the history file and trims the file to the
// recent limit.
func (w *Writer) Record(prompt string) error {
	if prompt == "" {
		return nil
	}
	escaped := strings.ReplaceAll(prompt, "\t", "\\t")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	line := time.Now().Format(time.RFC3339) + "\t" + escaped + "\n"

	return w.withLock(func() error {
		f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return fmt.Errorf("open history file: %w", err)
		}
		if _, err := f.WriteString(line); err != nil {
			_ = f.Close() //nolint:errcheck // best-effort close on write failure
			return fmt.Errorf("append history entry: %w", err)
		}
		if err := f.Sync(); err != nil {
			_ = f.Close() //nolint:errcheck // best-effort close on sync failure
			return fmt.Errorf("sync history file: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close history file: %w", err)
		}
		return w.trim()
	})
}

// trim keeps only the most recent maxEntries lines of the history file. It
// must be called with the lock held.
func (w *Writer) trim() error {
	data, err := os.ReadFile(w.path)
	if err != nil {
		return fmt.Errorf("read history file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxEntries {
		return nil
	}
	lines = lines[len(lines)-maxEntries:]

	dir := filepath.Dir(w.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(w.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp history file: %w", err)
	}
	tmpName := tmp.Name()

	for _, line := range lines {
		if _, err := tmp.WriteString(line + "\n"); err != nil {
			_ = tmp.Close()    //nolint:errcheck // best-effort close on write failure
			os.Remove(tmpName) //nolint:errcheck
			return fmt.Errorf("write temp history file: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()    //nolint:errcheck // best-effort close on sync failure
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("sync temp history file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("close temp history file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("chmod temp history file: %w", err)
	}
	if err := os.Rename(tmpName, w.path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("rename temp history file: %w", err)
	}
	return nil
}

// Path returns the configured history file path.
func (w *Writer) Path() string {
	return w.path
}

// Load reads the stored prompts from the history file.
func (w *Writer) Load() ([]string, error) {
	var prompts []string
	err := w.withLock(func() error {
		data, err := os.ReadFile(w.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("read history file: %w", err)
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			prompt := parts[1]
			prompt = strings.ReplaceAll(prompt, "\\t", "\t")
			prompt = strings.ReplaceAll(prompt, "\\n", "\n")
			prompts = append(prompts, prompt)
		}
		if len(prompts) > maxEntries {
			prompts = prompts[len(prompts)-maxEntries:]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return prompts, nil
}
