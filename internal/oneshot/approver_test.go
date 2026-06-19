package oneshot

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/tool"
)

func TestWorktreeAutoApproverScopesMutationApprovalToWorktree(t *testing.T) {
	worktree := t.TempDir()
	approver := NewWorktreeAutoApprover(worktree)

	mutateResp := make(chan tool.ApprovalResponse, 1)
	if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "mutate"},
		WorkDir:  worktree,
		Response: mutateResp,
	}); err != nil {
		t.Fatalf("mutate approval request failed: %v", err)
	}
	if got := <-mutateResp; !got.Allow {
		t.Fatal("mutate approval response = denied, want allowed")
	}

	bashResp := make(chan tool.ApprovalResponse, 1)
	if err := approver.RequestApproval(context.Background(), tool.ApprovalRequest{
		Tool:     tool.ToolDef{Name: "bash"},
		WorkDir:  worktree,
		Response: bashResp,
	}); err != nil {
		t.Fatalf("bash approval request failed: %v", err)
	}
	if got := <-bashResp; got.Allow {
		t.Fatal("bash approval response = allowed, want denied")
	}
}
