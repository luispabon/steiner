package tool

import (
	"slices"
	"testing"
)

type workspaceExcludeCase struct {
	path     string
	walkRoot string
	isDir    bool
	want     bool
}

func runWorkspaceExcludeCases(t *testing.T, ex WorkspaceExcluder, cases []workspaceExcludeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			walkRoot := tc.walkRoot
			if walkRoot == "" {
				walkRoot = ex.workspaceRoot
			}
			got := ex.ExcludeEntry(walkRoot, tc.path, tc.isDir)
			if got != tc.want {
				t.Fatalf("ExcludeEntry(%q, %q, isDir=%v) = %v, want %v", walkRoot, tc.path, tc.isDir, got, tc.want)
			}
		})
	}
}

func TestWorkspaceExcluder_ExactPaths(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", []string{"secrets", "/ws/private", "./logs/"}, nil, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/secrets", isDir: true, want: true},
		{path: "/ws/secrets/key.txt", want: true},
		{path: "/ws/secretary/notes.txt", want: false},
		{path: "/ws/private/data", want: true},
		{path: "/ws/logs/access.log", want: true},
		{path: "/ws/src/main.go", want: false},
		{path: "/other/secrets/key.txt", want: false},
	})
}

func TestWorkspaceExcluder_AbsoluteExclusionsOutsideWorkspace(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", []string{"/var/tmp", "/ws/cache"}, nil, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/var/tmp/build.log", want: true},
		{path: "/var/tmpx/build.log", want: false},
		{path: "/ws/cache/x", want: true},
		{path: "/elsewhere/x", want: false},
	})
}

func TestWorkspaceExcluder_SlashPatterns(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"generated/**", "**/fixtures", "docs/private"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/generated/out.o", want: true},
		{path: "/ws/a/generated/out.o", want: false},
		{path: "/ws/fixtures/data.json", want: true},
		{path: "/ws/pkg/fixtures/data.json", want: true},
		{path: "/ws/docs/private/deep/x.md", want: true},
		{path: "/ws/docs/public/x.md", want: false},
		{path: "/ws/src/main.go", want: false},
	})
}

func TestWorkspaceExcluder_SlashlessPatterns(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"*.bak", "dump*"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/data.bak", want: true},
		{path: "/ws/dump", isDir: true, want: true},
		{path: "/ws/dump/core", want: true},
		{path: "/ws/nested/dump/file", want: true},
		{path: "/ws/backup.bak/notes.txt", want: true},
		{path: "/ws/src/main.go", want: false},
	})
}

func TestWorkspaceExcluder_BuiltinDirectories(t *testing.T) {
	ex := NewWorkspaceExcluder("/ws", nil, nil)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/.git", isDir: true, want: true},
		{path: "/ws/.git", want: false},
		{path: "/ws/.git/config", want: true},
		{path: "/ws/node_modules", isDir: true, want: true},
		{path: "/ws/build", isDir: true, want: true},
		{path: "/ws/build", want: false},
		{path: "/ws/.steiner", isDir: true, want: true},
		{path: "/ws/src/main.go", want: false},
	})
}

func TestWorkspaceExcluder_ExplicitRootUnderExcludedAncestor(t *testing.T) {
	ex := NewWorkspaceExcluder("/ws", nil, nil)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/.steiner/worktrees/wt/main.go", walkRoot: "/ws/.steiner/worktrees/wt", want: false},
		{path: "/ws/.steiner/worktrees/wt/build/out.o", walkRoot: "/ws/.steiner/worktrees/wt", want: true},
		{path: "/ws/build/out.txt", walkRoot: "/ws/build", want: false},
		{path: "/ws/build/vendor/lib", walkRoot: "/ws/build", want: true},
	})
}

func TestWorkspaceExcluder_ForceIncludes(t *testing.T) {
	ex := NewWorkspaceExcluderWithIncludes("/ws", []string{"secrets"}, []string{"*.log"}, []string{".steiner", "logs"})
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/.steiner/config.yaml", want: false},
		{path: "/ws/.steiner", isDir: true, want: false},
		{path: "/ws/logs/app.log", want: false},
		{path: "/ws/src/app.log", want: true},
		{path: "/ws/secrets/key.txt", want: true},
		{path: "/ws/secrets", isDir: true, want: true},
	})
}

func TestWorkspaceExcluder_ForceIncludeNeverOverridesAbsoluteExclusion(t *testing.T) {
	ex := NewWorkspaceExcluderWithIncludes("/ws", []string{"/var/tmp"}, nil, []string{"tmp"})
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/var/tmp/x", walkRoot: "/var", want: true},
	})
}

func TestWorkspaceExcluder_OutsideWorkspace(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"generated/**", "*.bak", "dump*"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/elsewhere/x.bak", walkRoot: "/elsewhere", want: true},
		{path: "/elsewhere/sub/dump/file", walkRoot: "/elsewhere", want: true},
		{path: "/elsewhere/generated/out.o", walkRoot: "/elsewhere", want: false},
		{path: "/elsewhere/node_modules", walkRoot: "/elsewhere", isDir: true, want: true},
		{path: "/elsewhere/src/main.go", walkRoot: "/elsewhere", want: false},
	})
}

func TestWorkspaceExcluder_ZeroValue(t *testing.T) {
	var ex WorkspaceExcluder
	if ex.ExcludeEntry("/ws", "/ws/.git/config", false) {
		t.Fatal("zero value must not exclude anything")
	}
}

func TestWorkspaceExcluder_UnixBackslashFilenames(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", []string{"a\\b"}, []string{"name.txt"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/dir\\name.txt", want: false},
		{path: "/ws/a\\b/inside.txt", want: true},
		{path: "/ws/a\\c/inside.txt", want: false},
	})
}

func TestWorkspaceExcluder_WindowsSemantics(t *testing.T) {
	ex := newWorkspaceExcluder(
		`C:\Work\Proj`,
		[]string{"Secrets", `C:\Work\Proj\Private`},
		[]string{`Build\**`, "**/Fixtures"},
		[]string{".STEINER"},
		true,
	)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: `C:\Work\Proj\secrets\Key.txt`, want: true},
		{path: `C:/WORK/Proj/PRIVATE/x`, want: true},
		{path: `C:\Work\Proj\Build\out.o`, want: true},
		{path: `C:\Work\Proj\pkg\FIXTURES\data.json`, want: true},
		{path: `C:\Work\Proj\.GIT`, isDir: true, want: true},
		{path: `C:\Work\Proj\.STEINER\config.yaml`, want: false},
		{path: `C:\Work\Proj\src\Main.go`, want: false},
		{path: `C:\Other\secrets\Key.txt`, walkRoot: `C:\Other`, want: false},
	})
}

func TestWorkspaceExcluder_InvalidPatternIgnored(t *testing.T) {
	ex := NewWorkspaceExcluder("/ws", nil, []string{"[abc", "*.bak"})
	if !ex.ExcludeEntry("/ws", "/ws/x.bak", false) {
		t.Fatal("valid pattern should still apply when another pattern is invalid")
	}
}

func TestExpandZeroDirectorySegments(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{pattern: "**/fixtures", want: []string{"**/fixtures", "fixtures"}},
		{pattern: "a/**/b", want: []string{"a/**/b", "a/b"}},
		{pattern: "a/**/**/b", want: []string{"a/**/**/b", "a/**/b", "a/b"}},
		{pattern: "[**/]x", want: []string{"[**/]x"}},
		{pattern: `\*\*/x`, want: []string{`\*\*/x`}},
		{pattern: "plain", want: []string{"plain"}},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			got := expandZeroDirectorySegments(tc.pattern)
			if len(got) != len(tc.want) {
				t.Fatalf("expandZeroDirectorySegments(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
			for _, want := range tc.want {
				if !slices.Contains(got, want) {
					t.Fatalf("expandZeroDirectorySegments(%q) = %q, missing %q", tc.pattern, got, want)
				}
			}
		})
	}
}
