package builtin

import (
	"strings"

	"github.com/deepnoodle-ai/dive"
)

// Result is a generic tool result.
type Result struct {
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	NextOffset int    `json:"nextOffset,omitempty"`
}

// ReadResult is the result from a read tool call.
type ReadResult struct {
	Path       string `json:"path"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	TotalLines int    `json:"totalLines"`
	NextOffset int    `json:"nextOffset,omitempty"`
	Output     string `json:"output"`
}

// GrepResult is the result from a grep tool call.
type GrepResult struct {
	Matches    int    `json:"matches"`
	Returned   int    `json:"returned"`
	NextOffset int    `json:"nextOffset,omitempty"`
	Output     string `json:"output"`
}

// MutationResult is the result from a write or edit tool call.
type MutationResult struct {
	Path   string `json:"path"`
	Output string `json:"output"`
}

// diveText flattens a Dive ToolResult into a single text string by combining
// the Display field and all Content[].Text fields.
func diveText(res *dive.ToolResult) string {
	var b strings.Builder
	if res.Display != "" {
		b.WriteString(res.Display)
	}
	for _, c := range res.Content {
		if c.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(c.Text)
		}
	}
	return b.String()
}
