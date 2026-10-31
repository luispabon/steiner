package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestBuildGrepContentLines_ScanSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		preview   output.ToolPreview
		want      []string
		notInText []string
	}{
		{
			name: "skipped entries mark the result partial",
			preview: output.ToolPreview{
				Matches: 1,
				GrepScan: &output.ToolPreviewGrepScan{
					FilesSearched:     3,
					SkippedBinary:     1,
					SkippedUnreadable: 2,
				},
				GrepFiles: []output.ToolPreviewGrepFile{
					{Path: "a.go", Matches: []output.ToolPreviewGrepMatch{{LineNumber: 1, Text: "match"}}},
				},
			},
			want:      []string{"3 files searched", "partial: 1 binary, 2 unreadable skipped"},
			notInText: []string{"no candidate files searched"},
		},
		{
			name: "unreadable directories are reported",
			preview: output.ToolPreview{
				GrepScan: &output.ToolPreviewGrepScan{FilesSearched: 1, SkippedDirectories: 2},
				GrepFiles: []output.ToolPreviewGrepFile{
					{Path: "a.go", Matches: []output.ToolPreviewGrepMatch{{LineNumber: 1, Text: "match"}}},
				},
			},
			want: []string{"1 files searched", "partial: 2 unreadable directories skipped"},
		},
		{
			name: "no candidates is not no matches",
			preview: output.ToolPreview{
				GrepScan: &output.ToolPreviewGrepScan{FilesSearched: 0, SkippedBinary: 2},
			},
			want:      []string{"no candidate files searched", "0 files searched"},
			notInText: []string{"no matches found", "no results at this offset"},
		},
		{
			name: "exhausted offset still reports the offset",
			preview: output.ToolPreview{
				TotalMatches: 12,
				GrepScan:     &output.ToolPreviewGrepScan{FilesSearched: 5, SkippedBinary: 1},
			},
			want:      []string{"no results at this offset", "5 files searched"},
			notInText: []string{"no candidate files searched", "no matches found"},
		},
		{
			name: "legacy result keeps the old messages",
			preview: output.ToolPreview{
				OutputMode: "content",
			},
			want:      []string{"no matches found"},
			notInText: []string{"files searched", "partial:", "no candidate files searched"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			preview := tt.preview
			preview.Kind = "grep"
			preview.OutputMode = "content"
			preview.Path = "/tmp"
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			joined := strings.Join(b.buildGrepContentLines(&toolCallSegment{
				tool:     "grep",
				bodyKind: "grep",
				preview:  preview,
			}), "\n")

			for _, want := range tt.want {
				if !strings.Contains(joined, want) {
					t.Errorf("should show %q, got: %q", want, joined)
				}
			}
			for _, absent := range tt.notInText {
				if strings.Contains(joined, absent) {
					t.Errorf("should not show %q, got: %q", absent, joined)
				}
			}
		})
	}
}

func TestBuildGrepFileLines_PartialScan(t *testing.T) {
	t.Parallel()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	tc := &toolCallSegment{
		tool:     "grep",
		bodyKind: "grep",
		preview: output.ToolPreview{
			Kind:       "grep",
			OutputMode: "files_with_matches",
			Path:       "/tmp",
			Returned:   1,
			GrepScan:   &output.ToolPreviewGrepScan{FilesSearched: 4, SkippedBinary: 1},
			GrepFiles:  []output.ToolPreviewGrepFile{{Path: "a.go"}},
		},
	}

	joined := strings.Join(b.buildGrepFileLines(tc), "\n")
	if !strings.Contains(joined, "4 files searched") {
		t.Errorf("summary should count searched files, got: %q", joined)
	}
	if !strings.Contains(joined, "partial: 1 binary skipped") {
		t.Errorf("summary should mark the result partial, got: %q", joined)
	}
}
