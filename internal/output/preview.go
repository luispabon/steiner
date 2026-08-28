package output

import "strings"

const (
	// ToolPreviewKindReadFile is a read-file preview.
	ToolPreviewKindReadFile = "read_file"
	// ToolPreviewKindGlobList is a glob results preview.
	ToolPreviewKindGlobList = "glob_list"
	// ToolPreviewKindLSList is a directory listing preview.
	ToolPreviewKindLSList = "ls_list"
	// ToolPreviewKindGrep is a grep results preview.
	ToolPreviewKindGrep = "grep"
	// ToolPreviewKindBash is a shell command preview.
	ToolPreviewKindBash = "bash"
	// ToolPreviewKindMutate is a mutate tool result preview.
	ToolPreviewKindMutate = "mutate"
	// ToolPreviewKindPlain is a fallback preview with no structured rendering.
	ToolPreviewKindPlain = "plain"
	// ToolPreviewKindFetchURL is a fetch_url tool result preview.
	ToolPreviewKindFetchURL = "fetch_url"
	// ToolPreviewKindWebSearch is a web_search tool result preview.
	ToolPreviewKindWebSearch = "web_search"
)

// ToolPreviewListEntry is one filesystem entry in a list-style preview.
type ToolPreviewListEntry struct {
	Path  string
	IsDir bool
}

// ToolPreviewPatchMove describes a renamed file in a patch result.
type ToolPreviewPatchMove struct {
	From string
	To   string
}

// ToolPreviewMutateOperation carries one operation for mutate rendering.
type ToolPreviewMutateOperation struct {
	Type      string
	Path      string
	From      string
	To        string
	Content   string
	OldString string
	NewString string
}

// ToolPreviewGrepMatch is one matching line in a grep preview.
type ToolPreviewGrepMatch struct {
	LineNumber int
	Text       string
}

// ToolPreviewGrepFile groups grep matches by file.
type ToolPreviewGrepFile struct {
	Path    string
	Count   int
	Matches []ToolPreviewGrepMatch
}

// ToolPreview is the normalized preview payload for tool results.
type ToolPreview struct {
	Kind             string
	Path             string
	Language         string
	Before           string
	After            string
	Contents         string
	Created          bool
	Command          string
	Output           string
	Message          string
	ExitCode         int
	Truncated        bool
	HasMore          bool
	Returned         int
	NextOffset       int
	OutputMode       string
	StartLine        int
	StatusCode       int
	ContentLength    int
	FetchTitle       string
	FetchDescription string
	Entries          []ToolPreviewListEntry
	GrepFiles        []ToolPreviewGrepFile
	PatchAdded       []string
	PatchModified    []string
	PatchDeleted     []string
	PatchMoved       []ToolPreviewPatchMove
	HunksApplied     int
	HunksFailed      int
	MutateOperations []ToolPreviewMutateOperation
}

// BuildToolPreview builds a structured preview for a tool result when supported.
func BuildToolPreview(tool string, arguments map[string]any, result string) ToolPreview {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "read", "read_file":
		return buildReadPreview(arguments, result)
	case "glob":
		return buildGlobPreview(arguments, result)
	case "ls":
		return buildLSPreview(arguments, result)
	case "grep":
		return buildGrepPreview(arguments, result)
	case "bash":
		return buildBashPreview(arguments, result)
	case "mutate":
		return buildMutatePreview(arguments, result)
	case "fetch_url":
		return buildFetchURLPreview(result)
	case "web_search":
		return buildWebSearchPreview(result)
	default:
		return plainToolPreview()
	}
}
