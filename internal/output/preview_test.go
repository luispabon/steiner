package output

import "testing"

func TestBuildToolPreview(t *testing.T) {
	writeExisted := true
	writeMissing := false

	tests := []struct {
		name    string
		tool    string
		args    map[string]any
		result  string
		existed *bool
		want    ToolPreview
	}{
		{
			name: "edit diff",
			tool: "edit",
			args: map[string]any{
				"path": "internal/tui/content.go",
				"old":  "before()",
				"new":  "after()",
			},
			want: ToolPreview{
				Kind:     ToolPreviewKindEditDiff,
				Path:     "internal/tui/content.go",
				Language: "go",
				Before:   "before()",
				After:    "after()",
			},
		},
		{
			name: "write created",
			tool: "write",
			args: map[string]any{
				"path":     "notes.md",
				"contents": "# hi\n",
			},
			existed: &writeMissing,
			want: ToolPreview{
				Kind:     ToolPreviewKindFileWrite,
				Path:     "notes.md",
				Language: "markdown",
				Contents: "# hi\n",
				Created:  true,
			},
		},
		{
			name: "write updated",
			tool: "write",
			args: map[string]any{
				"path":     "notes.md",
				"contents": "# hi\n",
			},
			existed: &writeExisted,
			want: ToolPreview{
				Kind:     ToolPreviewKindFileWrite,
				Path:     "notes.md",
				Language: "markdown",
				Contents: "# hi\n",
			},
		},
		{
			name: "read file",
			tool: "read",
			args: map[string]any{
				"path": "README.md",
			},
			result: `{"path":"README.md","contents":"hello\nworld\n"}`,
			want: ToolPreview{
				Kind:     ToolPreviewKindReadFile,
				Path:     "README.md",
				Language: "markdown",
				Contents: "hello\nworld\n",
			},
		},
		{
			name: "missing critical data falls back to plain",
			tool: "edit",
			args: map[string]any{
				"path": "x.go",
				"old":  "before()",
			},
			want: ToolPreview{Kind: ToolPreviewKindPlain},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildToolPreview(tt.tool, tt.args, tt.result, tt.existed)
			if got != tt.want {
				t.Fatalf("BuildToolPreview() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
