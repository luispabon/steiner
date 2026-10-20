// Package fsatomic replaces files atomically: data is written to a uniquely
// named temp file in the target directory, fsynced, and renamed over the
// target. The temp file is fsynced before the rename, but the parent directory
// is not fsynced afterwards, so after a power loss the rename may not be durable.
//
// A crash between creating the temp file and the rename leaves an orphan named
// .tmp-<base>-<random>. The first WriteFile to each target path in a process
// removes orphans for that path that are older than one hour. Only regular
// files matching that pattern are removed; symlinks, directories, and other
// names are left alone. If the directory cannot be read, the sweep is retried
// on the next call. The one-hour cutoff is a heuristic, not a lock: a write
// stalled for longer than an hour (for example, a laptop suspended mid-write)
// can lose its temp file to a sweep. Its chmod or rename then fails, WriteFile
// returns that error, and the target file is left unchanged.
package fsatomic

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// staleTempAge is the minimum age of an orphan temp file before the sweep
// removes it. It is a heuristic: a write stalled for longer than this can lose
// its temp file (see the package comment).
const staleTempAge = time.Hour

// sweptPaths records the absolute target paths already swept in this process.
var sweptPaths sync.Map

// tempPrefix returns the name prefix of temp files created for base. The
// CreateTemp pattern and the sweep both derive from it.
func tempPrefix(base string) string {
	return ".tmp-" + base + "-"
}

// sweepStaleTempsOnce sweeps the directory of path the first time path is seen
// in this process. A path is recorded only after its directory is read, so a
// missing or unreadable directory is swept on a later call. If the absolute
// path cannot be resolved, the sweep is skipped.
func sweepStaleTempsOnce(path string, now time.Time) {
	abs, err := filepath.Abs(path)
	if err != nil {
		// Best effort: skipping the sweep does not affect the write itself.
		return
	}
	if _, swept := sweptPaths.Load(abs); swept {
		return
	}
	// Two concurrent first calls may both sweep; a sweep is idempotent. A failed
	// read is not recorded, so it is retried. The write reports its own error.
	if err := sweepStaleTemps(abs, now); err != nil {
		return
	}
	sweptPaths.Store(abs, struct{}{})
}

// sweepStaleTemps removes orphan temp files for path that are older than
// staleTempAge at now. It returns an error only if the directory cannot be read.
// Failures on individual entries are ignored: the sweep is cleanup only, and a
// failure must not fail or delay the write that triggered it.
func sweepStaleTemps(path string, now time.Time) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read temp dir: %w", err)
	}
	cutoff := now.Add(-staleTempAge)
	for _, entry := range entries {
		if !isTempName(entry.Name(), base) {
			continue
		}
		name := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(name)
		if err != nil {
			// The file may have been removed concurrently; skip it.
			continue
		}
		// Lstat describes a symlink itself, so symlinks fail IsRegular and are kept.
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		// A failed remove leaves the orphan for a later sweep.
		_ = os.Remove(name)
	}
	return nil
}

// isTempName reports whether name is tempPrefix(base) followed by the random
// part os.CreateTemp substitutes for "*". The random part contains no "-", so a
// temp file for a longer base such as "out-2" does not match base "out".
func isTempName(name, base string) bool {
	rest, ok := strings.CutPrefix(name, tempPrefix(base))
	return ok && rest != "" && !strings.Contains(rest, "-")
}

// WriteFile atomically replaces path with data and sets its permission bits to
// mode exactly, independent of the process umask. The temp file lives in the
// same directory as path so the final rename stays on one filesystem. Readers
// see either the old or the new content, never a partial write.
//
// WriteFile does not create parent directories; callers create them with the
// mode they need. On any failure the temp file is removed and path is left
// unchanged.
func WriteFile(path string, data []byte, mode os.FileMode) (err error) {
	sweepStaleTempsOnce(path, time.Now())
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPrefix(filepath.Base(path))+"*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			// Best-effort cleanup; the error that failed the write is returned.
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
