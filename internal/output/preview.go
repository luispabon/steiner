package output

import "strings"

const (
	ToolPreviewKindEditDiff  = "edit_diff"
	ToolPreviewKindFileWrite = "file_write"
	ToolPreviewKindReadFile  = "read_file"
	ToolPreviewKindGlobList  = "glob_list"
	ToolPreviewKindLSList    = "ls_list"
	ToolPreviewKindGrep      = "grep"
	ToolPreviewKindBash      = "bash"
	ToolPreviewKindPatch     = "patch"
	ToolPreviewKindPlain     = "plain"
)

type ToolPreviewListEntry struct {
	Path  string
	IsDir bool
}

// ToolPreviewPatchMove describes a renamed file in a patch result.
type ToolPreviewPatchMove struct {
	From string
	To   string
}

type ToolPreviewGrepMatch struct {
	LineNumber int
	Text       string
}

type ToolPreviewGrepFile struct {
	Path    string
	Count   int
	Matches []ToolPreviewGrepMatch
}

type ToolPreview struct {
	Kind          string
	Path          string
	Language      string
	Before        string
	After         string
	Contents      string
	Created       bool
	Command       string
	Output        string
	Message       string
	ExitCode      int
	Truncated     bool
	HasMore       bool
	Returned      int
	NextOffset    int
	OutputMode    string
	Entries       []ToolPreviewListEntry
	GrepFiles     []ToolPreviewGrepFile
	PatchAdded    []string
	PatchModified []string
	PatchDeleted  []string
	PatchMoved    []ToolPreviewPatchMove
	HunksApplied  int
	HunksFailed   int
}

func BuildToolPreview(tool string, arguments map[string]any, result string, writeTargetExistedBefore *bool) ToolPreview {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "edit":
		return buildEditPreview(arguments)
	case "write", "write_file":
		return buildWritePreview(arguments, writeTargetExistedBefore)
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
	case "apply_patch":
		return buildApplyPatchPreview(result)
	default:
		return plainToolPreview()
	}
}
