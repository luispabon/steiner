package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"
)

// homeCacheMaxAge is how long a sandbox cache file may go unwritten before it
// is removed. Go's build cache refreshes the mtime of an action file (-a) on a
// read at most once an hour, while output files (-d) keep their write time, so
// a hot compile output is still pruned 48 hours after it was produced.
const homeCacheMaxAge = 48 * time.Hour

// homeCacheTargets are the tool cache directories under
// <root>/.steiner/home/cache whose contents are disposable. Each tool
// regenerates a missing file on demand. Unknown directories, including
// steiner/lsp, are never touched; add a tool here only when its cache layout
// and miss behaviour are known.
var homeCacheTargets = []string{"go-build", "golangci-lint", "gh"}

// PruneHomeCache removes files from <root>/.steiner/home/cache that have not
// been written for homeCacheMaxAge. The sandbox binds that directory over the
// host cache, so sandboxed builds write their caches into the project.
// Only the go-build, golangci-lint, and gh caches are pruned, and only regular
// files inside a two-hex shard directory: bookkeeping files such as go-build's
// trim.txt and unknown tool state are preserved. Directories and symlinks are
// never removed or followed. Missing or unsafe cache paths are not an error
// and remove nothing. Best-effort: per-entry errors are joined and returned.
// removed counts files unlinked; freed is their total size in bytes.
func PruneHomeCache(root string) (removed int, freed int64, err error) {
	return pruneHomeCache(root, time.Now())
}

// pruneHomeCache implements PruneHomeCache with an injectable clock.
func pruneHomeCache(root string, now time.Time) (removed int, freed int64, err error) {
	cacheDir := privateCacheDir(filepath.Join(root, ".steiner", "home"))
	if safeDirectoryPath(root, cacheDir) != nil {
		return 0, 0, nil
	}
	info, err := os.Lstat(cacheDir)
	if err != nil {
		return 0, 0, nil
	}
	cacheRoot, err := os.OpenRoot(cacheDir)
	if err != nil {
		return 0, 0, fmt.Errorf("open sandbox cache: %w", err)
	}
	defer func() { _ = cacheRoot.Close() }() // read-only directory handle; nothing to flush
	opened, err := cacheRoot.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return 0, 0, nil
	}

	var errs []error
	for _, name := range homeCacheTargets {
		target, ok, err := openVerifiedDir(cacheRoot, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("open sandbox cache target %q: %w", name, err))
			continue
		}
		if !ok {
			continue
		}
		targetRemoved, targetFreed, pruneErr := pruneHomeCacheTarget(target, now)
		closeErr := target.Close()
		removed += targetRemoved
		freed += targetFreed
		if pruneErr != nil {
			errs = append(errs, pruneErr)
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("close sandbox cache target %q: %w", name, closeErr))
		}
	}
	return removed, freed, errors.Join(errs...)
}

// openVerifiedDir opens name under parent when it is a real directory, and
// verifies the opened handle is the file Lstat reported. It returns ok=false
// without error when the component is missing, is a symlink or non-directory,
// or was replaced between the check and the open.
func openVerifiedDir(parent *os.Root, name string) (*os.Root, bool, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect sandbox cache directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, nil
	}
	target, err := parent.OpenRoot(name)
	if err != nil {
		return nil, false, fmt.Errorf("open sandbox cache directory: %w", err)
	}
	opened, err := target.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = target.Close() // read-only directory handle; nothing to flush
		return nil, false, nil
	}
	return target, true, nil
}

// pruneHomeCacheTarget removes stale shard files under one verified target
// directory. Errors are collected per entry so one bad file does not abort the
// sweep.
func pruneHomeCacheTarget(target *os.Root, now time.Time) (removed int, freed int64, err error) {
	var errs []error
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("walk sandbox cache: %w", err))
			return nil
		}
		if d.IsDir() || !isCacheShardFile(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect sandbox cache entry %q: %w", p, err))
			return nil
		}
		if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < homeCacheMaxAge {
			return nil
		}
		gone, size, err := removeStaleFile(target, filepath.FromSlash(p), now)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if gone {
			removed++
			freed += size
		}
		return nil
	}
	// The callback never returns an error, so WalkDir cannot fail after the
	// root is opened; per-entry errors are collected in errs instead.
	_ = fs.WalkDir(target.FS(), ".", walk)
	return removed, freed, errors.Join(errs...)
}

// isCacheShardFile reports whether p is a file directly inside a two-hex shard
// directory, the layout shared by the Go build cache, golangci-lint's cache,
// and gh's HTTP cache. Bookkeeping files at a cache root are not shard files.
func isCacheShardFile(p string) bool {
	shard := path.Dir(p)
	if shard == "." {
		return false
	}
	base := path.Base(shard)
	return len(base) == 2 && isLowerHex(base[0]) && isLowerHex(base[1])
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// removeStaleFile removes name relative to target when it is still a regular
// file that has not been written for homeCacheMaxAge. It returns whether the
// file was unlinked and its size. A file that disappeared or was rewritten
// during the sweep is left alone.
func removeStaleFile(target *os.Root, name string, now time.Time) (bool, int64, error) {
	info, err := target.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("inspect sandbox cache entry %q: %w", name, err)
	}
	if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < homeCacheMaxAge {
		return false, 0, nil
	}
	if err := target.Remove(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("remove sandbox cache file %q: %w", name, err)
	}
	return true, info.Size(), nil
}
