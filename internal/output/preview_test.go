package output

import (
	"reflect"
	"testing"
)

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
				"path":       "internal/tui/content.go",
				"old_string": "before()",
				"new_string": "after()",
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
				"path":    "notes.md",
				"content": "# hi\n",
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
				"path":    "notes.md",
				"content": "# hi\n",
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
			result: `{"path":"README.md","output":"hello\nworld\n"}`,
			want: ToolPreview{
				Kind:     ToolPreviewKindReadFile,
				Path:     "README.md",
				Language: "markdown",
				Contents: "hello\nworld\n",
			},
		},
		{
			name: "read file strips numbered preview lines",
			tool: "read",
			args: map[string]any{
				"path": "README.md",
			},
			result: `{"path":"README.md","start_line":1,"output":"1 # Heading\n2\n3 Body line\n"}`,
			want: ToolPreview{
				Kind:     ToolPreviewKindReadFile,
				Path:     "README.md",
				Language: "markdown",
				Contents: "# Heading\n\nBody line\n",
			},
		},
		{
			name: "glob list",
			tool: "glob",
			args: map[string]any{
				"path":    "src",
				"pattern": "**/*.go",
			},
			result: `{"output":"main.go\npkg/tool.go\n","returned":2,"next_offset":3}`,
			want: ToolPreview{
				Kind:       ToolPreviewKindGlobList,
				Path:       "src",
				Returned:   2,
				NextOffset: 3,
				Entries: []ToolPreviewListEntry{
					{Path: "main.go"},
					{Path: "pkg/tool.go"},
				},
			},
		},
		{
			name: "ls list",
			tool: "ls",
			args: map[string]any{
				"path": "src",
			},
			result: `{"output":"cmd/\nmain.go\n","returned":2}`,
			want: ToolPreview{
				Kind:     ToolPreviewKindLSList,
				Path:     "src",
				Returned: 2,
				Entries: []ToolPreviewListEntry{
					{Path: "cmd", IsDir: true},
					{Path: "main.go"},
				},
			},
		},
		{
			name: "grep content",
			tool: "grep",
			args: map[string]any{
				"path":        "src",
				"output_mode": "content",
			},
			result: `{"matches":2,"returned":1,"output":"## src/main.go\n12: hello\n13: world\n"}`,
			want: ToolPreview{
				Kind:       ToolPreviewKindGrep,
				Path:       "src",
				Returned:   1,
				OutputMode: "content",
				Output:     "## src/main.go\n12: hello\n13: world\n",
				GrepFiles: []ToolPreviewGrepFile{
					{
						Path: "src/main.go",
						Matches: []ToolPreviewGrepMatch{
							{LineNumber: 12, Text: "hello"},
							{LineNumber: 13, Text: "world"},
						},
					},
				},
			},
		},
		{
			name: "grep files",
			tool: "grep",
			args: map[string]any{
				"output_mode": "files_with_matches",
			},
			result: `{"matches":2,"returned":2,"output":"a.txt\nb.txt\n"}`,
			want: ToolPreview{
				Kind:       ToolPreviewKindGrep,
				Path:       ".",
				Returned:   2,
				OutputMode: "files_with_matches",
				Output:     "a.txt\nb.txt\n",
				GrepFiles: []ToolPreviewGrepFile{
					{Path: "a.txt"},
					{Path: "b.txt"},
				},
			},
		},
		{
			name: "grep count",
			tool: "grep",
			args: map[string]any{
				"output_mode": "count",
			},
			result: `{"matches":3,"returned":3,"output":"a.txt:2\nb.txt:1\n"}`,
			want: ToolPreview{
				Kind:       ToolPreviewKindGrep,
				Path:       ".",
				Returned:   3,
				OutputMode: "count",
				Output:     "a.txt:2\nb.txt:1\n",
				GrepFiles: []ToolPreviewGrepFile{
					{Path: "a.txt", Count: 2},
					{Path: "b.txt", Count: 1},
				},
			},
		},
		{
			name: "bash result",
			tool: "bash",
			args: map[string]any{
				"command": "go test ./...",
			},
			result: `{"exit_code":1,"truncated":true,"output":"FAIL\n","message":"output truncated at 12 characters"}`,
			want: ToolPreview{
				Kind:      ToolPreviewKindBash,
				Command:   "go test ./...",
				ExitCode:  1,
				Truncated: true,
				Output:    "FAIL\n",
				Message:   "output truncated at 12 characters",
			},
		},
		{
			name: "missing critical data falls back to plain",
			tool: "edit",
			args: map[string]any{
				"path":       "x.go",
				"old_string": "before()",
			},
			want: ToolPreview{Kind: ToolPreviewKindPlain},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildToolPreview(tt.tool, tt.args, tt.result, tt.existed)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildToolPreview() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
