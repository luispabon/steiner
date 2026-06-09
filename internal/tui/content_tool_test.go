package tui

import "testing"

func TestSummarizeMutateArgs(t *testing.T) {
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
			result := summarizeMutateArgs(tt.args)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
