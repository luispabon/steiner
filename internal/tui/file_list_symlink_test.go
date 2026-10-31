package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeSymlinkTestTree creates each slash-separated rel path as a file under
// root, creating parent directories as needed.
func writeSymlinkTestTree(t *testing.T, root string, rels []string) {
	t.Helper()
	for _, rel := range rels {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// symlinkTestRoot creates a real directory holding rels and returns a symlink
// that points at it.
func symlinkTestRoot(t *testing.T, rels []string) string {
	t.Helper()
	realRoot := t.TempDir()
	writeSymlinkTestTree(t, realRoot, rels)
	linkRoot := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("symlink root: %v", err)
	}
	return linkRoot
}

// TestWalkDirEntries_SymlinkRoot proves a symlinked root is walked and that
// returned entries stay lexical, relative to the root the caller passed.
func TestWalkDirEntries_SymlinkRoot(t *testing.T) {
	linkRoot := symlinkTestRoot(t, []string{"README.md", "src/main.go"})

	entries, err := walkDirEntries(linkRoot)
	if err != nil {
		t.Fatalf("walkDirEntries: %v", err)
	}

	want := []string{"README.md", "src/", "src/main.go"}
	slices.Sort(entries)
	slices.Sort(want)
	if !slices.Equal(entries, want) {
		t.Fatalf("entries = %v, want %v", entries, want)
	}
	for _, entry := range entries {
		if filepath.IsAbs(entry) {
			t.Errorf("entry %q is absolute, want paths relative to %q", entry, linkRoot)
		}
	}
}

// TestWalkDirEntries_SymlinkRootDoesNotFollowNestedDirSymlinks proves nested
// directory symlinks are listed as entries but not descended into.
func TestWalkDirEntries_SymlinkRootDoesNotFollowNestedDirSymlinks(t *testing.T) {
	linkRoot := symlinkTestRoot(t, []string{"src/main.go"})

	outside := t.TempDir()
	writeSymlinkTestTree(t, outside, []string{"secret.txt"})

	resolvedLinkRoot, err := filepath.EvalSymlinks(linkRoot)
	if err != nil {
		t.Fatalf("resolve link root: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(resolvedLinkRoot, "linked")); err != nil {
		t.Fatalf("symlink nested dir: %v", err)
	}

	entries, err := walkDirEntries(linkRoot)
	if err != nil {
		t.Fatalf("walkDirEntries: %v", err)
	}

	if !slices.Contains(entries, "linked") {
		t.Errorf("entries missing nested symlink %q: %v", "linked", entries)
	}
	for _, unwanted := range []string{"linked/", filepath.Join("linked", "secret.txt")} {
		if slices.Contains(entries, unwanted) {
			t.Errorf("entries traversed nested symlink, contain %q: %v", unwanted, entries)
		}
	}
}

// TestWalkDirEntries_SymlinkRootKeepsPickerExclusions proves the picker
// force-include and exact-exclude rules still apply when the root is a
// symlink.
func TestWalkDirEntries_SymlinkRootKeepsPickerExclusions(t *testing.T) {
	linkRoot := symlinkTestRoot(t, []string{
		".steiner/config.yaml",
		".steiner/tmp/scratch.txt",
		".steiner/worktrees/wt/src/main.go",
		"src/.steiner/keep.yaml",
		"src/main.go",
		"build/out.txt",
		"node_modules/pkg/index.js",
	})

	entries, err := walkDirEntries(linkRoot)
	if err != nil {
		t.Fatalf("walkDirEntries: %v", err)
	}

	for _, want := range []string{
		".steiner/",
		".steiner/config.yaml",
		"src/",
		"src/.steiner/",
		"src/.steiner/keep.yaml",
		"src/main.go",
	} {
		if !slices.Contains(entries, want) {
			t.Errorf("entries missing %q: %v", want, entries)
		}
	}
	for _, unwanted := range []string{
		".steiner/tmp/",
		".steiner/tmp/scratch.txt",
		".steiner/worktrees/",
		"build/",
		"node_modules/",
	} {
		if slices.Contains(entries, unwanted) {
			t.Errorf("entries contain excluded %q: %v", unwanted, entries)
		}
	}
}

// TestFilePickerOverlay_SymlinkRootListsEntries proves the picker surfaces
// candidates when opened on a symlinked root.
func TestFilePickerOverlay_SymlinkRootListsEntries(t *testing.T) {
	linkRoot := symlinkTestRoot(t, []string{"src/main.go"})

	f := newFilePickerOverlay(testStyles("#ff0000")).Open(linkRoot)
	if !slices.Contains(f.allEntries, "src/main.go") {
		t.Errorf("allEntries missing %q: %v", "src/main.go", f.allEntries)
	}
	if !slices.Contains(f.candidates, "src/") {
		t.Errorf("candidates missing %q: %v", "src/", f.candidates)
	}
}
