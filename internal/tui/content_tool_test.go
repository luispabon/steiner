package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestSpecializedDelegateToolAccessor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool string
		want bool
	}{
		{name: "explore", tool: "explore", want: true},
		{name: "research", tool: "research", want: true},
		{name: "code", tool: "code", want: true},
		{name: "evaluate", tool: "evaluate", want: true},
		{name: "sanity_check", tool: "sanity_check", want: true},
		{name: "review", tool: "review", want: true},
		{name: "follow_up", tool: "follow_up", want: true},
		{name: "uppercase tool", tool: "Explore", want: true},
		{name: "trimmed tool", tool: "  evaluate  ", want: true},
		{name: "delegate", tool: "delegate", want: false},
		{name: "read", tool: "read", want: false},
		{name: "empty", tool: "", want: false},
		{name: "unknown", tool: "debug", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSpecializedDelegateTool(tt.tool); got != tt.want {
				t.Fatalf("isSpecializedDelegateTool(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}

	wantTools := []string{"explore", "research", "code", "evaluate", "sanity_check", "review", "vision", "follow_up"}
	if got := delegation.AllSpecializedDelegateTools(); !slices.Equal(got, wantTools) {
		t.Fatalf("delegation.AllSpecializedDelegateTools() = %v, want %v", got, wantTools)
	}
}

func TestRenderToolCallBoxKeepsRequestedWidth(t *testing.T) {
	useTrueColor(t)
	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}
	segment := &toolCallSegment{
		tool: "bash",
		args: "printf hello",
		meta: "✓",
	}

	rendered := strings.TrimSuffix(buffer.renderToolCall(segment, 50), "\n")
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) != 50 {
			t.Fatalf("line width = %d, want 50 for line %q", lipgloss.Width(line), stripANSI(line))
		}
	}
}

func TestRenderToolCallGroupMixedUsesDefaultBorder(t *testing.T) {
	t.Parallel()

	styles := testStyles(theme.AccentAmber)
	buffer := &contentBuffer{
		styles: styles,
		mcpToolOrigins: map[string]MCPToolOrigin{
			"mcp__server__read": {Server: "server", Tool: "read"},
		},
	}
	group := &toolCallGroupSegment{
		tool:  "mcp__server__read",
		mixed: true,
		entries: []*toolCallSegment{
			{tool: "mcp__server__read", args: "first", collapsed: true},
			{tool: "bash", args: "second", collapsed: true},
		},
	}
	const width = 60
	innerWidth := width - 4
	parts := []string{
		buffer.renderToolCallFrame(group.entries[0], innerWidth),
		buffer.renderToolCallDivider(width - 4),
		buffer.renderToolCallFrame(group.entries[1], innerWidth),
	}
	want := buffer.renderToolCallBox(strings.Join(parts, "\n"), "", width)

	if got := buffer.renderToolCallGroup(group, width); got != want {
		t.Fatalf("mixed group rendering did not use default border\ngot:  %q\nwant: %q", got, want)
	}
}

func TestSummarizeGrepArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name: "grep with pattern, path, and glob",
			args: map[string]any{
				"pattern": "func.*Schema",
				"path":    "internal",
				"glob":    "*.go",
			},
			expected: "'func.*Schema' in ./internal/*.go",
		},
		{
			name:     "grep with pattern and path",
			args:     map[string]any{"pattern": "TODO", "path": "internal"},
			expected: "'TODO' in ./internal",
		},
		{
			name:     "grep with pattern only",
			args:     map[string]any{"pattern": "TODO"},
			expected: "'TODO'",
		},
		{
			name:     "grep with no args",
			args:     map[string]any{},
			expected: "grep",
		},
		{
			name:     "grep with empty pattern",
			args:     map[string]any{"pattern": ""},
			expected: "grep",
		},
		{
			name:     "grep with pattern and path with leading ./",
			args:     map[string]any{"pattern": "error", "path": "./cmd"},
			expected: "'error' in ./cmd",
		},
		{
			name:     "grep with pattern and absolute path",
			args:     map[string]any{"pattern": "config", "path": "/etc/config"},
			expected: "'config' in /etc/config",
		},
		{
			name: "grep with pattern, path ending with slash, and glob",
			args: map[string]any{
				"pattern": "test",
				"path":    "testdata/",
				"glob":    "*.json",
			},
			expected: "'test' in ./testdata/*.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeGrepArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeGlobArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "pattern and path together",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal"},
			expected: "./internal/**/*.go",
		},
		{
			name:     "pattern and path with ./ prefix",
			args:     map[string]any{"pattern": "*.go", "path": "./src"},
			expected: "./src/*.go",
		},
		{
			name:     "pattern only",
			args:     map[string]any{"pattern": "**/*.go"},
			expected: "**/*.go",
		},
		{
			name:     "path only with ./ prefix",
			args:     map[string]any{"path": "./internal"},
			expected: "./internal",
		},
		{
			name:     "path only without ./ prefix",
			args:     map[string]any{"path": "internal"},
			expected: "./internal",
		},
		{
			name:     "empty args map",
			args:     map[string]any{},
			expected: "glob",
		},
		{
			name:     "path with trailing slash",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal/"},
			expected: "./internal/**/*.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeGlobArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeGlobArgs(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestSummarizeReadArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "read with path only (both defaults)",
			args:     map[string]any{"path": "file.go"},
			expected: "file.go:1–200",
		},
		{
			name:     "read with path and offset (limit defaults)",
			args:     map[string]any{"path": "file.go", "offset": 150.0},
			expected: "file.go:150–349",
		},
		{
			name:     "read with path, offset, and limit",
			args:     map[string]any{"path": "file.go", "offset": 150.0, "limit": 50.0},
			expected: "file.go:150–199",
		},
		{
			name:     "read with no path",
			args:     map[string]any{},
			expected: "read",
		},
		{
			name:     "read with file_path fallback",
			args:     map[string]any{"file_path": "main.go"},
			expected: "main.go:1–200",
		},
		{
			name:     "read with both path and file_path (path preferred)",
			args:     map[string]any{"path": "preferred.go", "file_path": "fallback.go"},
			expected: "preferred.go:1–200",
		},
		{
			name:     "read with offset=1 (explicit default)",
			args:     map[string]any{"path": "test.go", "offset": 1.0},
			expected: "test.go:1–200",
		},
		{
			name:     "read with large limit",
			args:     map[string]any{"path": "doc.txt", "limit": 1000.0},
			expected: "doc.txt:1–1000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeReadArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeLSArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "ls with path",
			args:     map[string]any{"path": "./mydir"},
			expected: "./mydir",
		},
		{
			name:     "ls with path + recursive=true",
			args:     map[string]any{"path": "./mydir", "recursive": true},
			expected: "./mydir (recursive)",
		},
		{
			name:     "ls with path + recursive=false",
			args:     map[string]any{"path": "./mydir", "recursive": false},
			expected: "./mydir",
		},
		{
			name:     "ls with no args",
			args:     map[string]any{},
			expected: ".",
		},
		{
			name:     "ls with recursive=true only",
			args:     map[string]any{"recursive": true},
			expected: ". (recursive)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeLSArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeLSArgs(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestSummarizeMutateArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name: "single replace op",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
				},
			},
			expected: "replace file.go",
		},
		{
			name: "single move op",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "move", "from": "a.go", "to": "b.go"},
				},
			},
			expected: "move a.go → b.go",
		},
		{
			name: "single create op",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "create", "path": "new.go"},
				},
			},
			expected: "create new.go",
		},
		{
			name: "multi-op shows count",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
					map[string]any{"type": "delete", "path": "old.go"},
					map[string]any{"type": "create", "path": "new.go"},
				},
			},
			expected: "replace file.go (+2 more)",
		},
		{
			name:     "no operations",
			args:     map[string]any{},
			expected: "mutate",
		},
		{
			name: "op with no type",
			args: map[string]any{
				"operations": []any{
					map[string]any{"path": "file.go"},
				},
			},
			expected: "file.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeMutateArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSummarizeArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		expected string
	}{
		{
			name: "grep tool dispatches to grep handler",
			tool: "grep",
			args: map[string]any{
				"pattern": "func.*Schema",
				"path":    "internal",
				"glob":    "*.go",
			},
			expected: "'func.*Schema' in ./internal/*.go",
		},
		{
			name:     "bash tool uses command key",
			tool:     "bash",
			args:     map[string]any{"command": "ls -la"},
			expected: "ls -la",
		},
		{
			name:     "glob tool dispatches to glob handler",
			tool:     "glob",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal"},
			expected: "./internal/**/*.go",
		},
		{
			name:     "fetch_url shows url",
			tool:     "fetch_url",
			args:     map[string]any{"url": "https://example.com"},
			expected: "https://example.com",
		},
		{
			name:     "nil args returns tool name",
			tool:     "bash",
			args:     nil,
			expected: "bash",
		},
		{
			name:     "read tool dispatches to read handler",
			tool:     "read",
			args:     map[string]any{"path": "main.go"},
			expected: "main.go:1–200",
		},
		{
			name:     "read_file tool dispatches to read handler",
			tool:     "read_file",
			args:     map[string]any{"path": "config.yaml", "offset": 10.0, "limit": 30.0},
			expected: "config.yaml:10–39",
		},
		{
			name:     "ls tool dispatches to ls handler",
			tool:     "ls",
			args:     map[string]any{"path": "./src", "recursive": true},
			expected: "./src (recursive)",
		},
		{
			name: "mutate tool shows op type",
			tool: "mutate",
			args: map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
				},
			},
			expected: "replace file.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := summarizeArgs(tt.tool, tt.args)
			if result != tt.expected {
				t.Errorf("summarizeArgs(%q) = %q, want %q", tt.tool, result, tt.expected)
			}
		})
	}
}

func TestApplyFinishedToolCallResultMutateStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		preview   output.ToolPreview
		err       error
		wantMeta  string
		wantError bool
	}{
		{
			name: "mutate all operations succeeded shows check",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindMutate,
				HunksApplied:     2,
				HunksFailed:      0,
				MutateOperations: []output.ToolPreviewMutateOperation{{Type: "replace", Path: "file.go"}},
			},
			wantMeta:  "✓",
			wantError: false,
		},
		{
			name: "mutate partial failures shows error",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindMutate,
				HunksApplied:     1,
				HunksFailed:      1,
				MutateOperations: []output.ToolPreviewMutateOperation{{Type: "replace", Path: "file.go"}},
			},
			wantMeta:  "✗",
			wantError: true,
		},
		{
			name: "mutate transport-level error still shows error",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindMutate,
				HunksApplied:     1,
				HunksFailed:      0,
				MutateOperations: []output.ToolPreviewMutateOperation{{Type: "replace", Path: "file.go"}},
			},
			err:       errors.New("boom"),
			wantMeta:  "✗",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buffer := &contentBuffer{
				collapseState: make(map[int]bool),
			}

			buffer.AppendEvent(output.NewToolCallStartedEvent(1, "mutate", "call_1", map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
				},
			}))
			buffer.AppendEvent(output.NewToolCallFinishedEventWithPreview(1, "mutate", "call_1", `{"operations_applied":1,"operations_failed":0}`, tt.err, tt.preview))

			if got := len(buffer.segments); got != 1 {
				t.Fatalf("segments = %d, want 1", got)
			}

			td := buffer.segments[0].toolData
			if td == nil {
				t.Fatal("toolData = nil, want mutate tool call")
			}
			if got := td.meta; got != tt.wantMeta {
				t.Fatalf("meta = %q, want %q", got, tt.wantMeta)
			}
			if got := td.hasError; got != tt.wantError {
				t.Fatalf("hasError = %v, want %v", got, tt.wantError)
			}
		})
	}
}
