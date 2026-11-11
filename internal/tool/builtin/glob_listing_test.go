package builtin

import (
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// globListingFixture builds a listing fixture: real files and directories, an
// excluded directory, and symlinks to a file, a directory, an outside file, a
// missing file, and an excluded target. A link inside the excluded directory
// proves a filtered directory is never entered.
func globListingFixture(t *testing.T) Env {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"real.go", "dir/inner.go", "excluded/excluded.go"} {
		mustWriteTree(t, root, rel, "x\n")
	}
	mustSymlink(t, "../real.go", filepath.Join(root, "excluded", "link-out.go"))
	outside := t.TempDir()
	mustWriteTree(t, outside, "outside.go", "x\n")

	links := map[string]string{
		"dlink":    "dir",
		"flink":    "real.go",
		"outside":  filepath.Join(outside, "outside.go"),
		"dangling": "missing.go",
		"excllink": "excluded/excluded.go",
	}
	for name, target := range links {
		mustSymlink(t, target, filepath.Join(root, name))
	}

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	excluder := tool.NewWorkspaceExcluder(root, []string{"excluded"}, nil)
	return Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}
}

// TestGlobListsNestedSymlinksByName proves a listing returns every nested
// symlink as a plain name without following it: directory, outside, dangling,
// and excluded-target links are listed, a filtered directory is never entered,
// and neither "*/" nor "dlink/*" can see a directory link's contents.
func TestGlobListsNestedSymlinksByName(t *testing.T) {
	env := globListingFixture(t)
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "one level lists every link",
			pattern: "*",
			want:    []string{"dangling", "dlink", "dir/", "excllink", "flink", "outside", "real.go"},
		},
		{
			name:    "trailing slash lists real directories only",
			pattern: "*/",
			want:    []string{"dir/"},
		},
		{
			name:    "directory link has no children",
			pattern: "dlink/*",
		},
		{
			name:    "recursive listing skips filtered directories",
			pattern: "**",
			want:    []string{"dangling", "dlink", "dir/", "dir/inner.go", "excllink", "flink", "outside", "real.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSameStringSet(t, "glob rows", globPatternRows(t, env, ".", tt.pattern), tt.want)
		})
	}
}

// TestGlobHidesSymlinksToBlockedTargets proves a blocked target hides the link
// that points at it while an allowed link and its target are both listed.
func TestGlobHidesSymlinksToBlockedTargets(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "real.go", "x\n")
	mustWriteTree(t, root, "blocked/blocked.go", "x\n")
	mustSymlink(t, "real.go", filepath.Join(root, "flink"))
	mustSymlink(t, "blocked/blocked.go", filepath.Join(root, "blocklink"))

	policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"blocked"}})
	excluder := tool.NewWorkspaceExcluder(root, nil, nil)
	env := Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder}

	assertSameStringSet(t, "glob rows", globPatternRows(t, env, ".", "*"), []string{"flink", "real.go"})
}
