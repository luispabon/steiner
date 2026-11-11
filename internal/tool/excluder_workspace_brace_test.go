package tool

import (
	"slices"
	"testing"
)

// TestWorkspaceExcluder_BracePatterns pins brace handling in configured
// exclusions. Brace groups are expanded before compilation: the empty and
// unmatched forms below made a raw gobwas brace matcher slice past its input,
// and a slash-free alternative of a slash pattern must stay anchored to the
// workspace root instead of matching as a bare component name.
func TestWorkspaceExcluder_BracePatterns(t *testing.T) {
	tests := []struct {
		pattern string
		paths   map[string]bool
	}{
		{
			pattern: "aa{}",
			paths:   map[string]bool{"/ws/aa": true, "/ws/aab": false, "/ws/x/aa": true},
		},
		{
			pattern: "aa{,}",
			paths:   map[string]bool{"/ws/aa": true, "/ws/aab": false},
		},
		{
			pattern: "aa{",
			paths:   map[string]bool{"/ws/aa": false, "/ws/aab": false},
		},
		{
			pattern: "{a/b,c}",
			paths: map[string]bool{
				"/ws/a/b":   true,
				"/ws/c":     true,
				"/ws/x/c":   false,
				"/ws/x/a/b": false,
			},
		},
		{
			pattern: "x/{a/b,c}",
			paths: map[string]bool{
				"/ws/x/a/b": true,
				"/ws/x/c":   true,
				"/ws/x/y":   false,
				"/ws/c":     false,
			},
		},
		{
			pattern: "a{,}/b",
			paths:   map[string]bool{"/ws/a/b": true, "/ws/b": false},
		},
		{
			pattern: "{**,a}/b",
			paths: map[string]bool{
				"/ws/b":     true,
				"/ws/x/b":   true,
				"/ws/a/b":   true,
				"/ws/a/y/b": true,
				"/ws/a/y":   false,
			},
		},
		{
			pattern: "{.,a}/b",
			paths:   map[string]bool{"/ws/b": true, "/ws/a/b": true, "/ws/x/b": false},
		},
		{
			pattern: "src/{gen,}",
			paths:   map[string]bool{"/ws/src/gen": true, "/ws/src/x": false},
		},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			ex := newWorkspaceExcluder("/ws", nil, []string{tc.pattern}, nil, false)
			for path, want := range tc.paths {
				if got := ex.ExcludeEntry("/ws", path, false); got != want {
					t.Errorf("ExcludeEntry(%q, %q) = %v, want %v", tc.pattern, path, got, want)
				}
			}
		})
	}
}

// TestWorkspaceExcluder_InvalidPatternReports proves malformed exclusions are
// reported by configuration position with a fixed reason that carries no
// pattern text, while the remaining valid patterns still apply.
func TestWorkspaceExcluder_InvalidPatternReports(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"ok.txt", "aa{", "[abc", "*.log", "src/[abc"}, nil, false)

	invalid, count := ex.InvalidExcludePatterns()
	want := []InvalidExcludePattern{
		{Index: 1, Reason: "unmatched brace"},
		{Index: 2, Reason: "invalid pattern"},
		{Index: 4, Reason: "invalid pattern"},
	}
	if !slices.Equal(invalid, want) {
		t.Errorf("InvalidExcludePatterns() = %v, want %v", invalid, want)
	}
	if count != len(want) {
		t.Errorf("invalid count = %d, want %d", count, len(want))
	}
	for _, entry := range invalid {
		switch entry.Reason {
		case "unmatched brace", "expands to too many forms", "invalid pattern":
		default:
			t.Errorf("InvalidExcludePatterns() reason = %q, want a fixed reason", entry.Reason)
		}
	}

	for path, want := range map[string]bool{"/ws/ok.txt": true, "/ws/x.log": true, "/ws/x.go": false, "/ws/abc": false} {
		if got := ex.ExcludeEntry("/ws", path, false); got != want {
			t.Errorf("ExcludeEntry(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestWorkspaceExcluder_InvalidPatternReportsBounded proves the reported entries
// are capped while the total count stays exact.
func TestWorkspaceExcluder_InvalidPatternReportsBounded(t *testing.T) {
	patterns := make([]string, 12)
	for i := range patterns {
		patterns[i] = "a{"
	}
	ex := newWorkspaceExcluder("/ws", nil, patterns, nil, false)

	invalid, count := ex.InvalidExcludePatterns()
	if len(invalid) != maxInvalidExcludePatternReports {
		t.Fatalf("reported entries = %d, want %d", len(invalid), maxInvalidExcludePatternReports)
	}
	if count != len(patterns) {
		t.Fatalf("invalid count = %d, want %d", count, len(patterns))
	}
	for i, entry := range invalid {
		if entry.Index != i || entry.Reason != "unmatched brace" {
			t.Errorf("entry %d = %v, want index %d with an unmatched brace reason", i, entry, i)
		}
	}
}

// TestWorkspaceExcluder_OverflowingPatternReported proves an exclusion whose
// brace expansion exceeds the shared cap is reported instead of applied.
func TestWorkspaceExcluder_OverflowingPatternReported(t *testing.T) {
	ex := newWorkspaceExcluder("/ws", nil, []string{"{{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}}"}, nil, false)

	invalid, count := ex.InvalidExcludePatterns()
	want := []InvalidExcludePattern{{Index: 0, Reason: "expands to too many forms"}}
	if !slices.Equal(invalid, want) {
		t.Errorf("InvalidExcludePatterns() = %v, want %v", invalid, want)
	}
	if count != 1 {
		t.Errorf("invalid count = %d, want 1", count)
	}
}

// TestInvalidWorkspaceExcludePatterns proves the exported helper reports the
// same malformed entries the excluder records, for callers that validate
// configuration before building one.
func TestInvalidWorkspaceExcludePatterns(t *testing.T) {
	patterns := []string{"ok.txt", "aa{", "src/[abc", "src/{gen,out}"}

	ex := newWorkspaceExcluder("/ws", nil, patterns, nil, false)
	wantEntries, wantCount := ex.InvalidExcludePatterns()
	gotEntries, gotCount := InvalidWorkspaceExcludePatterns(patterns)

	if !slices.Equal(gotEntries, wantEntries) {
		t.Errorf("InvalidWorkspaceExcludePatterns() = %v, want %v", gotEntries, wantEntries)
	}
	if gotCount != wantCount {
		t.Errorf("InvalidWorkspaceExcludePatterns() count = %d, want %d", gotCount, wantCount)
	}
	if len(gotEntries) != 2 || gotCount != 2 {
		t.Fatalf("reported %v (count %d), want two malformed patterns", gotEntries, gotCount)
	}
}
