package tui

import "testing"

func TestSummarizeLSArgs(t *testing.T) {
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
			result := summarizeLSArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeLSArgs(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

func TestSummarizeArgs(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		expected string
	}{
		{
			name:     "ls tool dispatches to ls handler",
			tool:     "ls",
			args:     map[string]any{"path": "./src", "recursive": true},
			expected: "./src (recursive)",
		},
		{
			name:     "bash tool uses command key",
			tool:     "bash",
			args:     map[string]any{"command": "ls -la"},
			expected: "ls -la",
		},
		{
			name:     "ls with case-insensitive tool name",
			tool:     "LS",
			args:     map[string]any{"path": "./docs"},
			expected: "./docs",
		},
		{
			name:     "nil args returns tool name",
			tool:     "bash",
			args:     nil,
			expected: "bash",
		},
		{
			name:     "empty args returns tool name",
			tool:     "bash",
			args:     map[string]any{},
			expected: "bash",
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
