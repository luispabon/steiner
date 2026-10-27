package output

import "slices"

// sanitizeToolPreview returns p with every display string passed through
// SanitizeDisplayText. Slices are copied so the caller's preview is unchanged.
func sanitizeToolPreview(p ToolPreview) ToolPreview {
	p.Path = SanitizeDisplayText(p.Path)
	p.Language = SanitizeDisplayText(p.Language)
	p.Before = SanitizeDisplayText(p.Before)
	p.After = SanitizeDisplayText(p.After)
	p.Contents = SanitizeDisplayText(p.Contents)
	p.Command = SanitizeDisplayText(p.Command)
	p.Output = SanitizeDisplayText(p.Output)
	p.FetchTitle = SanitizeDisplayText(p.FetchTitle)
	p.FetchDescription = SanitizeDisplayText(p.FetchDescription)
	p.Entries = slices.Clone(p.Entries)
	for i := range p.Entries {
		p.Entries[i].Path = SanitizeDisplayText(p.Entries[i].Path)
	}
	p.GrepFiles = sanitizeGrepFiles(p.GrepFiles)
	p.PatchAdded = sanitizeStrings(p.PatchAdded)
	p.PatchModified = sanitizeStrings(p.PatchModified)
	p.PatchDeleted = sanitizeStrings(p.PatchDeleted)
	p.PatchMoved = slices.Clone(p.PatchMoved)
	for i := range p.PatchMoved {
		p.PatchMoved[i].From = SanitizeDisplayText(p.PatchMoved[i].From)
		p.PatchMoved[i].To = SanitizeDisplayText(p.PatchMoved[i].To)
	}
	p.MutateOperations = sanitizeMutateOperations(p.MutateOperations)
	return p
}

func sanitizeGrepFiles(files []ToolPreviewGrepFile) []ToolPreviewGrepFile {
	files = slices.Clone(files)
	for i := range files {
		files[i].Path = SanitizeDisplayText(files[i].Path)
		files[i].Matches = slices.Clone(files[i].Matches)
		for j := range files[i].Matches {
			files[i].Matches[j].Text = SanitizeDisplayText(files[i].Matches[j].Text)
		}
	}
	return files
}

func sanitizeMutateOperations(ops []ToolPreviewMutateOperation) []ToolPreviewMutateOperation {
	ops = slices.Clone(ops)
	for i := range ops {
		ops[i].Path = SanitizeDisplayText(ops[i].Path)
		ops[i].From = SanitizeDisplayText(ops[i].From)
		ops[i].To = SanitizeDisplayText(ops[i].To)
		ops[i].Content = SanitizeDisplayText(ops[i].Content)
		ops[i].OldString = SanitizeDisplayText(ops[i].OldString)
		ops[i].NewString = SanitizeDisplayText(ops[i].NewString)
	}
	return ops
}

func sanitizeStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = SanitizeDisplayText(v)
	}
	return out
}
