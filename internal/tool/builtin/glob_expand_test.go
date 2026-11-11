package builtin

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestValidateGlobBraces(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{name: "no braces", pattern: "pkg/a.go"},
		{name: "matched group", pattern: "{a,b}/x.go"},
		{name: "empty group", pattern: "aa{}"},
		{name: "nested groups", pattern: "{a,{b,c}}/x.go"},
		{name: "escaped open brace", pattern: `a\{b`},
		{name: "escaped close brace", pattern: `a\}b`},
		{name: "escaped pair", pattern: `a\{b\}`},
		{name: "open brace in a class", pattern: "[{]x"},
		{name: "close brace in a class", pattern: "[}]x"},
		{name: "braces in separate classes", pattern: "[{][}]x"},
		{name: "class holding a close brace inside a group", pattern: "{a,[}]}x"},
		{name: "escaped close brace leaves the group open", pattern: `{a\}`, wantErr: true},
		{name: "trailing escape is valid", pattern: `a\`},
		{name: "lone open brace", pattern: "aa{", wantErr: true},
		{name: "lone close brace", pattern: "a}b", wantErr: true},
		{name: "unterminated group", pattern: "{a,", wantErr: true},
		{name: "unterminated nested group", pattern: "{x{c,d}", wantErr: true},
		{name: "unterminated group after a literal", pattern: "a{x{c,d}", wantErr: true},
		{name: "closed group inside an open group", pattern: "{a{b,c}", wantErr: true},
		{name: "extra close brace after a group", pattern: "{a,b}}", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGlobBraces(tt.pattern)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("validateGlobBraces(%q) error = %v, want nil", tt.pattern, err)
				}
				return
			}
			if !errors.Is(err, errGlobUnmatchedBrace) {
				t.Fatalf("validateGlobBraces(%q) error = %v, want errGlobUnmatchedBrace", tt.pattern, err)
			}
		})
	}
}

func TestExpandGlobBraces(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{name: "literal pattern is unchanged", pattern: "pkg/a.go", want: []string{"pkg/a.go"}},
		{name: "top-level group", pattern: "a.{go,md}", want: []string{"a.go", "a.md"}},
		{name: "empty alternative expands away", pattern: "aa{}", want: []string{"aa"}},
		{name: "nested groups", pattern: "{a,{b,c}}/x.go", want: []string{"a/x.go", "b/x.go", "c/x.go"}},
		{name: "nested group on one alternative", pattern: "a{b,{c,d}}", want: []string{"ab", "ac", "ad"}},
		{name: "escaped braces are not groups", pattern: `a\{b,c\}`, want: []string{`a\{b,c\}`}},
		{name: "braces in separate classes are not groups", pattern: "[{][}]x", want: []string{"[{][}]x"}},
		{name: "group inside a class is not expanded", pattern: "[{a,b}]x", want: []string{"[{a,b}]x"}},
		{name: "class inside a group is not split", pattern: "{[a,b],c}", want: []string{"[a,b]", "c"}},
		{name: "escaped close brace keeps its alternative", pattern: `{a\},b}`, want: []string{`a\}`, "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandGlobBraces(tt.pattern)
			if err != nil {
				t.Fatalf("expandGlobBraces(%q) error: %v", tt.pattern, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("expandGlobBraces(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandGlobBracesRejectsUnmatched(t *testing.T) {
	if _, err := expandGlobBraces("aa{"); !errors.Is(err, errGlobUnmatchedBrace) {
		t.Fatalf("expandGlobBraces(%q) error = %v, want errGlobUnmatchedBrace", "aa{", err)
	}
}

func TestExpandGlobBracesCap(t *testing.T) {
	atLimit := strings.Repeat("{a,b}", 8)
	got, err := expandGlobBraces(atLimit)
	if err != nil {
		t.Fatalf("expandGlobBraces(%q) error: %v", atLimit, err)
	}
	if len(got) != maxGlobExpansions {
		t.Errorf("forms = %d, want %d", len(got), maxGlobExpansions)
	}

	overLimit := strings.Repeat("{a,b}", 9)
	if _, err := expandGlobBraces(overLimit); !errors.Is(err, errGlobTooComplex) {
		t.Fatalf("expandGlobBraces(%q) error = %v, want errGlobTooComplex", overLimit, err)
	}
}

func TestExpandGlobZeroDir(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{name: "no doublestar", pattern: "pkg/a.go", want: []string{"pkg/a.go"}},
		{name: "leading doublestar", pattern: "**/a.go", want: []string{"**/a.go", "a.go"}},
		{name: "inner doublestar", pattern: "a/**/b.go", want: []string{"a/**/b.go", "a/b.go"}},
		{
			name:    "two doublestar segments",
			pattern: "**/a/**/b.go",
			want:    []string{"**/a/**/b.go", "**/a/b.go", "a/**/b.go", "a/b.go"},
		},
		{name: "doublestar attached to a literal is kept", pattern: "foo**/bar.go", want: []string{"foo**/bar.go"}},
		{name: "trailing doublestar without a slash is kept", pattern: "a/**", want: []string{"a/**"}},
		{name: "escaped doublestar is kept", pattern: `a/\**/b.go`, want: []string{`a/\**/b.go`}},
		{name: "doublestar in a class is kept", pattern: "x[**/]y", want: []string{"x[**/]y"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandGlobZeroDir(tt.pattern)
			if err != nil {
				t.Fatalf("expandGlobZeroDir(%q) error: %v", tt.pattern, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("expandGlobZeroDir(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandGlobZeroDirCap(t *testing.T) {
	atLimit := globExpansionCapPattern(8)
	got, err := expandGlobZeroDir(atLimit)
	if err != nil {
		t.Fatalf("expandGlobZeroDir(%q) error: %v", atLimit, err)
	}
	if len(got) != maxGlobExpansions {
		t.Errorf("forms = %d, want %d", len(got), maxGlobExpansions)
	}

	overLimit := globExpansionCapPattern(9)
	if _, err := expandGlobZeroDir(overLimit); !errors.Is(err, errGlobTooComplex) {
		t.Fatalf("expandGlobZeroDir(9 segments) error = %v, want errGlobTooComplex", err)
	}
}
