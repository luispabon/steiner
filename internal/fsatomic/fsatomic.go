// Package fsatomic replaces files atomically: data is written to a uniquely
// named temp file in the target directory, fsynced, and renamed over the
// target. The temp file is fsynced before the rename, but the parent directory
// is not fsynced afterwards, so after a power loss the rename may not be durable.
//
// A crash between creating the temp file and the rename leaves an orphan named
// .tmp-<base>-<random>. The first WriteFile to each target path in a process
// removes orphans for that path that are older than one hour. Only regular
// files matching that pattern are removed; symlinks, directories, and other
// names are left alone.
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
// removes it. Live writes finish far sooner, so a temp file still being written
// is never old enough to remove.
const staleTempAge = time.Hour

// sweptPaths records the absolute target paths already swept in this process.
var sweptPaths sync.Map

// tempPrefix returns the name prefix of temp files created for base. The
// CreateTemp pattern and the sweep both derive from it.
func tempPrefix(base string) string {
	return ".tmp-" + base + "-"
}

// sweepStaleTempsOnce sweeps the directory of path the first time path is seen
// in this process. If the absolute path cannot be resolved, the sweep is skipped.
func sweepStaleTempsOnce(path string, now time.Time) {
	abs, err := filepath.Abs(path)
	if err != nil {
		// Best effort: skipping the sweep does not affect the write itself.
		return
	}
	if _, swept := sweptPaths.LoadOrStore(abs, struct{}{}); swept {
		return
	}
	sweepStaleTemps(abs, now)
}

// sweepStaleTemps removes orphan temp files for path that are older than
// staleTempAge at now. Errors are ignored: the sweep is cleanup only, and a
// failure must not fail or delay the write that triggered it.
func sweepStaleTemps(path string, now time.Time) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing or unreadable directory: nothing to sweep. The write reports its own error.
		return
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
