package output

import (
	"encoding/json"
	"testing"
)

// requireNoControls fails the test when s contains a C0, DEL, or C1 control
// other than tab and newline.
func requireNoControls(t *testing.T, label, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s = %q, contains control %U", label, s, r)
			return
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(data)
}

func TestSanitizeToolPreviewCopiesAndCleansAllStrings(t *testing.T) {
	t.Parallel()

	const raw = "\x1b[31mX\x1b[0m\r"
	in := ToolPreview{
		Path:             raw,
		Language:         raw,
		Before:           raw,
		After:            raw,
		Contents:         raw,
		Command:          raw,
		Output:           raw,
		FetchTitle:       raw,
		FetchDescription: raw,
		Entries:          []ToolPreviewListEntry{{Path: raw}},
		GrepFiles:        []ToolPreviewGrepFile{{Path: raw, Matches: []ToolPreviewGrepMatch{{Text: raw}}}},
		PatchAdded:       []string{raw},
		PatchModified:    []string{raw},
		PatchDeleted:     []string{raw},
		PatchMoved:       []ToolPreviewPatchMove{{From: raw, To: raw}},
		MutateOperations: []ToolPreviewMutateOperation{{Path: raw, From: raw, To: raw, Content: raw, OldString: raw, NewString: raw}},
	}

	got := sanitizeToolPreview(in)
	fields := map[string]string{
		"Path": got.Path, "Language": got.Language, "Before": got.Before, "After": got.After,
		"Contents": got.Contents, "Command": got.Command, "Output": got.Output,
		"FetchTitle": got.FetchTitle, "FetchDescription": got.FetchDescription,
		"Entries[0].Path":              got.Entries[0].Path,
		"GrepFiles[0].Path":            got.GrepFiles[0].Path,
		"GrepFiles[0].Matches[0].Text": got.GrepFiles[0].Matches[0].Text,
		"PatchAdded[0]":                got.PatchAdded[0], "PatchModified[0]": got.PatchModified[0], "PatchDeleted[0]": got.PatchDeleted[0],
		"PatchMoved[0].From": got.PatchMoved[0].From, "PatchMoved[0].To": got.PatchMoved[0].To,
		"MutateOperations[0].Path":      got.MutateOperations[0].Path,
		"MutateOperations[0].From":      got.MutateOperations[0].From,
		"MutateOperations[0].To":        got.MutateOperations[0].To,
		"MutateOperations[0].Content":   got.MutateOperations[0].Content,
		"MutateOperations[0].OldString": got.MutateOperations[0].OldString,
		"MutateOperations[0].NewString": got.MutateOperations[0].NewString,
	}
	for name, value := range fields {
		if value != "X" {
			t.Errorf("%s = %q, want \"X\"", name, value)
		}
	}
	if in.Path != raw || in.Entries[0].Path != raw || in.GrepFiles[0].Matches[0].Text != raw ||
		in.PatchAdded[0] != raw || in.PatchMoved[0].To != raw || in.MutateOperations[0].NewString != raw {
		t.Error("sanitizeToolPreview modified the input preview")
	}
}

func TestBuildToolPreviewSanitizesBashAndRead(t *testing.T) {
	t.Parallel()

	bash := BuildToolPreview("bash", map[string]any{"command": "echo \x1b[2J hi"},
		mustJSON(t, map[string]any{"exit_code": 0, "output": "ok\x1b[31m red\r\n\x1b]0;x\x07done"}))
	if bash.Command != "echo  hi" {
		t.Errorf("bash Command = %q, want %q", bash.Command, "echo  hi")
	}
	if bash.Output != "ok red\ndone" {
		t.Errorf("bash Output = %q, want %q", bash.Output, "ok red\ndone")
	}

	read := BuildToolPreview("read", map[string]any{"path": "a\x1b[2J.go"},
		mustJSON(t, map[string]any{"path": "a.go", "output": "1\x1b[31mx\x1b[0m\n2\x1b]0;t\x07y"}))
	if read.Path != "a.go" {
		t.Errorf("read Path = %q, want %q", read.Path, "a.go")
	}
	if read.Contents != "1x\n2y" {
		t.Errorf("read Contents = %q, want %q", read.Contents, "1x\n2y")
	}
}

func TestFormatPreviewsSanitizePathAndText(t *testing.T) {
	t.Parallel()

	const path = "f\x1b]0;t\x07.go"
	docs := map[string]PreviewDocument{
		"file":      FormatFilePreview(path, "a\x1b[31mb\x1b[0m\r\nc\n"),
		"edit diff": FormatEditDiffPreview(path, "a\x1b[31m\n", "b\r\n"),
	}
	for name, doc := range docs {
		if doc.Path != "f.go" {
			t.Errorf("%s Path = %q, want %q", name, doc.Path, "f.go")
		}
		if len(doc.Lines) == 0 {
			t.Errorf("%s Lines is empty", name)
		}
		for _, line := range doc.Lines {
			requireNoControls(t, name+" prefix", line.Prefix)
			for _, span := range line.Spans {
				requireNoControls(t, name+" span", span.Text)
			}
		}
	}
}
