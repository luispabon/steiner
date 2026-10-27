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
			name:   "content without shown reports no count",
			args:   content,
			result: `{"output":"## a.go\n3: context\n"}`,
			want:   0,
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
