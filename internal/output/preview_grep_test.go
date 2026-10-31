package output

import "testing"

func TestBuildGrepPreviewMatchCounts(t *testing.T) {
	t.Parallel()

	content := map[string]any{"output_mode": "content"}
	count := map[string]any{"output_mode": "count"}
	files := map[string]any{"output_mode": "files_with_matches"}
	tests := []struct {
		name   string
		args   map[string]any
		result string
		want   int
	}{
		{
			name:   "content counts shown matches, not context lines",
			args:   content,
			result: `{"matches":5,"shown":2,"next_offset":2,"output":"## a.go\n3:1: match one\n4: context\n5:1: match two\n"}`,
			want:   2,
		},
		{
			name:   "content without shown and no match rows reports zero",
			args:   content,
			result: `{"output":"## a.go\n3: context\n"}`,
			want:   0,
		},
		{
			name:   "content without shown counts legacy match rows",
			args:   content,
			result: `{"output":"## a.go\n3:1: match one\n4: context\n5:1: match two\n"}`,
			want:   2,
		},
		{
			name:   "explicit zero shown ignores legacy rows",
			args:   content,
			result: `{"shown":0,"output":"## a.go\n3:1: stale row\n"}`,
			want:   0,
		},
		// Legacy limitation: multiline continuation matches were rendered as
		// "N: " rows, the same as context. The fallback cannot tell them apart,
		// so it counts 1 here where 3 results were shown.
		{
			name:   "legacy multiline continuation rows are not counted",
			args:   content,
			result: `{"output":"## a.txt\n1:1: A\n2: B\n3: C\n"}`,
			want:   1,
		},
		{
			name:   "content excerpt marker is not a match",
			args:   content,
			result: `{"shown":1,"output":"## big.go\n300:1: needle\n<truncated output shown=198 total=501>"}`,
			want:   1,
		},
		{
			name:   "count sums per-file counts",
			args:   count,
			result: `{"output":"a.go:2\nb.go:1\n"}`,
			want:   3,
		},
		{
			name:   "files_with_matches reports no match count",
			args:   files,
			result: `{"shown":2,"output":"a.go\nb.go\n"}`,
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildToolPreview("grep", tt.args, tt.result)
			if got.Matches != tt.want {
				t.Fatalf("Matches = %d, want %d", got.Matches, tt.want)
			}
		})
	}
}

func TestBuildGrepPreviewReportsTotalMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		result    string
		wantShown int
		wantTotal int
	}{
		{
			name:      "page past the last result keeps the search total",
			result:    `{"matches":12,"shown":0,"output":""}`,
			wantTotal: 12,
		},
		{
			name:      "legacy page without shown keeps the search total",
			result:    `{"matches":7,"output":""}`,
			wantTotal: 7,
		},
		{
			name:   "no matches reports no total",
			result: `{"shown":0,"output":"No matches found"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildToolPreview("grep", map[string]any{"output_mode": "content"}, tt.result)
			if got.Matches != tt.wantShown {
				t.Fatalf("Matches = %d, want %d", got.Matches, tt.wantShown)
			}
			if got.TotalMatches != tt.wantTotal {
				t.Fatalf("TotalMatches = %d, want %d", got.TotalMatches, tt.wantTotal)
			}
			if len(got.GrepFiles) != 0 {
				t.Fatalf("GrepFiles = %d entries, want none", len(got.GrepFiles))
			}
		})
	}
}

func TestBuildGrepPreviewPassesPagingMetadata(t *testing.T) {
	t.Parallel()

	got := BuildToolPreview("grep", map[string]any{"output_mode": "content"},
		`{"matches":9,"shown":2,"next_offset":4,"output":"## a.go\n3:1: match one\n4: context\n5:1: match two\n"}`)
	if got.NextOffset != 4 {
		t.Fatalf("NextOffset = %d, want 4", got.NextOffset)
	}
	if got.Matches != 2 || got.Returned != 1 {
		t.Fatalf("Matches = %d, Returned = %d, want 2 and 1", got.Matches, got.Returned)
	}
}
