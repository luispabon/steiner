package tool

import (
	"strings"
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

func TestWorkspaceExcluder_NormalizesPatterns(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"./generated/**", "build/", "docs/private/", "/docs/**", "/./legacy/**", "docs/./deep/**"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/generated/out.o", want: true},
		{path: "/ws/docs/private/x.md", want: true},
		{path: "/ws/docs/api/x.md", want: true},
		{path: "/ws/docs/deep/x.md", want: true},
		{path: "/ws/legacy/code.go", want: true},
		{path: "/ws/other/legacy/code.go", want: false},
		{path: "/ws/other/docs/deep/x.md", want: false},
		{path: "/ws/other/docs/api/x.md", want: false},
		{path: "/ws/other/docs/private/x.md", want: false},
		{path: "/ws/build", isDir: true, want: true},
		{path: "/ws/a/build", isDir: true, want: true},
		{path: "/ws/buildish/x", want: false},
	})
}

func TestWorkspaceExcluder_NegatedClassStaysWithinSegment(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"**/a[!x]b", "a[!x]b/c"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/a/b", want: false},
		{path: "/ws/a/b/c", want: false},
		{path: "/ws/ayb", want: true},
		{path: "/ws/ayb/c", want: true},
		{path: "/ws/pkg/ayb", want: true},
	})
}

func TestNormalizeWorkspacePattern(t *testing.T) {
	tests := []struct {
		pattern string
		windows bool
		want    string
	}{
		{pattern: "docs/**", want: "docs/**"},
		{pattern: "./docs/**", want: "docs/**"},
		{pattern: "/./docs/**", want: "docs/**"},
		{pattern: "/.//./docs/**/", want: "docs/**"},
		{pattern: "././docs/", want: "docs"},
		{pattern: "a/./b", want: "a/./b"},
		{pattern: "build/", want: "build"},
		{pattern: `\docs\**`, windows: true, want: "docs/**"},
		{pattern: "/", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := normalizeWorkspacePattern(tc.pattern, tc.windows); got != tc.want {
				t.Fatalf("normalizeWorkspacePattern(%q, %v) = %q, want %q", tc.pattern, tc.windows, got, tc.want)
			}
		})
	}
}

func TestWorkspaceExcluder_ComponentPatternAppliesToExplicitRoot(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"secrets"}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/secrets/deep/x", walkRoot: "/ws/secrets/deep", want: true},
		{path: "/ws/secrets/deep/x", walkRoot: "/ws", want: true},
	})
}

func TestWorkspaceExcluder_EscapedSlashPattern(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{`a\/b`}, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws/a/b", want: true},
		{path: "/ws/x/a/b", want: false},
	})
}

func TestWorkspaceExcluder_FilesystemRootExclusion(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", []string{"/"}, nil, nil, false)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/", isDir: true, want: true},
		{path: "/ws/src/main.go", want: true},
		{path: "/elsewhere/x", walkRoot: "/elsewhere", want: true},
	})
}

func TestWorkspaceExcluder_WindowsVolumeRootExclusion(t *testing.T) {
	ex := newWorkspaceExcluder(`C:\`, []string{`C:\`}, nil, nil, true)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: `C:\`, isDir: true, want: true},
		{path: `C:\a\b`, want: true},
		{path: `D:\x`, walkRoot: `D:\`, want: false},
	})
}

func TestWorkspaceExcluder_DotExcludePath(t *testing.T) {
	ex := NewWorkspaceExcluder("/ws", []string{"."}, nil)
	runWorkspaceExcludeCases(t, ex, []workspaceExcludeCase{
		{path: "/ws", isDir: true, want: true},
		{path: "/ws/src/main.go", want: true},
		{path: "/other/x", walkRoot: "/other", want: false},
	})
}

func TestWorkspacePattern_MatchesPrefix(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{pattern: "**/fixtures", path: "fixtures", want: true},
		{pattern: "**/fixtures", path: "a/fixtures", want: true},
		{pattern: "**/fixtures", path: "a/b", want: false},
		{pattern: "**/target", path: "target/later", want: true},
		{pattern: "a/**/b", path: "a/b", want: true},
		{pattern: "a/**/**/b", path: "a/b", want: true},
		{pattern: "**/a/**/b", path: "a/b", want: true},
		{pattern: "build/**", path: "build", want: true},
		{pattern: "a**/b", path: "ab", want: false},
		{pattern: "a**/b", path: "a/b", want: true},
		{pattern: "a**/b", path: "axx/y/b", want: true},
		{pattern: "a/./b", path: "a/b", want: true},
		{pattern: `a/\./b`, path: "a/b", want: false},
		{pattern: "{.,a}/b", path: "a/b", want: true},
		{pattern: `\*\*/x`, path: "**/x", want: true},
		{pattern: "[**/]x", path: "**/x", want: false},
		{pattern: "{a/b,c}/**", path: "a/b/x", want: true},
		{pattern: "{a/b,c}/**", path: "c/x", want: true},
		{pattern: "**/a[!x]b", path: "a/b", want: false},
		{pattern: "**/a[!x]b", path: "ayb", want: true},
		{pattern: "**/a[!x]b", path: "pkg/ayb", want: true},
		{pattern: "a[!x]b/c", path: "a/b/c", want: false},
		{pattern: "a[!x]b/c", path: "ayb/c", want: true},
		{pattern: "{a[!x]b,c}/d", path: "a/b/d", want: false},
		{pattern: "{a[!x]b,c}/d", path: "ayb/d", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.pattern+" "+tc.path, func(t *testing.T) {
			compiled, ok := compileWorkspaceSlashPattern(tc.pattern)
			if !ok {
				t.Fatalf("compileWorkspaceSlashPattern(%q) failed", tc.pattern)
			}
			if got := compiled.matchesPrefix(tc.path); got != tc.want {
				t.Fatalf("%q matchesPrefix %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestWorkspacePattern_ManyGlobstarSegments(t *testing.T) {
	pattern := strings.Repeat("**/", 20) + "target"
	compiled, ok := compileWorkspaceSlashPattern(pattern)
	if !ok {
		t.Fatalf("compileWorkspaceSlashPattern(%q) failed", pattern)
	}
	for _, path := range []string{"target", "a/target", strings.Repeat("a/", 20) + "target"} {
		if !compiled.matchesPrefix(path) {
			t.Fatalf("%q should match %q", pattern, path)
		}
	}
	if compiled.matchesPrefix("later/other") {
		t.Fatalf("%q should not match %q", pattern, "later/other")
	}
}

func TestWorkspacePattern_DeepPathWorkBounded(t *testing.T) {
	compiled, ok := compileWorkspaceSlashPattern("**/target/**")
	if !ok {
		t.Fatal("compileWorkspaceSlashPattern failed")
	}
	deep := strings.Repeat("segment/", 300) + "target/file.go"
	var matched bool
	allocs := testing.AllocsPerRun(20, func() {
		matched = compiled.matchesPrefix(deep)
	})
	if !matched {
		t.Fatalf("deep path %q should match", deep)
	}
	if allocs > 10 {
		t.Fatalf("deep path matching allocated %.1f objects per run, want at most 10", allocs)
	}
}

func TestWorkspacePattern_DeepPathWithEmbeddedSuperStarBounded(t *testing.T) {
	compiled, ok := compileWorkspaceSlashPattern("**/d**/file")
	if !ok {
		t.Fatal("compileWorkspaceSlashPattern failed")
	}
	deep := strings.Repeat("segment/", 300) + "data/" + strings.Repeat("segment/", 40) + "file"
	if !compiled.matchesPrefix(deep) {
		t.Fatalf("deep path %q should match", deep)
	}
	if compiled.matchesPrefix(strings.Repeat("segment/", 300) + "file") {
		t.Fatal("path without a d-prefixed component should not match")
	}
	var matched bool
	allocs := testing.AllocsPerRun(10, func() {
		matched = compiled.matchesPrefix(deep)
	})
	if !matched {
		t.Fatalf("deep path %q should match", deep)
	}
	if allocs > 10 {
		t.Fatalf("deep path with embedded ** allocated %.1f objects per run, want at most 10", allocs)
	}
}

func TestWorkspacePattern_AllDPrefixedPathBounded(t *testing.T) {
	compiled, ok := compileWorkspaceSlashPattern("**/d**/file")
	if !ok {
		t.Fatal("compileWorkspaceSlashPattern failed")
	}
	deep := strings.Repeat("d/", 160) + "file"
	if !compiled.matchesPrefix(deep) {
		t.Fatalf("all d-prefixed path %q should match", deep)
	}
	var matched bool
	allocs := testing.AllocsPerRun(10, func() {
		matched = compiled.matchesPrefix(deep)
	})
	if !matched {
		t.Fatalf("all d-prefixed path %q should match", deep)
	}
	if allocs > 10 {
		t.Fatalf("all d-prefixed path allocated %.1f objects per run, want at most 10", allocs)
	}
}
