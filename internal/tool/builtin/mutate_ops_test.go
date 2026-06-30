package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestMultiOpBatch(t *testing.T) {
	tests := []struct {
		name       string
		initial    string
		operations []any
		want       string
		wantFailed int
	}{
		{
			name:    "two sequential delete_line ops see updated line numbers",
			initial: "line1\nline2\nline3\nline4\nline5\n",
			operations: []any{
				map[string]any{"type": "delete_line", "path": "test.txt", "line": float64(2)},
				map[string]any{"type": "delete_line", "path": "test.txt", "line": float64(3)},
			},
			want:       "line1\nline3\nline5\n",
			wantFailed: 0,
		},
		{
			name:    "three ops with interleaved delete and insert",
			initial: "line1\nline2\nline3\nline4\nline5\n",
			operations: []any{
				map[string]any{"type": "delete_line", "path": "test.txt", "line": float64(2)},
				map[string]any{"type": "line_replace", "path": "test.txt", "line": float64(2), "old_string": "line3", "new_string": "LINE3"},
				map[string]any{"type": "delete_line", "path": "test.txt", "line": float64(4)},
			},
			want:       "line1\nLINE3\nline4\n",
			wantFailed: 0,
		},
		{
			name:    "delete multiple lines at once then delete again",
			initial: "a\nb\nc\nd\ne\nf\n",
			operations: []any{
				map[string]any{"type": "delete_line", "path": "test.txt", "line": float64(2), "line_count": float64(2)},
				map[string]any{"type": "delete_line", "path": "test.txt", "line": float64(2)},
			},
			want:       "a\ne\nf\n",
			wantFailed: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "test.txt")
			if err := os.WriteFile(path, []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			policy := tool.NewPathPolicy(root, config.PathsConfig{})
			toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy})

			result, err := toolDef.Handler(context.Background(), map[string]any{
				"operations": tt.operations,
			})
			if err != nil {
				t.Fatalf("mutate Handler() error = %v", err)
			}

			got, ok := result.(*MutateResult)
			if !ok {
				t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
			}

			if got.OperationsFailed != tt.wantFailed {
				t.Fatalf("OperationsFailed = %d, want %d; output=%q", got.OperationsFailed, tt.wantFailed, got.Output)
			}

			assertFile(t, path, tt.want)
		})
	}
}
