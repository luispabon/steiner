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
		{name: "leading dot slash", pattern: "./*/*", want: 2},
		{name: "doublestar", pattern: "**/*.go", want: 0},
		{name: "multiple doublestar", pattern: "a/**/b/**/c.go", want: 0},
		{name: "brace alternatives at one depth", pattern: "*.{go,txt}", want: 1},
		{name: "brace alternative with its own slash", pattern: "{a,b/c}", want: 2},
		{name: "deepest brace alternative wins", pattern: "{src/lib,cmd}/*.go", want: 3},
		{name: "nested braces", pattern: "a/{b,{c,d}}/x.go", want: 3},
		{name: "brace alternative with a doublestar", pattern: "{a,**/b}", want: 0},
		{name: "brace alternative with a class", pattern: "{a,[/]b}", want: 0},
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

// TestGlobMatcherPatternNormalization pins the pattern rules: "." segments are
// dropped, including inside a brace alternative, an alternative left with
// nothing matches nothing while an empty pattern, ".", or "./" means the
// default pattern, and a pattern that leaves the named directory is rejected
// with an error naming the cause: an absolute pattern or a ".." segment. A name
// that merely starts with a dot is not a dot segment.
func TestGlobMatcherPatternNormalization(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
		wantErr error
	}{
		{name: "leading dot slash", pattern: "./*.go", want: []string{"x.go"}},
		{name: "repeated dot slash", pattern: "././x", want: []string{"x"}},
		{name: "dot prefix inside a brace alternative", pattern: "{./a,b}", want: []string{"a", "b"}},
		{name: "inner dot segment", pattern: "a/./b", want: []string{"a/b"}},
		{name: "inner dot segment inside a brace alternative", pattern: "{dot/./x,a}", want: []string{"dot/x", "a"}},
		{name: "trailing dot segment", pattern: "a/.", want: []string{"a"}},
		{name: "dot segments around a directory", pattern: "./a/./", want: []string{"a/"}},
		{name: "dot-only alternative matches nothing", pattern: "{./,zzz}", want: []string{"zzz"}},
		{name: "dot dot name is not a dot segment", pattern: "..foo", want: []string{"..foo"}},
		{name: "hidden name is not a dot segment", pattern: ".hidden", want: []string{".hidden"}},
		{name: "bare dot slash is the default", pattern: "./", want: []string{"x.go"}},
		{name: "bare dot is the default", pattern: ".", want: []string{"x.go"}},
		{name: "empty pattern is the default", pattern: "", want: []string{"x.go"}},
		{name: "leading slash", pattern: "/abs", wantErr: errGlobPatternAbsolute},
		{name: "absolute brace alternative left with a slash", pattern: "{.//,a}", wantErr: errGlobPatternAbsolute},
		{name: "leading dot dot segment", pattern: "../x", wantErr: errGlobPatternParent},
		{name: "inner dot dot segment", pattern: "a/../b", wantErr: errGlobPatternParent},
		{name: "dot dot segment inside a brace alternative", pattern: "{a,../b}", wantErr: errGlobPatternParent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := compileGlobMatcher(tt.pattern)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("compileGlobMatcher(%q) error = %v, want %v", tt.pattern, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("compileGlobMatcher(%q): %v", tt.pattern, err)
			}
			for _, want := range tt.want {
				path, isDir := strings.TrimSuffix(want, "/"), strings.HasSuffix(want, "/")
				if _, ok := matcher.matchEntry(path, isDir); !ok {
					t.Errorf("pattern %q does not match %q", tt.pattern, want)
				}
			}
		})
	}
}

// TestGlobMatcherDedupesForms proves identical expanded forms compile once.
func TestGlobMatcherDedupesForms(t *testing.T) {
	matcher, err := compileGlobMatcher("{a,a}")
	if err != nil {
		t.Fatalf("compileGlobMatcher: %v", err)
	}
	if len(matcher.matchers) != 1 {
		t.Errorf("matchers = %d, want 1 for duplicate brace alternatives", len(matcher.matchers))
	}
}

// TestGlobMatcherQuestionMarkSpansNoSeparator pins that "?" matches one
// character of a single path segment: a?b never matches the directory a/b, and
// the pattern still prunes at its own depth.
func TestGlobMatcherQuestionMarkSpansNoSeparator(t *testing.T) {
	matcher, err := compileGlobMatcher("a?b")
	if err != nil {
		t.Fatalf("compileGlobMatcher: %v", err)
	}
	if _, ok := matcher.matchEntry("a/b", true); ok {
		t.Error(`pattern "a?b" matched directory "a/b"`)
	}
	if _, ok := matcher.matchEntry("axb", false); !ok {
		t.Error(`pattern "a?b" did not match "axb"`)
	}
	if matcher.pruneDepth != 1 {
		t.Errorf("pruneDepth = %d, want 1", matcher.pruneDepth)
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
