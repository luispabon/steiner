package tool

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuildApprovalPreview_ApplyPatch(t *testing.T) {
	t.Parallel()

	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})
	preview := buildApprovalPreview("apply_patch", map[string]any{
		"patch": "*** Begin Patch\n*** Add File: foo.go\n+package main\n*** End Patch",
	}, policy)

	if preview.Tool != "apply_patch" {
		t.Fatalf("Tool = %q, want apply_patch", preview.Tool)
	}
	if len(preview.Fields) != 1 {
		t.Fatalf("Fields len = %d, want 1", len(preview.Fields))
	}
	if preview.Fields[0].Name != "patch" {
		t.Fatalf("Fields[0].Name = %q, want patch", preview.Fields[0].Name)
	}
	if !strings.Contains(preview.Fields[0].Value, "*** Begin Patch") {
		t.Fatalf("Fields[0].Value = %q, want patch content", preview.Fields[0].Value)
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
