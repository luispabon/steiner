package tool

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestGlobApprovalPreview proves the glob preview always shows the pattern,
// defaulting it to "*", and shows the path when the call names one.
func TestGlobApprovalPreview(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  []PreviewField
	}{
		{
			name:  "pattern and path",
			input: map[string]any{"pattern": "**/*.go", "path": "src"},
			want: []PreviewField{
				{Name: "pattern", Value: "**/*.go"},
				{Name: "path", Value: "src"},
			},
		},
		{
			name:  "missing pattern defaults to star",
			input: map[string]any{"path": "."},
			want: []PreviewField{
				{Name: "pattern", Value: "*"},
				{Name: "path", Value: "."},
			},
		},
		{
			name:  "empty path defaults to the current directory",
			input: map[string]any{"pattern": "*.go", "path": ""},
			want: []PreviewField{
				{Name: "pattern", Value: "*.go"},
				{Name: "path", Value: "."},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := buildApprovalPreview("glob", tt.input, PathPolicy{})
			if !reflect.DeepEqual(preview.Fields, tt.want) {
				t.Fatalf("fields = %v, want %v", preview.Fields, tt.want)
			}
		})
	}
}

func TestApprovalPreviewSanitizesWorkspaceRoots(t *testing.T) {
	t.Parallel()

	policy := NewPathPolicy("/repo", config.PathsConfig{ProjectRootOnly: true})
	preview, err := policy.previewToolInput("bash", map[string]any{
		"cwd":     "/repo/subdir",
		"command": "cd /repo && go test ./internal/agent",
	})
	if err != nil {
		t.Fatalf("previewToolInput(bash) error = %v", err)
	}

	summary := preview.Summary()
	if strings.Contains(summary, "/repo") {
		t.Fatalf("Summary() = %q, want no absolute workspace root", summary)
	}
	if !strings.Contains(summary, "workdir=.") {
		t.Fatalf("Summary() = %q, want redacted workdir", summary)
	}
	if !strings.Contains(summary, "cwd=subdir") {
		t.Fatalf("Summary() = %q, want repo-relative cwd", summary)
	}
	if !strings.Contains(summary, "command=go test ./internal/agent") {
		t.Fatalf("Summary() = %q, want sanitized command fragment", summary)
	}
}
