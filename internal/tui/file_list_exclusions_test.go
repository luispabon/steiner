package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestWalkDirEntries_FilePickerForceInclude proves the picker keeps its
// force-include semantics on the workspace excluder: .steiner stays visible at
// any depth while its heavy subdirectories and built-in directory names stay
// hidden.
func TestWalkDirEntries_FilePickerForceInclude(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		".steiner/config.yaml",
		".steiner/tmp/scratch.txt",
		".steiner/worktrees/wt/src/main.go",
		"src/main.go",
		"src/.steiner/keep.yaml",
		"build/out.txt",
		"node_modules/pkg/index.js",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	entries, err := walkDirEntries(root)
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
