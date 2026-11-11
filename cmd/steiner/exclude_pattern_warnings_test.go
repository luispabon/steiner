package main

import (
	"slices"
	"strings"
	"testing"
)

func TestExcludePatternDevLogWarningsReportsPositionAndReason(t *testing.T) {
	t.Parallel()
	patterns := []string{"ok.txt", "aa{", "src/{gen,out}", "[abc"}

	got := excludePatternDevLogWarnings(patterns)
	want := []string{
		"paths.exclude_patterns entry 2: unmatched brace; the entry is ignored",
		"paths.exclude_patterns entry 4: invalid pattern; the entry is ignored",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("excludePatternDevLogWarnings() = %q, want %q", got, want)
	}
	for _, warning := range got {
		if strings.Contains(warning, "aa{") || strings.Contains(warning, "[abc") {
			t.Errorf("warning restates configured pattern text: %q", warning)
		}
	}
}

func TestExcludePatternDevLogWarningsSilentForValidPatterns(t *testing.T) {
	t.Parallel()
	for _, patterns := range [][]string{nil, {}, {"src/{gen,out}", "**/fixtures", "*.log", "build/"}} {
		if got := excludePatternDevLogWarnings(patterns); len(got) != 0 {
			t.Errorf("excludePatternDevLogWarnings(%v) = %q, want no warnings", patterns, got)
		}
	}
}

func TestExcludePatternDevLogWarningsBoundsReports(t *testing.T) {
	t.Parallel()
	patterns := make([]string, 11)
	for i := range patterns {
		patterns[i] = "a{"
	}

	got := excludePatternDevLogWarnings(patterns)
	if len(got) != 9 {
		t.Fatalf("warnings = %d, want 8 entries plus an overflow line: %q", len(got), got)
	}
	if want := "paths.exclude_patterns entry 1: unmatched brace; the entry is ignored"; got[0] != want {
		t.Errorf("first warning = %q, want %q", got[0], want)
	}
	if want := "paths.exclude_patterns: 3 more invalid entries not listed"; got[8] != want {
		t.Errorf("overflow warning = %q, want %q", got[8], want)
	}
}
