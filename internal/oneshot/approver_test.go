package oneshot

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/tool"
)

func TestWorktreeAutoApproverScopesMutationApprovalToWorktree(t *testing.T) {
	worktree := t.TempDir()
	approver := NewWorktreeAutoApprover(worktree)

	mutateResp := make(chan tool.ApprovalResponse, 1)
	if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "mutate"},
		Response: mutateResp,
		Kind:     tool.ApprovalKindPath,
		Path: &tool.PathApprovalDetails{
			WorkDir: worktree,
		},
	}); err != nil {
		t.Fatalf("mutate approval request failed: %v", err)
	}
	if got := awaitSignal(t, mutateResp, "mutate approval response"); !got.Allow {
		t.Fatal("mutate approval response = denied, want allowed")
	}

	bashResp := make(chan tool.ApprovalResponse, 1)
	if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		Response: bashResp,
		Kind:     tool.ApprovalKindPath,
		Path: &tool.PathApprovalDetails{
			WorkDir: worktree,
		},
	}); err != nil {
		t.Fatalf("bash approval request failed: %v", err)
	}
	if got := awaitSignal(t, bashResp, "bash approval response"); got.Allow {
		t.Fatal("bash approval response = allowed, want denied")
	}

	// Mutate scopes outside the worktree must be denied: the parent directory, a
	// sibling directory, and a prefix sibling whose path merely starts with the
	// worktree path.
	outsideScopes := []struct {
		name  string
		scope string
	}{
		{name: "parent", scope: filepath.Dir(worktree)},
		{name: "sibling", scope: filepath.Join(filepath.Dir(worktree), filepath.Base(worktree)+"-sibling")},
		{name: "prefix sibling", scope: worktree + "-other"},
	}
	for _, tc := range outsideScopes {
		t.Run(tc.name, func(t *testing.T) {
			resp := make(chan tool.ApprovalResponse, 1)
			if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: "mutate"},
				Response: resp,
				Kind:     tool.ApprovalKindPath,
				Path: &tool.PathApprovalDetails{
					WorkDir: tc.scope,
				},
			}); err != nil {
				t.Fatalf("mutate approval request failed: %v", err)
			}
			if got := awaitSignal(t, resp, "mutate approval response"); got.Allow {
				t.Fatalf("mutate scope %q = allowed, want denied", tc.scope)
			}
		})
	}

	// A mutate request with nil Path details must not panic and must deny.
	noPathResp := make(chan tool.ApprovalResponse, 1)
	if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "mutate"},
		Response: noPathResp,
		Kind:     tool.ApprovalKindPath,
	}); err != nil {
		t.Fatalf("mutate approval request with nil Path failed: %v", err)
	}
	if got := awaitSignal(t, noPathResp, "mutate approval response with nil Path"); got.Allow {
		t.Fatal("mutate approval response with nil Path = allowed, want denied")
	}
}

func TestWorktreeAutoApproverTrimsAndCleansScope(t *testing.T) {
	worktree := t.TempDir()
	approver := NewWorktreeAutoApprover(worktree)

	resp := make(chan tool.ApprovalResponse, 1)
	if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "mutate"},
		Response: resp,
		Kind:     tool.ApprovalKindPath,
		Path: &tool.PathApprovalDetails{
			WorkDir: "  " + worktree + "  ",
		},
	}); err != nil {
		t.Fatalf("approval request failed: %v", err)
	}
	if got := awaitSignal(t, resp, "approval response"); !got.Allow {
		t.Fatal("whitespace-padded scope = denied, want allowed")
	}
}
