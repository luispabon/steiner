package tui

import "testing"

func TestSummarizeArgs(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		expected string
	}{
		{
			name:     "command_arg",
			tool:     "bash",
			args:     map[string]any{"command": "echo hello"},
			expected: "echo hello",
		},
		{
			name:     "path_arg",
			tool:     "read",
			args:     map[string]any{"path": "/etc/passwd"},
			expected: "/etc/passwd",
		},
		{
			name:     "file_path_arg",
			tool:     "read",
			args:     map[string]any{"file_path": "/home/user/file.txt"},
			expected: "/home/user/file.txt",
		},
		{
			name:     "pattern_arg",
			tool:     "glob",
			args:     map[string]any{"pattern": "*.go"},
			expected: "*.go",
		},
		{
			name:     "query_arg",
			tool:     "search",
			args:     map[string]any{"query": "test query"},
			expected: "test query",
		},
		{
			name:     "description_arg",
			tool:     "todo",
			args:     map[string]any{"description": "fix bug"},
			expected: "fix bug",
		},
		{
			name:     "url_arg",
			tool:     "fetch_url",
			args:     map[string]any{"url": "https://example.com"},
			expected: "https://example.com",
		},
		{
			name:     "nil_args",
			tool:     "bash",
			args:     nil,
			expected: "bash",
		},
		{
			name:     "empty_args",
			tool:     "bash",
			args:     map[string]any{},
			expected: "bash",
		},
		{
			name:     "url_priority_over_fallback",
			tool:     "fetch_url",
			args:     map[string]any{"url": "https://example.com", "other": "value"},
			expected: "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeArgs(tt.tool, tt.args)
			if result != tt.expected {
				t.Errorf("summarizeArgs(%q, %v) = %q, want %q", tt.tool, tt.args, result, tt.expected)
			}
		})
	}
}
