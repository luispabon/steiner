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

func (a worktreeAutoApprover) RequestApproval(_ context.Context, req tool.ApprovalRequest) error {
	if req.Response == nil {
		return nil
	}

	allowed := false
	if strings.EqualFold(strings.TrimSpace(req.Tool.Name), "mutate") && req.Kind == tool.ApprovalKindPath && req.Path != nil {
		scope := strings.TrimSpace(req.Path.WorkDir)
		if scope == "" {
			scope = req.Path.Preview.WorkDir
		}
		scope = filepath.Clean(strings.TrimSpace(scope))
		allowed = tool.PathWithinRoot(a.worktreeRoot, scope)
	}

	req.Response <- tool.ApprovalResponse{Allow: allowed}
	return nil
}
