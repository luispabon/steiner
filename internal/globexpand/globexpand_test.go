package globexpand

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestValidateBraces(t *testing.T) {
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
			err := ValidateBraces(tt.pattern)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("ValidateBraces(%q) error = %v, want nil", tt.pattern, err)
				}
				return
			}
			if !errors.Is(err, ErrUnmatchedBrace) {
				t.Fatalf("ValidateBraces(%q) error = %v, want ErrUnmatchedBrace", tt.pattern, err)
			}
		})
	}
}

func TestExpandBraces(t *testing.T) {
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
			got, err := ExpandBraces(tt.pattern)
			if err != nil {
				t.Fatalf("ExpandBraces(%q) error: %v", tt.pattern, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ExpandBraces(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandBracesRejectsUnmatched(t *testing.T) {
	if _, err := ExpandBraces("aa{"); !errors.Is(err, ErrUnmatchedBrace) {
		t.Fatalf("ExpandBraces(%q) error = %v, want ErrUnmatchedBrace", "aa{", err)
	}
}

func TestExpandBracesCap(t *testing.T) {
	atLimit := strings.Repeat("{a,b}", 8)
	got, err := ExpandBraces(atLimit)
	if err != nil {
		t.Fatalf("ExpandBraces(%q) error: %v", atLimit, err)
	}
	if len(got) != MaxExpansions {
		t.Errorf("forms = %d, want %d", len(got), MaxExpansions)
	}

	overLimit := strings.Repeat("{a,b}", 9)
	if _, err := ExpandBraces(overLimit); !errors.Is(err, ErrTooComplex) {
		t.Fatalf("ExpandBraces(%q) error = %v, want ErrTooComplex", overLimit, err)
	}
}

func TestExpandZeroDir(t *testing.T) {
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
			got, err := ExpandZeroDir(tt.pattern)
			if err != nil {
				t.Fatalf("ExpandZeroDir(%q) error: %v", tt.pattern, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ExpandZeroDir(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandZeroDirCap(t *testing.T) {
	atLimit := zeroDirCapPattern(8)
	got, err := ExpandZeroDir(atLimit)
	if err != nil {
		t.Fatalf("ExpandZeroDir(%q) error: %v", atLimit, err)
	}
	if len(got) != MaxExpansions {
		t.Errorf("forms = %d, want %d", len(got), MaxExpansions)
	}

	overLimit := zeroDirCapPattern(9)
	if _, err := ExpandZeroDir(overLimit); !errors.Is(err, ErrTooComplex) {
		t.Fatalf("ExpandZeroDir(%q) error = %v, want ErrTooComplex", overLimit, err)
	}
}

// zeroDirCapPattern builds a pattern with "**/" stages between distinct
// literals, so zero-directory expansion yields exactly 2^segments forms.
func zeroDirCapPattern(segments int) string {
	var builder strings.Builder
	for i := range segments {
		fmt.Fprintf(&builder, "l%d/**/", i)
	}
	builder.WriteString("leaf/x")
	return builder.String()
}

// FuzzExpandBraces proves expansion of any input either fails with one of the
// shared errors or stays within the cap, never panicking on malformed braces.
func FuzzExpandBraces(f *testing.F) {
	for _, seed := range []string{"", "aa{", "aa{}", "aa{,}", "{a/b,c}", `a\{b`, "[{]x", "a{b,{c,d}}", "{a,", "}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		forms, err := ExpandBraces(pattern)
		if err != nil {
			if !errors.Is(err, ErrUnmatchedBrace) && !errors.Is(err, ErrTooComplex) {
				t.Fatalf("ExpandBraces(%q) error = %v, want ErrUnmatchedBrace or ErrTooComplex", pattern, err)
			}
			return
		}
		if len(forms) > MaxExpansions {
			t.Fatalf("ExpandBraces(%q) returned %d forms, want at most %d", pattern, len(forms), MaxExpansions)
		}
		for _, form := range forms {
			if err := ValidateBraces(form); err != nil {
				t.Fatalf("ExpandBraces(%q) returned form %q with unmatched braces", pattern, form)
			}
		}
	})
}
