package oneshot

import (
	"context"
	"fmt"
)

func runGitLabCloseout(ctx context.Context, worktreePath, remoteName, branch, targetBranch, title, body string) (string, string, error) {
	pushArgs := []string{
		"push",
		"-o", "merge_request.create",
		"-o", "merge_request.title=" + title,
		"-o", "merge_request.description=" + body,
		"-o", "merge_request.target=" + targetBranch,
		remoteName,
		branch,
	}
	if err := runGit(ctx, worktreePath, pushArgs...); err != nil {
		return "", "", fmt.Errorf("closeout: create gitlab merge request via push options: %w", err)
	}
	return "", "merge request created via git push options", nil
}
