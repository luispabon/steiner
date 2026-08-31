package builtin

import "strings"

// Result is a generic tool result.
type Result struct {
	Output     string `json:"output"`
	NextOffset int    `json:"next_offset,omitempty"`
}

// ImageBlock represents an image embedded in a tool result.
type ImageBlock struct {
	ID        string `json:"id,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
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
	Matches    int    `json:"matches,omitempty"`
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

// MoveResult describes a file move operation result.
type MoveResult struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// MutateAssertionResult captures a post-operation content assertion.
type MutateAssertionResult struct {
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Matches int    `json:"matches"`
}

// MutateContextResult captures a bounded post-operation text excerpt.
type MutateContextResult struct {
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// MutateOperationResult captures structured verification metadata for one operation.
type MutateOperationResult struct {
	Index        int                     `json:"index"`
	Type         string                  `json:"type"`
	Path         string                  `json:"path,omitempty"`
	ResolvedPath string                  `json:"resolved_path,omitempty"`
	From         string                  `json:"from,omitempty"`
	To           string                  `json:"to,omitempty"`
	MatchCount   int                     `json:"match_count,omitempty"`
	Applied      bool                    `json:"applied"`
	FileHash     string                  `json:"file_hash,omitempty"`
	Assertions   []MutateAssertionResult `json:"assertions,omitempty"`
	Context      *MutateContextResult    `json:"context,omitempty"`
}

// MutateResult is the result from a mutate tool call.
type MutateResult struct {
	Paths                []string                `json:"paths,omitempty"`
	Created              []string                `json:"created,omitempty"`
	Modified             []string                `json:"modified,omitempty"`
	Deleted              []string                `json:"deleted,omitempty"`
	Moved                []MoveResult            `json:"moved,omitempty"`
	FileHashes           map[string]string       `json:"file_hashes,omitempty"`
	OperationResults     []MutateOperationResult `json:"operation_results,omitempty"`
	OperationsApplied    int                     `json:"operations_applied"`
	OperationsFailed     int                     `json:"operations_failed,omitempty"`
	OperationsRolledBack int                     `json:"operations_rolled_back,omitempty"`
	OperationsSkipped    int                     `json:"operations_skipped,omitempty"`
	Output               string                  `json:"output,omitempty"`
}

// MutatedPaths returns the union of all mutated paths (created, modified, deleted, moved).
// It returns paths in deterministic order and is used by internal/agent to track
// file generation for read-staleness annotations.
func (r *MutateResult) MutatedPaths() []string {
	var paths []string
	for _, p := range r.Created {
		paths = appendUnique(paths, p)
	}
	for _, p := range r.Modified {
		paths = appendUnique(paths, p)
	}
	for _, p := range r.Deleted {
		paths = appendUnique(paths, p)
	}
	for _, m := range r.Moved {
		paths = appendUnique(paths, m.From)
		paths = appendUnique(paths, m.To)
	}
	return paths
}

func (r *MutateResult) clearCommittedMetadata() {
	r.Paths = []string{}
	r.Created = nil
	r.Modified = nil
	r.Deleted = nil
	r.Moved = nil
	r.FileHashes = nil
	r.OperationsApplied = 0
	for i := range r.OperationResults {
		r.OperationResults[i].Applied = false
		r.OperationResults[i].FileHash = ""
	}
}

// WasMutated reports whether mutate actually modified the filesystem.
func (r *MutateResult) WasMutated() bool {
	return r != nil && r.OperationsFailed == 0 && r.OperationsApplied > 0
}

// pageResults builds a Result from a sorted list of entry names with pagination.
func pageResults(allLines []string, limit, offset int) Result {
	total := len(allLines)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := allLines[start:end]

	result := Result{
		Output: strings.Join(page, "\n"),
	}

	if end < total {
		result.NextOffset = offset + limit
	}

	return result
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
