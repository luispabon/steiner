package builtin

import (
	"strings"

	"github.com/deepnoodle-ai/dive"
	"github.com/luispabon/steiner/internal/tool/builtin/patchdoc"
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
	Truncated  bool   `json:"truncated,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
	Output     string `json:"output"`
}

// MutationResult is the result from a write or edit tool call.
type MutationResult struct {
	Path    string `json:"path"`
	Output  string `json:"output"`
	Mutated bool   `json:"mutated"`
}

// WasMutated reports whether the mutation actually modified the file.
func (r *MutationResult) WasMutated() bool {
	return r.Mutated
}

// ApplyPatchResult is the result from an apply_patch tool call.
type ApplyPatchResult struct {
	Paths        []string     `json:"paths"`
	Added        []string     `json:"added,omitempty"`
	Modified     []string     `json:"modified,omitempty"`
	Deleted      []string     `json:"deleted,omitempty"`
	Moved        []MoveResult `json:"moved,omitempty"`
	DryRun       bool         `json:"dry_run,omitempty"`
	HunksApplied int          `json:"hunks_applied"`
	HunksFailed  int          `json:"hunks_failed,omitempty"`
	Output       string       `json:"output"`
}

// MoveResult describes a file move in the apply_patch result.
type MoveResult struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// WasMutated reports whether apply_patch actually modified the file.
func (r *ApplyPatchResult) WasMutated() bool {
	return r != nil && !r.DryRun && r.HunksFailed == 0 && (r.HunksApplied > 0 || len(r.Paths) > 0)
}

func newApplyPatchResult(result patchdoc.ApplyResult) *ApplyPatchResult {
	output := summarizeApplyPatchResult(result)
	paths := make([]string, 0, len(result.Added)+len(result.Modified)+len(result.Deleted)+len(result.Moved))
	paths = append(paths, result.Added...)
	paths = append(paths, result.Modified...)
	paths = append(paths, result.Deleted...)
	for _, moved := range result.Moved {
		paths = append(paths, moved.To)
	}

	return &ApplyPatchResult{
		Paths:        paths,
		Added:        append([]string(nil), result.Added...),
		Modified:     append([]string(nil), result.Modified...),
		Deleted:      append([]string(nil), result.Deleted...),
		Moved:        copyMoveResults(result.Moved),
		DryRun:       result.DryRun,
		HunksApplied: len(result.Added) + len(result.Modified) + len(result.Deleted) + len(result.Moved),
		Output:       output,
	}
}

func summarizeApplyPatchResult(result patchdoc.ApplyResult) string {
	prefix := "Success."
	if result.DryRun {
		prefix = "Dry run succeeded."
	}

	lines := []string{prefix, "Updated the following files:"}
	for _, path := range result.Added {
		lines = append(lines, "A "+path)
	}
	for _, path := range result.Modified {
		lines = append(lines, "M "+path)
	}
	for _, path := range result.Deleted {
		lines = append(lines, "D "+path)
	}
	for _, moved := range result.Moved {
		lines = append(lines, "R "+moved.From+" -> "+moved.To)
	}
	return strings.Join(lines, "\n")
}

func copyMoveResults(moves []patchdoc.MoveResult) []MoveResult {
	if len(moves) == 0 {
		return nil
	}
	copied := make([]MoveResult, 0, len(moves))
	for _, move := range moves {
		copied = append(copied, MoveResult{From: move.From, To: move.To})
	}
	return copied
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
