package sandbox

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCacheFile creates path with size bytes of content and sets its modtime
// so age-based pruning can be exercised without waiting.
func writeCacheFile(t *testing.T, path string, size int, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// requirePresent fails when path is missing; Lstat is used so symlinks count.
func requirePresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

// requireMissing fails unless path is absent.
func requireMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected %s to be removed, got error %v", path, err)
	}
}

// cacheShardName returns a Go-cache-style entry name with the given id fill.
func cacheShardName(id string, suffix string) string {
	return strings.Repeat(id, 64) + suffix
}

// cacheFixture is one file to place under the sandbox cache before a prune.
// rel is slash-separated relative to the cache directory.
type cacheFixture struct {
	rel   string
	size  int
	fresh bool
}

func TestPruneHomeCache(t *testing.T) {
	tests := []struct {
		name        string
		files       []cacheFixture
		wantRemoved int
		wantFreed   int64
		wantGone    []string
		wantKept    []string
	}{
		{
			name: "removes stale shard file and keeps fresh one",
			files: []cacheFixture{
				{rel: "go-build/ab/" + cacheShardName("a", "-a"), size: 128},
				{rel: "go-build/ab/" + cacheShardName("b", "-d"), size: 64, fresh: true},
			},
			wantRemoved: 1,
			wantFreed:   128,
			wantGone:    []string{"go-build/ab/" + cacheShardName("a", "-a")},
			wantKept:    []string{"go-build/ab/" + cacheShardName("b", "-d")},
		},
		{
			name: "preserves bookkeeping and non-shard files",
			files: []cacheFixture{
				{rel: "go-build/trim.txt", size: 16},
				{rel: "go-build/README", size: 16},
				{rel: "go-build/zz/old-" + cacheShardName("c", "-a"), size: 32},
				{rel: "go-build/top-" + cacheShardName("d", "-a"), size: 48},
			},
			wantRemoved: 0,
			wantFreed:   0,
			wantKept: []string{
				"go-build/trim.txt",
				"go-build/README",
				"go-build/zz/old-" + cacheShardName("c", "-a"),
				"go-build/top-" + cacheShardName("d", "-a"),
			},
		},
		{
			name: "preserves unlisted tool state",
			files: []cacheFixture{
				{rel: "steiner/lsp/deadbeef/state", size: 8},
				{rel: "marker", size: 8},
			},
			wantRemoved: 0,
			wantFreed:   0,
			wantKept:    []string{"steiner/lsp/deadbeef/state", "marker"},
		},
		{
			name: "prunes every allowlisted target",
			files: []cacheFixture{
				{rel: "go-build/4b/" + cacheShardName("1", "-a"), size: 32},
				{rel: "golangci-lint/be/" + cacheShardName("2", "-d"), size: 48},
				{rel: "gh/37/c7/" + cacheShardName("3", ""), size: 64},
			},
			wantRemoved: 3,
			wantFreed:   144,
			wantGone: []string{
				"go-build/4b/" + cacheShardName("1", "-a"),
				"golangci-lint/be/" + cacheShardName("2", "-d"),
				"gh/37/c7/" + cacheShardName("3", ""),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cacheDir := filepath.Join(root, ".steiner", "home", "cache")
			now := time.Now()
			stale := now.Add(-72 * time.Hour)
			fresh := now.Add(-time.Hour)
			for _, f := range tt.files {
				modTime := stale
				if f.fresh {
					modTime = fresh
				}
				writeCacheFile(t, filepath.Join(cacheDir, filepath.FromSlash(f.rel)), f.size, modTime)
			}

			removed, freed, err := pruneHomeCache(root, now)
			if err != nil {
				t.Fatalf("pruneHomeCache() error = %v", err)
			}
			if removed != tt.wantRemoved {
				t.Errorf("removed = %d, want %d", removed, tt.wantRemoved)
			}
			if freed != tt.wantFreed {
				t.Errorf("freed = %d, want %d", freed, tt.wantFreed)
			}
			for _, rel := range tt.wantGone {
				requireMissing(t, filepath.Join(cacheDir, filepath.FromSlash(rel)))
			}
			for _, rel := range tt.wantKept {
				requirePresent(t, filepath.Join(cacheDir, filepath.FromSlash(rel)))
			}
		})
	}
}

func TestPruneHomeCache_SymlinkedCacheDir(t *testing.T) {
	root := t.TempDir()
	homeDir := filepath.Join(root, ".steiner", "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	outside := t.TempDir()
	now := time.Now()
	outsideFile := filepath.Join(outside, "go-build", "ab", cacheShardName("a", "-a"))
	writeCacheFile(t, outsideFile, 64, now.Add(-72*time.Hour))
	cacheDir := filepath.Join(homeDir, "cache")
	if err := os.Symlink(outside, cacheDir); err != nil {
		t.Fatalf("symlink cache: %v", err)
	}

	removed, freed, err := pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	requirePresent(t, cacheDir)
	requirePresent(t, outsideFile)
}

func TestPruneHomeCache_SymlinkedTargetDir(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".steiner", "home", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	outside := t.TempDir()
	now := time.Now()
	outsideFile := filepath.Join(outside, "ab", cacheShardName("a", "-a"))
	writeCacheFile(t, outsideFile, 64, now.Add(-72*time.Hour))
	target := filepath.Join(cacheDir, "go-build")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatalf("symlink target: %v", err)
	}

	removed, freed, err := pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	requirePresent(t, target)
	requirePresent(t, outsideFile)
}

func TestPruneHomeCache_SymlinkedFileEntry(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".steiner", "home", "cache")
	now := time.Now()
	outside := t.TempDir()
	payload := filepath.Join(outside, "payload")
	writeCacheFile(t, payload, 64, now.Add(-72*time.Hour))
	link := filepath.Join(cacheDir, "go-build", "ab", cacheShardName("a", "-a"))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	if err := os.Symlink(payload, link); err != nil {
		t.Fatalf("symlink entry: %v", err)
	}

	removed, freed, err := pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	requirePresent(t, link)
	requirePresent(t, payload)
}

func TestPruneHomeCache_SymlinkedShardDir(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".steiner", "home", "cache")
	if err := os.MkdirAll(filepath.Join(cacheDir, "go-build"), 0o755); err != nil {
		t.Fatalf("mkdir go-build: %v", err)
	}
	outside := t.TempDir()
	now := time.Now()
	outsideFile := filepath.Join(outside, cacheShardName("a", "-a"))
	writeCacheFile(t, outsideFile, 64, now.Add(-72*time.Hour))
	shard := filepath.Join(cacheDir, "go-build", "ab")
	if err := os.Symlink(outside, shard); err != nil {
		t.Fatalf("symlink shard: %v", err)
	}

	removed, freed, err := pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	requirePresent(t, shard)
	requirePresent(t, outsideFile)
}

func TestPruneHomeCache_DirectoryNamedLikeShardFile(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".steiner", "home", "cache")
	now := time.Now()
	dir := filepath.Join(cacheDir, "go-build", "ab", cacheShardName("a", "-a"))
	inner := filepath.Join(dir, "inner")
	writeCacheFile(t, inner, 32, now.Add(-72*time.Hour))

	removed, freed, err := pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	requirePresent(t, dir)
	requirePresent(t, inner)
}

func TestIsCacheShardFile(t *testing.T) {
	hash := cacheShardName("a", "-a")
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "root level file", path: "trim.txt", want: false},
		{name: "two hex parent", path: "ab/" + hash, want: true},
		{name: "uppercase two hex parent", path: "AB/" + hash, want: false},
		{name: "one character parent", path: "a/" + hash, want: false},
		{name: "three character parent", path: "abc/" + hash, want: false},
		{name: "nested non-hex parent", path: "steiner/lsp/" + hash, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCacheShardFile(tc.path); got != tc.want {
				t.Errorf("isCacheShardFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestRemoveStaleFile(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".steiner", "home", "cache")
	now := time.Now()
	stale := now.Add(-72 * time.Hour)
	name := filepath.Join("ab", cacheShardName("a", "-a"))
	entry := filepath.Join(cacheDir, "go-build", name)
	writeCacheFile(t, entry, 96, stale)

	target, err := os.OpenRoot(filepath.Join(cacheDir, "go-build"))
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer func() { _ = target.Close() }() // read-only directory handle; nothing to flush

	// A refresh between the walk and the removal keeps the file.
	if err := os.Chtimes(entry, now, now); err != nil {
		t.Fatalf("chtimes fresh: %v", err)
	}
	gone, size, err := removeStaleFile(target, name, now)
	if err != nil {
		t.Fatalf("removeStaleFile(refreshed) error = %v", err)
	}
	if gone || size != 0 {
		t.Errorf("removeStaleFile(refreshed) = %v, %d, want false, 0", gone, size)
	}
	requirePresent(t, entry)

	// The same file is removed once it is stale again.
	if err := os.Chtimes(entry, stale, stale); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	gone, size, err = removeStaleFile(target, name, now)
	if err != nil {
		t.Fatalf("removeStaleFile(stale) error = %v", err)
	}
	if !gone || size != 96 {
		t.Errorf("removeStaleFile(stale) = %v, %d, want true, 96", gone, size)
	}
	requireMissing(t, entry)

	// A file that disappeared during the sweep is not an error.
	gone, size, err = removeStaleFile(target, name, now)
	if err != nil {
		t.Fatalf("removeStaleFile(missing) error = %v", err)
	}
	if gone || size != 0 {
		t.Errorf("removeStaleFile(missing) = %v, %d, want false, 0", gone, size)
	}
}

func TestPruneHomeCache_MissingCacheDir(t *testing.T) {
	root := t.TempDir()
	removed, freed, err := pruneHomeCache(root, time.Now())
	if err != nil {
		t.Fatalf("pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	if _, err := os.Stat(filepath.Join(root, ".steiner")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected pruning to create nothing, stat error = %v", err)
	}
}

func TestPruneHomeCache_Idempotent(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, ".steiner", "home", "cache")
	now := time.Now()
	staleFile := filepath.Join(cacheDir, "go-build", "ab", cacheShardName("a", "-a"))
	freshFile := filepath.Join(cacheDir, "go-build", "ab", cacheShardName("b", "-d"))
	writeCacheFile(t, staleFile, 128, now.Add(-72*time.Hour))
	writeCacheFile(t, freshFile, 64, now.Add(-time.Hour))

	removed, freed, err := pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("first pruneHomeCache() error = %v", err)
	}
	if removed != 1 || freed != 128 {
		t.Errorf("first removed, freed = %d, %d, want 1, 128", removed, freed)
	}
	requireMissing(t, staleFile)

	removed, freed, err = pruneHomeCache(root, now)
	if err != nil {
		t.Fatalf("second pruneHomeCache() error = %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Errorf("second removed, freed = %d, %d, want 0, 0", removed, freed)
	}
	requirePresent(t, freshFile)
}
