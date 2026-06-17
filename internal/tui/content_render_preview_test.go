package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestBuildMutateLinesInsertBefore(t *testing.T) {
	b := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}
	tc := &toolCallSegment{
		tool:     "mutate",
		bodyKind: "mutate",
		preview: output.ToolPreview{
			Kind: output.ToolPreviewKindMutate,
			MutateOperations: []output.ToolPreviewMutateOperation{
				{
					Type:      "insert_before",
					Path:      "main.go",
					Line:      10,
					NewString: "import \"fmt\"",
				},
			},
		},
	}
	lines := b.buildMutateLines(tc, 80)
	joined := strings.Join(lines, "\n")

	// Check badge character
	if !strings.Contains(joined, "I") {
		t.Errorf("missing I badge in output: %q", joined)
	}
	// Check path:line format
	if !strings.Contains(joined, "main.go:10") {
		t.Errorf("missing path:line in output: %q", joined)
	}
	// Check content with + prefix
	if !strings.Contains(joined, "+") {
		t.Errorf("missing + prefix in output: %q", joined)
	}
	if !strings.Contains(joined, "import") {
		t.Errorf("missing inserted content in output: %q", joined)
	}
}

func TestBuildMutateLinesInsertAfter(t *testing.T) {
	b := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}
	tc := &toolCallSegment{
		tool:     "mutate",
		bodyKind: "mutate",
		preview: output.ToolPreview{
			Kind: output.ToolPreviewKindMutate,
			MutateOperations: []output.ToolPreviewMutateOperation{
				{
					Type:      "insert_after",
					Path:      "config.go",
					Line:      25,
					NewString: "line1\nline2",
				},
			},
		},
	}
	lines := b.buildMutateLines(tc, 80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "I") {
		t.Errorf("missing I badge in output: %q", joined)
	}
	if !strings.Contains(joined, "config.go:25") {
		t.Errorf("missing path:line in output: %q", joined)
	}
	// Multi-line content should have both lines
	if !strings.Contains(joined, "line1") || !strings.Contains(joined, "line2") {
		t.Errorf("missing multi-line content in output: %q", joined)
	}
}

func TestBuildFetchURLLines(t *testing.T) {
	b := &contentBuffer{
		styles: theme.BuildStyles(theme.AccentAmber),
	}

	t.Run("markdown response shows status header", func(t *testing.T) {
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
