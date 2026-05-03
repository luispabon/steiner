package builtin

import (
	"strings"

	"github.com/deepnoodle-ai/dive"
)

// Result is a generic tool result.
type Result struct {
	Output     string `json:"output"`
	Returned   int    `json:"returned"`
	Error      string `json:"error,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
}

// ReadResult is the result from a read tool call.
type ReadResult struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	NextOffset int    `json:"next_offset,omitempty"`
	Output     string `json:"output"`
}

// GrepResult is the result from a grep tool call.
type GrepResult struct {
	Matches    int    `json:"matches"`
	Returned   int    `json:"returned"`
	NextOffset int    `json:"next_offset,omitempty"`
	Output     string `json:"output"`
}

// MutationResult is the result from a write or edit tool call.
type MutationResult struct {
	Path   string `json:"path"`
	Output string `json:"output"`
}

// ApplyPatchResult is the result from an apply_patch tool call.
type ApplyPatchResult struct {
	Path         string `json:"path"`
	HunksApplied int    `json:"hunks_applied"`
	HunksFailed  int    `json:"hunks_failed,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	Output       string `json:"output"`
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
