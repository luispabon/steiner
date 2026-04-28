package builtin

// Result is a generic tool result.
type Result struct {
	Output     string `json:"output"`
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
