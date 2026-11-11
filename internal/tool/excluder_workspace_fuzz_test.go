package tool

import "testing"

// FuzzWorkspaceExcluder proves no configured pattern, including the malformed
// brace forms that panic inside gobwas, can crash matching, and that the
// malformed-pattern report stays bounded and self-consistent.
func FuzzWorkspaceExcluder(f *testing.F) {
	seeds := []struct {
		pattern string
		rel     string
	}{
		{pattern: "", rel: "a"},
		{pattern: "aa{", rel: "aa"},
		{pattern: "aa{}", rel: "aa/a"},
		{pattern: "aa{,}", rel: "aa"},
		{pattern: "gen{", rel: "src/gen/out.go"},
		{pattern: "{a/b,c}", rel: "x/c"},
		{pattern: "x/{a/b,c}", rel: "x/a/b"},
		{pattern: "{**,a}/b", rel: "a/y/b"},
		{pattern: "{.}/.", rel: "a"},
		{pattern: "[abc", rel: "a"},
		{pattern: "**/{a,b}", rel: "a/b/c"},
		{pattern: "{a,}/b", rel: "a/b"},
	}
	for _, seed := range seeds {
		f.Add(seed.pattern, seed.rel)
	}
	f.Fuzz(func(t *testing.T, pattern, rel string) {
		// gobwas compilation cost grows with pattern length, so cap the inputs to
		// keep a bounded run fast. The glob tool shares that cost and is not what
		// this target covers.
		if len(pattern) > 96 || len(rel) > 64 {
			return
		}
		ex := newWorkspaceExcluder("/ws", nil, []string{pattern}, nil, false)
		for _, isDir := range []bool{false, true} {
			// A panic fails the fuzz target with the crashing input.
			ex.ExcludeEntry("/ws", "/ws/"+rel, isDir)
			ex.ExcludeEntry("", "/ws/"+rel, isDir)
		}
		invalid, count := ex.InvalidExcludePatterns()
		if len(invalid) > maxInvalidExcludePatternReports {
			t.Fatalf("reported %d entries, want at most %d", len(invalid), maxInvalidExcludePatternReports)
		}
		if count < len(invalid) {
			t.Fatalf("count = %d is smaller than the %d reported entries", count, len(invalid))
		}
	})
}
