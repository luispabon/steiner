package output

import "testing"

func TestBuildGrepPreviewScanCounters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result string
		want   *ToolPreviewGrepScan
	}{
		{
			name:   "counters are reported",
			result: `{"shown":0,"output":"No matches found","files_searched":4,"skipped_binary":2,"skipped_unreadable":1,"skipped_directories":3}`,
			want:   &ToolPreviewGrepScan{FilesSearched: 4, SkippedBinary: 2, SkippedUnreadable: 1, SkippedDirectories: 3},
		},
		{
			name:   "zero skips stay zero",
			result: `{"shown":1,"output":"a.go","files_searched":7}`,
			want:   &ToolPreviewGrepScan{FilesSearched: 7},
		},
		{
			name:   "legacy result has no counters",
			result: `{"shown":0,"output":"No matches found"}`,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildToolPreview("grep", map[string]any{"output_mode": "content"}, tt.result)
			if tt.want == nil {
				if got.GrepScan != nil {
					t.Fatalf("GrepScan = %+v, want nil for a legacy result", *got.GrepScan)
				}
				return
			}
			if got.GrepScan == nil {
				t.Fatal("GrepScan = nil, want counters")
			}
			if *got.GrepScan != *tt.want {
				t.Fatalf("GrepScan = %+v, want %+v", *got.GrepScan, *tt.want)
			}
		})
	}
}
