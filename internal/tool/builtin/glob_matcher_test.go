package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGlobMatcherDepthPruning(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    int
	}{
		{name: "single segment", pattern: "*.go", want: 1},
		{name: "trailing slash", pattern: "dir/", want: 1},
		{name: "two segments", pattern: "src/*.go", want: 2},
		{name: "two segments with trailing slash", pattern: "a/b/", want: 2},
		{name: "doublestar", pattern: "**/*.go", want: 0},
		{name: "multiple doublestar", pattern: "a/**/b/**/c.go", want: 0},
		{name: "brace with slash", pattern: "{src/lib,cmd}/*.go", want: 0},
		{name: "nested braces", pattern: "a/{b,{c,d}}/x.go", want: 0},
		{name: "escape", pattern: `a\*.go`, want: 0},
		{name: "class spanning a separator", pattern: "a[!x]b", want: 0},
		{name: "class in a later segment", pattern: "src[!x]lib/*.go", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := compileGlobMatcher(tt.pattern)
			if err != nil {
				t.Fatalf("compileGlobMatcher(%q): %v", tt.pattern, err)
			}
			if matcher.pruneDepth != tt.want {
				t.Errorf("pruneDepth = %d, want %d", matcher.pruneDepth, tt.want)
			}
		})
	}
}

// globExpansionCapPattern builds a pattern with "**/" stages between distinct
// literals, so zero-directory expansion yields exactly 2^segments forms.
func globExpansionCapPattern(segments int) string {
	var builder strings.Builder
	for i := range segments {
		fmt.Fprintf(&builder, "l%d/**/", i)
	}
	builder.WriteString("leaf/x")
	return builder.String()
}

// TestGlobMatcherExpansionCap proves the shared expansion cap is enforced: a
// pattern under the cap compiles, the exact boundary still compiles, and one
// over it fails with the shared cap error.
func TestGlobMatcherExpansionCap(t *testing.T) {
	t.Run("under the cap compiles", func(t *testing.T) {
		matcher, err := compileGlobMatcher(globExpansionCapPattern(7))
		if err != nil {
			t.Fatalf("compileGlobMatcher: %v", err)
		}
		if len(matcher.matchers) != 128 {
			t.Errorf("matchers = %d, want 128", len(matcher.matchers))
		}
	})

	t.Run("exact boundary compiles", func(t *testing.T) {
		matcher, err := compileGlobMatcher(globExpansionCapPattern(8))
		if err != nil {
			t.Fatalf("compileGlobMatcher: %v", err)
		}
		if len(matcher.matchers) != maxGlobExpansions {
			t.Errorf("matchers = %d, want %d", len(matcher.matchers), maxGlobExpansions)
		}
	})

	t.Run("over the cap fails", func(t *testing.T) {
		_, err := compileGlobMatcher(globExpansionCapPattern(9))
		if err == nil {
			t.Fatal("compileGlobMatcher succeeded for a pattern past the expansion cap")
		}
		if !errors.Is(err, errGlobTooComplex) {
			t.Fatalf("error = %v, want the shared cap error", err)
		}
	})

	t.Run("handler reports the cap", func(t *testing.T) {
		root := t.TempDir()
		toolDef := NewGlobTool(workspaceTestEnv(t, root, nil, nil))
		_, err := toolDef.Handler(t.Context(), map[string]any{"pattern": globExpansionCapPattern(9)})
		if err == nil || !strings.Contains(err.Error(), "glob: "+errGlobTooComplex.Error()) {
			t.Fatalf("Handler error = %v, want the glob-prefixed cap error", err)
		}
	})
}

// TestGlobMatcherDoublestarAfterLiteral pins the rule that a doublestar glued to
// a literal prefix spans separators but does not match the literal text itself:
// zero-directory expansion leaves "foo**/" alone, so "foobar.go" never matches.
func TestGlobMatcherDoublestarAfterLiteral(t *testing.T) {
	matcher, err := compileGlobMatcher("foo**/bar.go")
	if err != nil {
		t.Fatalf("compileGlobMatcher: %v", err)
	}
	for _, path := range []string{"foo/bar.go", "fooX/bar.go"} {
		if _, ok := matcher.matchEntry(path, false); !ok {
			t.Errorf("pattern %q should match %q", "foo**/bar.go", path)
		}
	}
	if _, ok := matcher.matchEntry("foobar.go", false); ok {
		t.Errorf("pattern %q should not match %q", "foo**/bar.go", "foobar.go")
	}
}
