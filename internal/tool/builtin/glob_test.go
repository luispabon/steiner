package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// globWalkFixture writes the tree shared by the entry-selection tests: the
// directory a holding b, plus a.txt and x.go at the root.
func globWalkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWriteTree(t, root, "a/b", "b")
	mustWriteTree(t, root, "a.txt", "a")
	mustWriteTree(t, root, "x.go", "x")
	return root
}

// globWalkParamsWith builds walk params rooted at root from the given policy and
// excluder, with one page large enough for the fixtures.
func globWalkParamsWith(root, pattern string, policy *tool.PathPolicy, excluder tool.WorkspaceExcluder) globWalkParams {
	return globWalkParams{
		root:        root,
		lexicalRoot: root,
		pattern:     pattern,
		excluder:    excluder,
		policy:      policy,
		limit:       defaultGlobLimit,
	}
}

// globWalkParamsFor builds walk params rooted at root with no configured
// exclusions and the given page window.
func globWalkParamsFor(t *testing.T, root, pattern string, limit, offset int) globWalkParams {
	t.Helper()
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	params := globWalkParamsWith(root, pattern, &policy, tool.NewWorkspaceExcluder(root, nil, nil))
	params.limit = limit
	params.offset = offset
	return params
}

func runGlobWalk(t *testing.T, params globWalkParams) Result {
	t.Helper()
	result, err := globWalk(context.Background(), params)
	if err != nil {
		t.Fatalf("globWalk(pattern=%q): %v", params.pattern, err)
	}
	return result
}

func runGlobTool(t *testing.T, toolDef tool.ToolDef, input map[string]any) Result {
	t.Helper()
	resultI, err := toolDef.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("glob handler error: %v", err)
	}
	result, ok := resultI.(Result)
	if !ok {
		t.Fatalf("glob result type = %T, want Result", resultI)
	}
	return result
}

func TestGlobWalkEntries(t *testing.T) {
	root := globWalkFixture(t)
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{name: "missing pattern lists one level", pattern: "", want: []string{"a/", "a.txt", "x.go"}},
		{name: "single star lists one level", pattern: "*", want: []string{"a/", "a.txt", "x.go"}},
		{name: "trailing slash lists directories only", pattern: "*/", want: []string{"a/"}},
		{name: "doublestar lists every entry in walk order", pattern: "**", want: []string{"a/", "a/b", "a.txt", "x.go"}},
		{name: "doublestar go pattern matches files", pattern: "**/*.go", want: []string{"x.go"}},
		{name: "subdirectory pattern", pattern: "a/*", want: []string{"a/b"}},
		{name: "bare name matches a directory", pattern: "a", want: []string{"a/"}},
		{name: "trailing slash pattern matches a directory", pattern: "a/", want: []string{"a/"}},
		{name: "nested basename pattern", pattern: "**/b", want: []string{"a/b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGlobWalk(t, globWalkParamsFor(t, root, tt.pattern, defaultGlobLimit, 0))
			if got := outputRows(result.Output, ""); !slices.Equal(got, tt.want) {
				t.Fatalf("entries = %v, want %v", got, tt.want)
			}
			if result.NextOffset != 0 {
				t.Errorf("NextOffset = %d, want 0", result.NextOffset)
			}
		})
	}
}

// TestGlobWalkDirectoryPatternSlash proves a directory is returned when a
// pattern matches its own path, or when a pattern ending in "/" matches that
// path plus the slash. A pattern such as src/* must not return src/ itself.
func TestGlobWalkDirectoryPatternSlash(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "src/main.go", "main")
	mustWriteTree(t, root, "src/pkg/lib.go", "lib")
	mustWriteTree(t, root, "src/pkg/deep/x.go", "x")
	mustWriteTree(t, root, "top.txt", "top")

	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{name: "star does not emit its parent directory", pattern: "src/*", want: []string{"src/main.go", "src/pkg/"}},
		{name: "trailing slash excludes child files", pattern: "src/*/", want: []string{"src/pkg/"}},
		{name: "top level slash lists directories only", pattern: "*/", want: []string{"src/"}},
		{name: "top level star lists files and directories", pattern: "*", want: []string{"src/", "top.txt"}},
		{name: "doublestar slash lists directories at every level", pattern: "**/", want: []string{"src/", "src/pkg/", "src/pkg/deep/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGlobWalk(t, globWalkParamsFor(t, root, tt.pattern, defaultGlobLimit, 0))
			if got := outputRows(result.Output, ""); !slices.Equal(got, tt.want) {
				t.Fatalf("entries = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGlobWalkMatchesDirectoryWithFilePattern covers the accepted consequence
// that a file-style pattern can also match a directory of the same name.
func TestGlobWalkMatchesDirectoryWithFilePattern(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "x.go/y.txt", "y")
	mustWriteTree(t, root, "z.go", "z")

	result := runGlobWalk(t, globWalkParamsFor(t, root, "**/*.go", defaultGlobLimit, 0))
	want := []string{"x.go/", "z.go"}
	if got := outputRows(result.Output, ""); !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestGlobWalkPagesPastThousandMatches(t *testing.T) {
	root := t.TempDir()
	const total = 1200
	for i := range total {
		name := fmt.Sprintf("file%04d.txt", i)
		mustWriteTree(t, root, name, name)
	}

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantFirst string
		wantLast  string
		wantCount int
		wantNext  int
	}{
		{
			name:      "full first page",
			limit:     maxGlobLimit,
			offset:    0,
			wantFirst: "file0000.txt",
			wantLast:  "file0999.txt",
			wantCount: maxGlobLimit,
			wantNext:  1000,
		},
		{
			name:      "page past the old collection cap",
			limit:     100,
			offset:    1000,
			wantFirst: "file1000.txt",
			wantLast:  "file1099.txt",
			wantCount: 100,
			wantNext:  1100,
		},
		{
			name:      "last page has no next offset",
			limit:     200,
			offset:    1100,
			wantFirst: "file1100.txt",
			wantLast:  "file1199.txt",
			wantCount: 100,
			wantNext:  0,
		},
		{
			name:      "exact remaining matches have no next offset",
			limit:     100,
			offset:    1100,
			wantFirst: "file1100.txt",
			wantLast:  "file1199.txt",
			wantCount: 100,
			wantNext:  0,
		},
		{
			name:      "offset past the end",
			limit:     10,
			offset:    total + 5,
			wantCount: 0,
			wantNext:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGlobWalk(t, globWalkParamsFor(t, root, "*.txt", tt.limit, tt.offset))
			lines := outputRows(result.Output, "")
			if len(lines) != tt.wantCount {
				t.Fatalf("line count = %d, want %d", len(lines), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if lines[0] != tt.wantFirst || lines[len(lines)-1] != tt.wantLast {
				t.Fatalf("page = %q ... %q, want %q ... %q", lines[0], lines[len(lines)-1], tt.wantFirst, tt.wantLast)
			}
			if result.NextOffset != tt.wantNext {
				t.Errorf("NextOffset = %d, want %d", result.NextOffset, tt.wantNext)
			}
		})
	}
}

// countingContext counts Err calls so a test can observe which walk entries
// were visited. When liveCalls is positive it reports cancellation once more
// Err calls than that have happened.
type countingContext struct {
	context.Context
	liveCalls int
	errCalls  int
}

func (c *countingContext) Err() error {
	c.errCalls++
	if c.liveCalls > 0 && c.errCalls > c.liveCalls {
		return context.Canceled
	}
	return nil
}

func TestGlobWalkPrunesDeeperLevels(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "a/b/c.txt", "c")
	mustWriteTree(t, root, "src/lib/a.go", "package a\n")
	mustWriteTree(t, root, "top.txt", "top")

	tests := []struct {
		name      string
		pattern   string
		wantCalls int
		want      []string
	}{
		{
			name:      "fixed depth pattern stops below its deepest level",
			pattern:   "*.txt",
			wantCalls: 4,
			want:      []string{"top.txt"},
		},
		{
			name:      "brace pattern prunes to its deepest form",
			pattern:   "*.{go,txt}",
			wantCalls: 4,
			want:      []string{"top.txt"},
		},
		{
			name:      "doublestar pattern visits every level",
			pattern:   "**/c.txt",
			wantCalls: 8,
			want:      []string{"a/b/c.txt"},
		},
		{
			name:      "class pattern that spans a separator is never pruned",
			pattern:   "a[!x]b",
			wantCalls: 8,
			want:      []string{"a/b/"},
		},
		{
			name:      "class pattern in a later segment is never pruned",
			pattern:   "src[!x]lib/*.go",
			wantCalls: 8,
			want:      []string{"src/lib/a.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &countingContext{Context: context.Background()}
			result, err := globWalk(ctx, globWalkParamsFor(t, root, tt.pattern, defaultGlobLimit, 0))
			if err != nil {
				t.Fatalf("globWalk(pattern=%q): %v", tt.pattern, err)
			}
			if got := outputRows(result.Output, ""); !slices.Equal(got, tt.want) {
				t.Fatalf("entries = %v, want %v", got, tt.want)
			}
			if ctx.errCalls != tt.wantCalls {
				t.Fatalf("entries visited = %d, want %d", ctx.errCalls, tt.wantCalls)
			}
		})
	}
}

func TestGlobWalkCancellation(t *testing.T) {
	root := globWalkFixture(t)

	t.Run("cancelled context stops the walk", func(t *testing.T) {
		ctx := &countingContext{Context: context.Background(), liveCalls: 1}
		params := globWalkParamsFor(t, root, "**", defaultGlobLimit, 0)
		if _, err := globWalk(ctx, params); !errors.Is(err, context.Canceled) {
			t.Fatalf("globWalk error = %v, want context.Canceled", err)
		}
		if ctx.errCalls < 2 {
			t.Fatalf("ctx.Err calls = %d, want at least 2", ctx.errCalls)
		}
	})

	t.Run("handler reports cancellation", func(t *testing.T) {
		env := workspaceTestEnv(t, root, nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := NewGlobTool(env).Handler(ctx, map[string]any{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Handler error = %v, want context.Canceled", err)
		}
	})
}

func TestGlobWalkRootErrors(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file.txt")
	mustWriteTree(t, base, "file.txt", "x")

	t.Run("nonexistent root", func(t *testing.T) {
		missing := filepath.Join(base, "missing")
		if _, err := globWalk(context.Background(), globWalkParamsFor(t, missing, "*", defaultGlobLimit, 0)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("globWalk error = %v, want not exist error", err)
		}
	})

	t.Run("file root is rejected", func(t *testing.T) {
		_, err := globWalk(context.Background(), globWalkParamsFor(t, file, "*", defaultGlobLimit, 0))
		if err == nil || !strings.Contains(err.Error(), "is not a directory") {
			t.Fatalf("globWalk error = %v, want a not-a-directory error", err)
		}
	})

	t.Run("handler reports a file root", func(t *testing.T) {
		env := workspaceTestEnv(t, base, nil, nil)
		_, err := NewGlobTool(env).Handler(context.Background(), map[string]any{"path": "file.txt"})
		if err == nil || !strings.Contains(err.Error(), "glob: path") || !strings.Contains(err.Error(), "is not a directory") {
			t.Fatalf("Handler error = %v, want a glob not-a-directory error", err)
		}
	})

	t.Run("unreadable root", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("permission-based root failure test is unix-oriented")
		}
		blocked := t.TempDir()
		mustWriteTree(t, blocked, "visible.txt", "visible")
		if err := os.Chmod(blocked, 0o000); err != nil {
			t.Fatalf("chmod root: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(blocked, 0o755); err != nil {
				t.Errorf("restore root permissions: %v", err)
			}
		})
		if _, err := os.ReadDir(blocked); err == nil {
			t.Skip("filesystem does not deny directory traversal after chmod 000")
		}

		_, err := globWalk(context.Background(), globWalkParamsFor(t, blocked, "*", defaultGlobLimit, 0))
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("globWalk error = %v, want a permission error", err)
		}
	})
}

// TestGlobToolMalformedBraces pins the brace rule for the glob tool: an
// unescaped brace with no counterpart fails the call, a brace inside a character
// class or written with a backslash is literal, and a matched empty group
// expands away. gobwas must never compile an unmatched brace, because its
// matcher panics on a path shorter than the pattern text.
func TestGlobToolMalformedBraces(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "aa", "aa")
	mustWriteTree(t, root, "a{b", "a{b")
	mustWriteTree(t, root, "{x", "{x")
	toolDef := NewGlobTool(workspaceTestEnv(t, root, nil, nil))

	tests := []struct {
		name      string
		pattern   string
		want      string
		wantErr   bool
		unmatched bool
	}{
		{name: "empty group matches its literal prefix", pattern: "aa{}", want: "aa"},
		{name: "escaped open brace is literal", pattern: `a\{b`, want: "a{b"},
		{name: "brace in a class is literal", pattern: "[{]x", want: "{x"},
		{name: "trailing backslash matches nothing", pattern: `\`, want: ""},
		{name: "lone open brace", pattern: "{", wantErr: true, unmatched: true},
		{name: "lone close brace", pattern: "}", wantErr: true, unmatched: true},
		{name: "close brace between literals", pattern: "a}b", wantErr: true, unmatched: true},
		{name: "unterminated group", pattern: "{a,", wantErr: true, unmatched: true},
		{name: "open brace after literals", pattern: "aa{", wantErr: true, unmatched: true},
		{name: "open brace then literal", pattern: "a{b", wantErr: true, unmatched: true},
		{name: "closed inner group in an open outer", pattern: "{x{c,d}", wantErr: true, unmatched: true},
		{name: "literal before an unclosed outer group", pattern: "a{x{c,d}", wantErr: true, unmatched: true},
		{name: "closed inner group after an open outer", pattern: "{a{b,c}", wantErr: true, unmatched: true},
		{name: "lone open bracket is a compile error", pattern: "[", wantErr: true},
		{name: "open bracket after a prefix is a compile error", pattern: "a[b", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultI, err := toolDef.Handler(context.Background(), map[string]any{"pattern": tt.pattern})
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "glob: ") {
					t.Fatalf("Handler(%q) error = %v, want glob-prefixed error", tt.pattern, err)
				}
				if got := errors.Is(err, errGlobUnmatchedBrace); got != tt.unmatched {
					t.Errorf("Handler(%q) error = %v, errGlobUnmatchedBrace match = %v, want %v", tt.pattern, err, got, tt.unmatched)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result, ok := resultI.(Result)
			if !ok {
				t.Fatalf("result type = %T, want Result", resultI)
			}
			if result.Output != tt.want {
				t.Fatalf("Output = %q, want %q", result.Output, tt.want)
			}
		})
	}
}

// TestGlobToolDoublestarGroupDirectoryForms pins the trailing slash of a
// directory brace alternative: dropping the zero-directory form of "**/" must
// leave "b/", never a doubled separator.
func TestGlobToolDoublestarGroupDirectoryForms(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "a/b/keep.txt", "keep")
	mustWriteTree(t, root, "b/keep.txt", "keep")
	mustWriteTree(t, root, "x/b/keep.txt", "keep")
	toolDef := NewGlobTool(workspaceTestEnv(t, root, nil, nil))

	result := runGlobTool(t, toolDef, map[string]any{"pattern": "**/{a,b/}"})
	if result.Output != "a/\na/b/\nb/\nx/b/" {
		t.Errorf("Output = %q, want %q", result.Output, "a/\na/b/\nb/\nx/b/")
	}
}

// TestGlobWalkerVisitSkipsVanishedDirectory pins the walk outcome for a
// descendant whose entry error is fs.ErrNotExist: the subtree is skipped rather
// than failing the walk.
func TestGlobWalkerVisitSkipsVanishedDirectory(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "gone")
	mustMkdir(t, gone)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	var dirEntry fs.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			dirEntry = entry
			break
		}
	}
	if dirEntry == nil {
		t.Fatal("root has no directory entry")
	}

	walker := &globWalker{params: globWalkParams{root: root}}
	if got := walker.visit(context.Background(), gone, dirEntry, fs.ErrNotExist); !errors.Is(got, filepath.SkipDir) {
		t.Fatalf("visit error = %v, want filepath.SkipDir", got)
	}
}

func TestGlobToolDefaultsAndPaging(t *testing.T) {
	root := globWalkFixture(t)
	toolDef := NewGlobTool(workspaceTestEnv(t, root, nil, nil))

	t.Run("missing pattern lists one level with directories suffixed", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{"path": "."})
		if result.Output != "a/\na.txt\nx.go" {
			t.Errorf("Output = %q, want %q", result.Output, "a/\na.txt\nx.go")
		}
	})

	t.Run("limit and offset page through results", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "**",
			"limit":   2,
			"offset":  1,
		})
		if result.Output != "a/b\na.txt" {
			t.Errorf("Output = %q, want %q", result.Output, "a/b\na.txt")
		}
		if result.NextOffset != 3 {
			t.Errorf("NextOffset = %d, want 3", result.NextOffset)
		}
	})

	t.Run("marshalled JSON omits an absent next offset", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{"path": "."})
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("json.Marshal error: %v", err)
		}
		if strings.Contains(string(data), "next_offset") {
			t.Fatalf("marshalled JSON contains next_offset: %s", data)
		}
	})
}

func TestGlobTool(t *testing.T) {
	tmpDir := t.TempDir()
	files := []string{"c.txt", "a.txt", "b.txt", "d.go", "e.go"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte(f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	toolDef := NewGlobTool(workspaceTestEnv(t, tmpDir, nil, nil))

	t.Run("results follow walk order", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "*.txt",
			"path":    ".",
		})
		if result.Output != "a.txt\nb.txt\nc.txt" {
			t.Errorf("Output = %q, want %q", result.Output, "a.txt\nb.txt\nc.txt")
		}
	})
}

func TestGlobTool_Exclusions(t *testing.T) {
	tmpDir := t.TempDir()

	visible := []string{"main.go", "helper.go", "README.md"}
	for _, f := range visible {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte(f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	excludedDirs := []string{".git", "node_modules", "vendor", ".steiner", ".cache", "dist", "build", "out", "target"}
	excludedFiles := []string{"config", "index.js", "lib.go", "secret.yaml", "cache.bin", "bundle.js", "output.o", "result", "binary"}
	for i, dir := range excludedDirs {
		dirPath := filepath.Join(tmpDir, dir)
		if err := os.Mkdir(dirPath, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dirPath, excludedFiles[i]), []byte(excludedFiles[i]), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", dir, excludedFiles[i], err)
		}
	}

	nestedExcluded := filepath.Join(tmpDir, "node_modules", "deep", ".cache", "nested.txt")
	if err := os.MkdirAll(filepath.Dir(nestedExcluded), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(nestedExcluded, []byte("nested"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	toolDef := NewGlobTool(workspaceTestEnv(t, tmpDir, nil, nil))

	t.Run("excluded directories not returned in results", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "*",
			"path":    ".",
		})
		for _, dir := range excludedDirs {
			if strings.Contains(result.Output, dir+"/") || result.Output == dir {
				t.Errorf("output contains excluded directory %q: %q", dir, result.Output)
			}
		}
	})

	t.Run("visible files are still found", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "*.go",
			"path":    ".",
		})
		for _, want := range []string{"main.go", "helper.go"} {
			if !strings.Contains(result.Output, want) {
				t.Errorf("output missing visible file %s: %q", want, result.Output)
			}
		}
	})

	t.Run("deeply nested excluded files not returned", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "**/*.txt",
			"path":    ".",
		})
		if strings.Contains(result.Output, "nested.txt") {
			t.Errorf("output contains deeply nested excluded file nested.txt: %q", result.Output)
		}
	})
}

func TestGlobWalk_DoublestarPatterns(t *testing.T) {
	tmpDir := t.TempDir()

	dirs := []string{"sub1", "sub2", "sub1/nested"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	files := []string{
		"root.go",
		"sub1/a.go",
		"sub1/b.txt",
		"sub2/c.go",
		"sub1/nested/d.go",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte(f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	t.Run("**/*.go matches Go files in all subdirectories", func(t *testing.T) {
		result := runGlobWalk(t, globWalkParamsFor(t, tmpDir, "**/*.go", defaultGlobLimit, 0))
		want := []string{"root.go", "sub1/a.go", "sub1/nested/d.go", "sub2/c.go"}
		if got := outputRows(result.Output, ""); !slices.Equal(got, want) {
			t.Fatalf("entries = %v, want %v", got, want)
		}
	})

	t.Run("*.{go,txt} matches go and txt files at root only", func(t *testing.T) {
		result := runGlobWalk(t, globWalkParamsFor(t, tmpDir, "*.{go,txt}", defaultGlobLimit, 0))
		if got := outputRows(result.Output, ""); !slices.Equal(got, []string{"root.go"}) {
			t.Fatalf("entries = %v, want [root.go]", got)
		}
	})

	t.Run("**/AGENTS.md includes root and nested matches", func(t *testing.T) {
		for _, rel := range []string{"AGENTS.md", "sub1/AGENTS.md"} {
			if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(rel), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}

		result := runGlobWalk(t, globWalkParamsFor(t, tmpDir, "**/AGENTS.md", defaultGlobLimit, 0))
		want := []string{"AGENTS.md", "sub1/AGENTS.md"}
		if got := outputRows(result.Output, ""); !slices.Equal(got, want) {
			t.Fatalf("entries = %v, want %v", got, want)
		}
	})
}

func TestGlobTool_CustomExcludePaths(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	secretDir := filepath.Join(tmpDir, "secret")
	if err := os.Mkdir(secretDir, 0o755); err != nil {
		t.Fatalf("mkdir secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "key.txt"), []byte("key"), 0o644); err != nil {
		t.Fatalf("write secret/key.txt: %v", err)
	}

	toolDef := NewGlobTool(workspaceTestEnv(t, tmpDir, []string{secretDir}, nil))

	t.Run("custom exact path exclusion", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "**",
			"path":    ".",
		})
		if strings.Contains(result.Output, "secret/") || strings.Contains(result.Output, "key.txt") {
			t.Errorf("output contains excluded path: %q", result.Output)
		}
		if !strings.Contains(result.Output, "main.go") {
			t.Errorf("output missing visible file main.go: %q", result.Output)
		}
	})
}

func TestGlobTool_CustomExcludePatterns(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.Mkdir(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "data.bak"), []byte("bak"), 0o644); err != nil {
		t.Fatalf("write backups/data.bak: %v", err)
	}

	toolDef := NewGlobTool(workspaceTestEnv(t, tmpDir, nil, []string{"*.bak"}))

	t.Run("custom glob pattern exclusion", func(t *testing.T) {
		result := runGlobTool(t, toolDef, map[string]any{
			"pattern": "**",
			"path":    ".",
		})
		if strings.Contains(result.Output, ".bak") {
			t.Errorf("output contains excluded pattern .bak: %q", result.Output)
		}
		if !strings.Contains(result.Output, "main.go") {
			t.Errorf("output missing visible file main.go: %q", result.Output)
		}
	})
}

func TestGlobWalk_PermissionDeniedSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, cannot test permission denial")
	}

	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "visible.txt"), []byte("visible"), 0o644); err != nil {
		t.Fatalf("write visible.txt: %v", err)
	}

	blockedDir := filepath.Join(tmpDir, "blocked")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write blocked/secret.txt: %v", err)
	}

	if err := os.Chmod(blockedDir, 0o000); err != nil {
		t.Fatalf("chmod blocked: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blockedDir, 0o755); err != nil {
			t.Errorf("restore blocked dir permissions: %v", err)
		}
	})

	if _, err := os.ReadDir(blockedDir); err == nil {
		t.Skip("filesystem does not deny directory traversal after chmod 000")
	}

	result := runGlobWalk(t, globWalkParamsFor(t, tmpDir, "**", defaultGlobLimit, 0))
	lines := outputRows(result.Output, "")
	if !slices.Contains(lines, "visible.txt") {
		t.Errorf("visible.txt not found in entries: %v", lines)
	}
	for _, line := range lines {
		if line != "blocked/" && strings.HasPrefix(line, "blocked/") {
			t.Errorf("got blocked directory child in entries: %q", line)
		}
	}
}
