package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestBuildFetchURLLines(t *testing.T) {
	t.Parallel()
	b := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	t.Run("markdown response shows status header", func(t *testing.T) {
		t.Parallel()
		tc := &toolCallSegment{
			tool:     "fetch_url",
			bodyKind: "fetch_url",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindFetchURL,
				Path:             "https://example.com",
				Language:         "markdown",
				Contents:         "# Hello\nWorld",
				StatusCode:       200,
				ContentLength:    12,
				FetchTitle:       "Example",
				FetchDescription: "A test page",
			},
			rawArgs: map[string]any{"max_size": float64(500000)},
		}
		lines := b.buildFetchURLLines(tc, 80)
		joined := strings.Join(lines, "\n")

		if !strings.Contains(joined, "https://example.com") {
			t.Errorf("missing URL in output: %q", joined)
		}
		if !strings.Contains(joined, "200") {
			t.Errorf("missing status code in output: %q", joined)
		}
		if !strings.Contains(joined, "Example") {
			t.Errorf("missing title in output: %q", joined)
		}
		if !strings.Contains(joined, "Hello") {
			t.Errorf("missing body content in output: %q", joined)
		}
	})

	t.Run("image response shows placeholder", func(t *testing.T) {
		t.Parallel()
		tc := &toolCallSegment{
			tool:     "fetch_url",
			bodyKind: "fetch_url",
			preview: output.ToolPreview{
				Kind:       output.ToolPreviewKindFetchURL,
				Path:       "https://example.com/photo.png",
				Language:   "image",
				Contents:   "[image: 2x2 png 84B]",
				StatusCode: 200,
			},
		}
		lines := b.buildFetchURLLines(tc, 80)
		joined := strings.Join(lines, "\n")

		if !strings.Contains(joined, "[image: 2x2 png 84B]") {
			t.Errorf("missing image placeholder in output: %q", joined)
		}
		if !strings.Contains(joined, "image returned to model") {
			t.Errorf("missing footer in output: %q", joined)
		}
		if !strings.Contains(joined, "200") {
			t.Errorf("missing status code in output: %q", joined)
		}
	})
}

func TestBuildGrepFileLines_NoMatchesSentinel(t *testing.T) {
	b := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	t.Run("strips No matches found sentinel", func(t *testing.T) {
		tc := &toolCallSegment{
			tool:     "grep",
			bodyKind: "grep",
			preview: output.ToolPreview{
				Kind:       "grep",
				OutputMode: "files_with_matches",
				Path:       "/tmp",
				Returned:   0,
				GrepFiles: []output.ToolPreviewGrepFile{
					{Path: "No matches found"},
				},
			},
		}

		lines := b.buildGrepFileLines(tc)
		joined := strings.Join(lines, "\n")

		if strings.Contains(joined, "No matches found") {
			t.Error("sentinel should be stripped from output")
		}
		if !strings.Contains(joined, "no matches found") {
			t.Errorf("should show 'no matches found' message, got: %q", joined)
		}
	})

	t.Run("preserves real grep files", func(t *testing.T) {
		tc := &toolCallSegment{
			tool:     "grep",
			bodyKind: "grep",
			preview: output.ToolPreview{
				Kind:       "grep",
				OutputMode: "files_with_matches",
				Path:       "/tmp",
				Returned:   2,
				GrepFiles: []output.ToolPreviewGrepFile{
					{Path: "file1.go"},
					{Path: "file2.go"},
				},
			},
		}

		lines := b.buildGrepFileLines(tc)
		joined := strings.Join(lines, "\n")

		if !strings.Contains(joined, "file1.go") {
			t.Error("file1.go should be in output")
		}
		if !strings.Contains(joined, "file2.go") {
			t.Error("file2.go should be in output")
		}
	})
}

func TestBuildGrepCountLines_NoMatchesSentinel(t *testing.T) {
	b := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}

	t.Run("strips No matches found sentinel", func(t *testing.T) {
		tc := &toolCallSegment{
			tool:     "grep",
			bodyKind: "grep",
			preview: output.ToolPreview{
				Kind:       "grep",
				OutputMode: "count",
				Path:       "/tmp",
				Returned:   0,
				GrepFiles: []output.ToolPreviewGrepFile{
					{Path: "No matches found", Count: 0},
				},
			},
		}

		lines := b.buildGrepCountLines(tc)
		joined := strings.Join(lines, "\n")

		if strings.Contains(joined, "No matches found") {
			t.Error("sentinel should be stripped from output")
		}
		if !strings.Contains(joined, "no matches found") {
			t.Errorf("should show 'no matches found' message, got: %q", joined)
		}
	})

	t.Run("preserves real grep counts", func(t *testing.T) {
		tc := &toolCallSegment{
			tool:     "grep",
			bodyKind: "grep",
			preview: output.ToolPreview{
				Kind:       "grep",
				OutputMode: "count",
				Path:       "/tmp",
				Returned:   2,
				GrepFiles: []output.ToolPreviewGrepFile{
					{Path: "file1.go", Count: 5},
					{Path: "file2.go", Count: 3},
				},
			},
		}

		lines := b.buildGrepCountLines(tc)
		joined := strings.Join(lines, "\n")

		if !strings.Contains(joined, "file1.go:5") {
			t.Error("file1.go:5 should be in output")
		}
		if !strings.Contains(joined, "file2.go:3") {
			t.Error("file2.go:3 should be in output")
		}
	})
}
