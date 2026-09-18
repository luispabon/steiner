package oneshot

import (
	"context"
	"fmt"
)

func runGitLabCloseout(ctx context.Context, req closeoutRequest) (string, string, error) {
	pushArgs := []string{
		"push",
		"-o", "merge_request.create",
		"-o", "merge_request.title=" + req.Title,
		"-o", "merge_request.description=" + req.Body,
		"-o", "merge_request.target=" + req.TargetBranch,
		req.RemoteName,
		req.Branch,
	}
	if err := runGit(ctx, req.WorktreePath, pushArgs...); err != nil {
		return "", "", fmt.Errorf("closeout: create gitlab merge request via push options: %w", err)
	}
	return "", "merge request created via git push options", nil
}
