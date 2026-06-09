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
