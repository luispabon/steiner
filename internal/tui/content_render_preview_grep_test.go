package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestBuildGrepContentLines_MatchCountExcludesContext(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	tc := &toolCallSegment{
		tool:     "grep",
		bodyKind: "grep",
		preview: output.ToolPreview{
			Kind:       "grep",
			OutputMode: "content",
			Path:       "/tmp",
			Matches:    2,
			NextOffset: 3,
			GrepFiles: []output.ToolPreviewGrepFile{
				{Path: "a.go", Matches: []output.ToolPreviewGrepMatch{
					{LineNumber: 1, Text: "match one"},
					{LineNumber: 2, Text: "context"},
					{LineNumber: 3, Text: "match two"},
				}},
			},
		},
	}

	joined := strings.Join(b.buildGrepContentLines(tc), "\n")
	if !strings.Contains(joined, "2 matches") {
		t.Errorf("summary should count 2 matches, got: %q", joined)
	}
	if strings.Contains(joined, "3 matches") {
		t.Errorf("summary should not count the context line, got: %q", joined)
	}
	if !strings.Contains(joined, "more available") {
		t.Errorf("summary should show more available, got: %q", joined)
	}
}

func TestBuildGrepCountLines_MatchCountSumsFileCounts(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	tc := &toolCallSegment{
		tool:     "grep",
		bodyKind: "grep",
		preview: output.ToolPreview{
			Kind:       "grep",
			OutputMode: "count",
			Path:       "/tmp",
			Returned:   2,
			Matches:    8,
			GrepFiles: []output.ToolPreviewGrepFile{
				{Path: "file1.go", Count: 5},
				{Path: "file2.go", Count: 3},
			},
		},
	}

	joined := strings.Join(b.buildGrepCountLines(tc), "\n")
	if !strings.Contains(joined, "8 matches") {
		t.Errorf("summary should count 8 matches, got: %q", joined)
	}
}
