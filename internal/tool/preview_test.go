package tool

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

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
