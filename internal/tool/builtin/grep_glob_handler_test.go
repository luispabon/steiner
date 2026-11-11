package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

const grepGlobHandlerNeedle = "needle"

// newGrepGlobHandlerFixture writes Go and non-Go files at the root and at two
// nesting levels, then returns the workspace root and a grep tool bound to it.
func newGrepGlobHandlerFixture(t *testing.T) (string, tool.ToolDef) {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"root.go":          "package root\n\n// needle in root\n",
		"notes.txt":        "needle in root notes\n",
		"pkg/nested.go":    "package pkg\n\n// needle in nested\n",
		"pkg/notes.txt":    "needle in pkg notes\n",
		"pkg/deep/deep.go": "package deep\n\n// needle in deep\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	return root, NewGrepTool(Env{WorkDir: root, PathPolicy: &policy})
}

func runGrepGlobHandler(t *testing.T, toolDef tool.ToolDef, input map[string]any) GrepResult {
	t.Helper()

	resultI, err := toolDef.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("grep handler error: %v", err)
	}
	result, ok := resultI.(GrepResult)
	if !ok {
		t.Fatalf("result type = %T, want GrepResult", resultI)
	}
	return result
}

func assertGrepOutputContains(t *testing.T, got string, wants []string, rejects []string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("Output = %q, want it to contain %q", got, want)
		}
	}
	for _, reject := range rejects {
		if strings.Contains(got, reject) {
			t.Errorf("Output = %q, want it to exclude %q", got, reject)
		}
	}
}

func TestGrepGlobHandlerRootPath(t *testing.T) {
	_, toolDef := newGrepGlobHandlerFixture(t)
	allGoFiles := []string{"root.go", "pkg/nested.go", "pkg/deep/deep.go"}

	t.Run("basename glob keeps nested go files and excludes other extensions", func(t *testing.T) {
		result := runGrepGlobHandler(t, toolDef, map[string]any{
			"pattern":     grepGlobHandlerNeedle,
			"output_mode": "files_with_matches",
			"glob":        "*.go",
		})
		assertGrepOutputContains(t, result.Output, allGoFiles, []string{".txt"})
	})

	t.Run("alternation pattern renders file headers and line numbers", func(t *testing.T) {
		result := runGrepGlobHandler(t, toolDef, map[string]any{
			"pattern":     "needle|absent",
			"output_mode": "content",
			"glob":        "*.go",
		})
		assertGrepOutputContains(t, result.Output, []string{
			"## root.go",
			"## pkg/nested.go",
			"## pkg/deep/deep.go",
			"3:4: // needle in root",
			"3:4: // needle in nested",
			"3:4: // needle in deep",
		}, []string{".txt"})
	})

	t.Run("doublestar glob matches go files at every depth", func(t *testing.T) {
		result := runGrepGlobHandler(t, toolDef, map[string]any{
			"pattern":     grepGlobHandlerNeedle,
			"output_mode": "files_with_matches",
			"glob":        "**/*.go",
		})
		assertGrepOutputContains(t, result.Output, allGoFiles, []string{".txt"})
	})

	t.Run("leading dot slash anchors the glob to the search root", func(t *testing.T) {
		result := runGrepGlobHandler(t, toolDef, map[string]any{
			"pattern":     grepGlobHandlerNeedle,
			"output_mode": "files_with_matches",
			"glob":        "./*.go",
		})
		assertGrepOutputContains(t, result.Output, []string{"root.go"}, []string{"pkg/"})
	})
}

func TestGrepGlobHandlerSubdirectoryPath(t *testing.T) {
	_, toolDef := newGrepGlobHandlerFixture(t)

	result := runGrepGlobHandler(t, toolDef, map[string]any{
		"pattern":     grepGlobHandlerNeedle,
		"output_mode": "files_with_matches",
		"glob":        "*.go",
		"path":        "pkg",
	})
	assertGrepOutputContains(t, result.Output, []string{"nested.go", "deep/deep.go"}, []string{"notes.txt"})
}

func TestGrepGlobHandlerExplicitFiles(t *testing.T) {
	root, toolDef := newGrepGlobHandlerFixture(t)

	tests := []struct {
		name string
		path string
	}{
		{name: "relative nested file", path: "pkg/nested.go"},
		{name: "absolute nested file", path: filepath.Join(root, "pkg", "nested.go")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGrepGlobHandler(t, toolDef, map[string]any{
				"pattern":     grepGlobHandlerNeedle,
				"output_mode": "files_with_matches",
				"glob":        "*.go",
				"path":        tt.path,
			})
			if result.Matches != 0 || result.Shown != 1 {
				t.Fatalf("Matches = %d, Shown = %d, want one shown file", result.Matches, result.Shown)
			}
			if !strings.Contains(result.Output, "nested.go") {
				t.Errorf("Output = %q, want it to contain %q", result.Output, "nested.go")
			}
		})
	}
}

// TestGrepGlobHandlerMalformedFileGlob pins the handler's answer to malformed
// file_glob braces: an unescaped brace with no counterpart fails the call, while
// an escaped brace or a brace inside a character class matches a literal brace
// name and an empty group expands away.
func TestGrepGlobHandlerMalformedFileGlob(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, root, "aa", grepGlobHandlerNeedle+"\n")
	mustWriteTree(t, root, "a{b", grepGlobHandlerNeedle+"\n")
	mustWriteTree(t, root, "{x", grepGlobHandlerNeedle+"\n")
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	toolDef := NewGrepTool(Env{WorkDir: root, PathPolicy: &policy})

	tests := []struct {
		name    string
		glob    string
		want    string
		wantErr bool
	}{
		{name: "unterminated brace fails the call", glob: "aa{", wantErr: true},
		{name: "empty group matches its prefix", glob: "aa{}", want: "aa"},
		{name: "escaped brace matches a literal name", glob: `a\{b`, want: "a{b"},
		{name: "brace in a class matches a literal name", glob: "[{]x", want: "{x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]any{
				"pattern":     grepGlobHandlerNeedle,
				"output_mode": "files_with_matches",
				"glob":        tt.glob,
			}
			if tt.wantErr {
				_, err := toolDef.Handler(context.Background(), input)
				if !errors.Is(err, errGlobUnmatchedBrace) {
					t.Fatalf("grep handler error = %v, want errGlobUnmatchedBrace", err)
				}
				if !strings.Contains(err.Error(), "invalid glob") {
					t.Errorf("grep handler error = %q, want it to contain %q", err, "invalid glob")
				}
				return
			}
			result := runGrepGlobHandler(t, toolDef, input)
			if result.Output != tt.want {
				t.Errorf("Output = %q, want %q", result.Output, tt.want)
			}
		})
	}
}

func TestGrepGlobHandlerInvalidPattern(t *testing.T) {
	_, toolDef := newGrepGlobHandlerFixture(t)

	_, err := toolDef.Handler(context.Background(), map[string]any{
		"pattern": grepGlobHandlerNeedle,
		"glob":    "[",
	})
	if err == nil {
		t.Fatal("grep handler error = nil, want invalid glob error")
	}
	if !strings.Contains(err.Error(), "invalid glob") {
		t.Errorf("grep handler error = %q, want it to contain %q", err, "invalid glob")
	}
}
