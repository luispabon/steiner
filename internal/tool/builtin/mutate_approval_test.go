package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// newApprovalTestExecutor builds an executor over the real mutate tool. workDir
// is both the policy root and the tool's working directory, and approver is the
// executor's approval responder: nil stands for a run with nobody to ask, as in
// a oneshot phase.
func newApprovalTestExecutor(t *testing.T, workDir string, projectRootOnly bool, approver tool.ApprovalResponder) *tool.Executor {
	t.Helper()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{ProjectRootOnly: projectRootOnly})
	registry := tool.NewRegistry(NewMutateTool(Env{WorkDir: workDir, PathPolicy: &policy}))
	cfg := config.Config{Paths: config.PathsConfig{ProjectRootOnly: projectRootOnly}}
	return tool.NewExecutor(registry, cfg, approver, workDir, "", tool.Unsandboxed{})
}

// assertDeniedByOutsidePath fails the test unless err is a policy_denied tool
// error.
func assertDeniedByOutsidePath(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Execute(mutate) error = nil, want policy_denied")
	}
	var toolErr *tool.ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *tool.ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
}

// TestMutateNilApproverDeniesOutsidePath pins the no-approver rule: with nobody
// to approve, a path outside the project root is denied before the mutate
// handler runs, so the outside file is never created.
func TestMutateNilApproverDeniesOutsidePath(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")

	executor := newApprovalTestExecutor(t, root, true, nil)
	_, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": outside, "content": "outside\n"},
		},
	})
	assertDeniedByOutsidePath(t, err)
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file stat error = %v, want not exist", err)
	}
	entries, err := os.ReadDir(outsideDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", outsideDir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside dir entries = %d, want 0: denied call left side effects", len(entries))
	}
}

// TestMutateNilApproverAllowsInRootPath pins that an in-root mutate needs no
// approval, so a run without an approver still edits the worktree.
func TestMutateNilApproverAllowsInRootPath(t *testing.T) {
	root := t.TempDir()

	executor := newApprovalTestExecutor(t, root, true, nil)
	result, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "notes.txt", "content": "notes\n"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	got, ok := result.(*MutateResult)
	if !ok {
		t.Fatalf("result type = %T, want *MutateResult", result)
	}
	if got.OperationsApplied != 1 || got.OperationsFailed != 0 {
		t.Fatalf("mutate result = %#v, want 1 applied, 0 failed", got)
	}
	assertFile(t, filepath.Join(root, "notes.txt"), "notes\n")
}

// TestMutateNilApproverDeniesMixedBatch pins that one outside path denies the
// whole batch: the in-root operation listed before it is not applied either.
func TestMutateNilApproverDeniesMixedBatch(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	executor := newApprovalTestExecutor(t, root, true, nil)
	_, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "inside.txt", "content": "inside\n"},
			map[string]any{"type": "create", "path": outside, "content": "outside\n"},
		},
	})
	assertDeniedByOutsidePath(t, err)
	if _, err := os.Stat(filepath.Join(root, "inside.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("in-root file stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file stat error = %v, want not exist", err)
	}
}

// TestMutateApprovedRelativePathStaysRooted pins that approving an outside path
// relaxes root containment without dropping the root anchor: the relative path
// in the approved call still resolves against the project root, not the process
// working directory.
func TestMutateApprovedRelativePathStaysRooted(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	relPath := filepath.Join("..", "outside.txt")
	wantPath := filepath.Join(base, "outside.txt")

	var requests int
	var deniedPath string
	approver := tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
		requests++
		if req.Path != nil {
			deniedPath = req.Path.DeniedPath
		}
		req.Response <- tool.ApprovalResponse{Allow: true}
		return nil
	})

	executor := newApprovalTestExecutor(t, root, true, approver)
	if _, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": relPath, "content": "approved\n"},
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v, want nil after approval", err)
	}
	if requests != 1 {
		t.Fatalf("approval requests = %d, want 1", requests)
	}
	if deniedPath != wantPath {
		t.Fatalf("DeniedPath = %q, want %q", deniedPath, wantPath)
	}
	assertFile(t, wantPath, "approved\n")
}

// TestMutateProjectRootOnlyFalseAllowsOutsidePath pins the configuration
// boundary: an explicit paths.project_root_only: false is effective, so an
// outside path needs no approval at all.
func TestMutateProjectRootOnlyFalseAllowsOutsidePath(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")

	executor := newApprovalTestExecutor(t, root, false, nil)
	if _, err := executor.Execute(context.Background(), "mutate", "", map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": outside, "content": "outside\n"},
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v, want nil with project_root_only false", err)
	}
	assertFile(t, outside, "outside\n")
}
