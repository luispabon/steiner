package output

import (
	"reflect"
	"testing"
)

func TestStripReadLineNumberPrefixForLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		lineNumber int
		want       string
	}{
		{
			name:       "line number with tab separator and indented content",
			line:       "     8\t\tNewReadTool(env),",
			lineNumber: 8,
			want:       "\tNewReadTool(env),",
		},
		{
			name:       "line number with space separator and no indentation",
			line:       "1 hello",
			lineNumber: 1,
			want:       "hello",
		},
		{
			name:       "leading spaces before line number",
			line:       "  12\tcontent",
			lineNumber: 12,
			want:       "content",
		},
		{
			name:       "no line number prefix returns as-is",
			line:       "just content",
			lineNumber: 1,
			want:       "just content",
		},
		{
			name:       "empty line",
			line:       "",
			lineNumber: 1,
			want:       "",
		},
		{
			name:       "line number no content after",
			line:       "     5",
			lineNumber: 5,
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripReadLineNumberPrefixForLine(tt.line, tt.lineNumber)
			if got != tt.want {
				t.Fatalf("stripReadLineNumberPrefixForLine(%q, %d) = %q, want %q", tt.line, tt.lineNumber, got, tt.want)
			}
		})
	}
}

func TestStripReadPreviewContents(t *testing.T) {
	input := []string{
		"     1\tpackage main",
		"     2\t",
		"     3\tfunc main() {",
		"     4\t\tfmt.Println(\"hi\")",
		"     5\t}",
	}
	want := "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}"
	got := stripReadPreviewContents(input, 1)
	if got != want {
		t.Fatalf("stripReadPreviewContents() = %q, want %q", got, want)
	}
}

func TestNormalizeReadPreviewContents(t *testing.T) {
	tests := []struct {
		name      string
		contents  string
		startLine int
		want      string
	}{
		{
			name:      "Dive-style numbered content with indentation",
			contents:  "     1\tpackage main\n     2\t\n     3\tfunc main() {\n     4\t\tfmt.Println(\"hi\")\n     5\t}\n",
			startLine: 1,
			want:      "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n",
		},
		{
			name:      "empty content",
			contents:  "",
			startLine: 1,
			want:      "",
		},
		{
			name:      "content without line number prefixes",
			contents:  "hello\nworld\n",
			startLine: 0,
			want:      "hello\nworld\n",
		},
		{
			name:      "startLine zero with non-numbered content",
			contents:  "hello\nworld\n",
			startLine: 0,
			want:      "hello\nworld\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeReadPreviewContents(tt.contents, tt.startLine)
			if got != tt.want {
				t.Fatalf("normalizeReadPreviewContents(%q, %d) = %q, want %q", tt.contents, tt.startLine, got, tt.want)
			}
		})
	}
}

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
			name: "read file preserves content indentation",
			tool: "read",
			args: map[string]any{
				"path": "main.go",
			},
			result: `{"path":"main.go","start_line":1,"output":"1 package main\n2\n3 func main() {\n4 \t\tfmt.Println(\"hi\")\n5 }\n"}`,
			want: ToolPreview{
				Kind:     ToolPreviewKindReadFile,
				Path:     "main.go",
				Language: "go",
				Contents: "package main\n\nfunc main() {\n\t\tfmt.Println(\"hi\")\n}\n",
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
			name: "grep content metadata",
			tool: "grep",
			args: map[string]any{
				"path":        "src",
				"output_mode": "content",
			},
			result: `{"matches":1,"returned":1,"truncated":true,"has_more":true,"next_offset":5,"output":"## src/main.go\n12: hello\n"}`,
			want: ToolPreview{
				Kind:       ToolPreviewKindGrep,
				Path:       "src",
				Truncated:  true,
				HasMore:    true,
				Returned:   1,
				NextOffset: 5,
				OutputMode: "content",
				Output:     "## src/main.go\n12: hello\n",
				GrepFiles: []ToolPreviewGrepFile{
					{
						Path: "src/main.go",
						Matches: []ToolPreviewGrepMatch{
							{LineNumber: 12, Text: "hello"},
						},
					},
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
