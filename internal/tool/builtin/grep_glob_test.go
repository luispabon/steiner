package builtin

import (
	"strings"
	"testing"
)

func TestCompileGrepFileGlobMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		matches []string
		misses  []string
	}{
		{
			name:    "slashless pattern matches basename at any depth",
			pattern: "*.go",
			matches: []string{"root.go", "pkg/root.go", "pkg/deep/root.go"},
			misses:  []string{"root.md", "pkg/root.md"},
		},
		{
			name:    "slash pattern matches root-relative path only",
			pattern: "*/*.go",
			matches: []string{"pkg/root.go"},
			misses:  []string{"root.go", "pkg/deep/root.go"},
		},
		{
			name:    "slashless brace alternatives match basenames",
			pattern: "{*.go,*.md}",
			matches: []string{"pkg/root.go", "pkg/deep/notes.md"},
			misses:  []string{"pkg/root.txt", "notes.md.bak"},
		},
		{
			name:    "mixed brace alternatives decide per alternative",
			pattern: "{*.go,src/*.md}",
			matches: []string{"deep/root.go", "src/notes.md"},
			misses:  []string{"deep/src/notes.md", "deep/notes.md"},
		},
		{
			name:    "nested braces keep every alternative",
			pattern: "{a,{b,c}}/x.go",
			matches: []string{"a/x.go", "b/x.go", "c/x.go"},
			misses:  []string{"x.go", "d/x.go", "a/deep/x.go"},
		},
		{
			name:    "nested braces mix slashless and path alternatives",
			pattern: "{*.go,{src,test}/*.md}",
			matches: []string{"deep/root.go", "src/notes.md", "test/notes.md"},
			misses:  []string{"deep/notes.md", "deep/src/notes.md"},
		},
		{
			name:    "leading dot slash anchors slashless pattern",
			pattern: "./*.go",
			matches: []string{"root.go"},
			misses:  []string{"pkg/root.go"},
		},
		{
			name:    "leading dot slash anchors path pattern",
			pattern: "./src/*.go",
			matches: []string{"src/main.go"},
			misses:  []string{"pkg/src/main.go", "src/deep/main.go"},
		},
		{
			name:    "leading dot slash with doublestar still spans directories",
			pattern: "./**/*.go",
			matches: []string{"root.go", "pkg/deep/root.go"},
			misses:  []string{"root.md"},
		},
		{
			name:    "leading doublestar matches zero directories",
			pattern: "**/*.go",
			matches: []string{"root.go", "pkg/root.go", "pkg/deep/root.go"},
			misses:  []string{"root.md"},
		},
		{
			name:    "middle doublestar matches zero directories",
			pattern: "pkg/**/root.go",
			matches: []string{"pkg/root.go", "pkg/a/root.go", "pkg/a/b/root.go"},
			misses:  []string{"root.go", "other/pkg/root.go"},
		},
		{
			name:    "doublestar with brace alternative",
			pattern: "**/{src,lib}/*.go",
			matches: []string{"src/main.go", "pkg/lib/main.go"},
			misses:  []string{"main.go", "pkg/lib/main.md"},
		},
		{
			name:    "escaped wildcard stays literal",
			pattern: "\\*.go",
			matches: []string{"*.go", "pkg/*.go"},
			misses:  []string{"main.go", "pkg/main.go"},
		},
		{
			name:    "escaped doublestar is not a directory wildcard",
			pattern: "\\*\\*/x.go",
			matches: []string{"**/x.go"},
			misses:  []string{"x.go", "pkg/x.go"},
		},
		{
			name:    "bracket class matches basename at any depth",
			pattern: "[!_]*.go",
			matches: []string{"main.go", "pkg/main.go"},
			misses:  []string{"_main.go", "main.md"},
		},
		{
			name:    "bracket range matches basename at any depth",
			pattern: "file-[0-9].go",
			matches: []string{"file-1.go", "pkg/file-2.go"},
			misses:  []string{"file-x.go", "file-12.go"},
		},
		{
			name:    "doublestar inside a bracket class is not rewritten",
			pattern: "x[**/]y",
			matches: []string{"x*y", "x/y"},
			misses:  []string{"xy", "xzy"},
		},
		{
			name:    "star matches root files and doublestar matches nested files",
			pattern: "*",
			matches: []string{"root.go", "pkg/main.go", "pkg/deep/main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := compileGrepFileGlob(tt.pattern)
			if err != nil {
				t.Fatalf("compileGrepFileGlob(%q) error: %v", tt.pattern, err)
			}
			if matcher == nil {
				t.Fatalf("compileGrepFileGlob(%q) returned nil matcher", tt.pattern)
			}
			for _, path := range tt.matches {
				if !matcher.Match(path) {
					t.Errorf("pattern %q should match %q", tt.pattern, path)
				}
			}
			for _, path := range tt.misses {
				if matcher.Match(path) {
					t.Errorf("pattern %q should not match %q", tt.pattern, path)
				}
			}
		})
	}
}

func TestCompileGrepFileGlobEmptyPattern(t *testing.T) {
	matcher, err := compileGrepFileGlob("")
	if err != nil {
		t.Fatalf("compileGrepFileGlob empty pattern error: %v", err)
	}
	if matcher != nil {
		t.Fatalf("compileGrepFileGlob empty pattern = %v, want nil matcher", matcher)
	}
}

func TestCompileGrepFileGlobInvalidPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{name: "unterminated bracket class", pattern: "["},
		{name: "unterminated range", pattern: "file-[0-"},
		{name: "unterminated bracket after prefix", pattern: "src/["},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := compileGrepFileGlob(tt.pattern)
			if err == nil {
				t.Fatalf("compileGrepFileGlob(%q) error = nil, want invalid glob error", tt.pattern)
			}
			if matcher != nil {
				t.Errorf("compileGrepFileGlob(%q) matcher = %v, want nil", tt.pattern, matcher)
			}
			if !strings.Contains(err.Error(), "invalid glob") {
				t.Errorf("compileGrepFileGlob(%q) error = %q, want it to contain %q", tt.pattern, err, "invalid glob")
			}
		})
	}
}
