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

// ImageBlock represents an image embedded in a tool result.
type ImageBlock struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SizeBytes int    `json:"size_bytes"`
}

// ReadResult is the result from a read tool call.
type ReadResult struct {
	Path       string      `json:"path"`
	StartLine  int         `json:"start_line"`
	EndLine    int         `json:"end_line"`
	TotalLines int         `json:"total_lines"`
	FileHash   string      `json:"file_hash"`
	NextOffset int         `json:"next_offset,omitempty"`
	Output     string      `json:"output"`
	Image      *ImageBlock `json:"image,omitempty"`
}

// GrepResult is the result from a grep tool call.
type GrepResult struct {
	Matches    int               `json:"matches"`
	Returned   int               `json:"returned"`
	Truncated  bool              `json:"truncated,omitempty"`
	HasMore    bool              `json:"has_more,omitempty"`
	NextOffset int               `json:"next_offset,omitempty"`
	FileHashes map[string]string `json:"file_hashes,omitempty"`
	Output     string            `json:"output"`
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

// MoveResult describes a file move operation result.
type MoveResult struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// MutateResult is the result from a mutate tool call.
type MutateResult struct {
	Paths             []string     `json:"paths"`
	Created           []string     `json:"created,omitempty"`
	Modified          []string     `json:"modified,omitempty"`
	Deleted           []string     `json:"deleted,omitempty"`
	Moved             []MoveResult `json:"moved,omitempty"`
	DryRun            bool         `json:"dry_run,omitempty"`
	OperationsApplied int          `json:"operations_applied"`
	OperationsFailed  int          `json:"operations_failed,omitempty"`
	OperationsSkipped int          `json:"operations_skipped,omitempty"`
	Output            string       `json:"output"`
}

func (r *MutateResult) clearCommittedMetadata() {
	r.Paths = []string{}
	r.Created = nil
	r.Modified = nil
	r.Deleted = nil
	r.Moved = nil
	r.OperationsApplied = 0
}

// WasMutated reports whether mutate actually modified the filesystem.
func (r *MutateResult) WasMutated() bool {
	return r != nil && !r.DryRun && r.OperationsFailed == 0 && r.OperationsApplied > 0
}

// WorkflowHandoffResult is the result from a workflow_handoff tool call.
type WorkflowHandoffResult struct {
	Next             string `json:"next"`
	Target           string `json:"target"`
	Message          string `json:"message,omitempty"`
	MessageTruncated bool   `json:"message_truncated,omitempty"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
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
