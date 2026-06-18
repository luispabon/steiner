package oneshot

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

type worktreeAutoApprover struct {
	worktreeRoot string
}

// NewWorktreeAutoApprover returns an approval responder that auto-allows
// worktree-scoped mutation approvals and denies everything else.
func NewWorktreeAutoApprover(worktreeRoot string) tool.ApprovalResponder {
	return worktreeAutoApprover{worktreeRoot: filepath.Clean(strings.TrimSpace(worktreeRoot))}
}

func (a worktreeAutoApprover) RequestApproval(ctx context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return nil
	}

	allowed := false
	if strings.EqualFold(strings.TrimSpace(req.Tool.Name), "mutate") {
		scope := strings.TrimSpace(req.WorkDir)
		if scope == "" {
			scope = req.Preview.WorkDir
		}
		allowed = pathWithinRoot(a.worktreeRoot, scope)
	}

	req.Response <- tool.ApprovalResponse{Allow: allowed}
	return nil
}

func pathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	candidate = filepath.Clean(strings.TrimSpace(candidate))
	if root == "" || candidate == "" {
		return false
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == "" {
		return false
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
