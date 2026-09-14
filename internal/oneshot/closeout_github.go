package oneshot

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func runGitHubCloseout(ctx context.Context, worktreePath, remoteName, branch, targetBranch, title, body string) (string, string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", "", fmt.Errorf("closeout: gh cli is required for github closeout: %w", err)
	}
	if err := runGit(ctx, worktreePath, "push", remoteName, branch); err != nil {
		return "", "", fmt.Errorf("closeout: push branch for github: %w", err)
	}
	if err := runGitHubAuth(ctx); err != nil {
		return "", "", err
	}
	out, err := commandOutput(ctx, worktreePath, "gh", "pr", "create", "--title", title, "--body", body, "--base", targetBranch, "--head", branch)
	if err != nil {
		var cmdErr *commandError
		if errors.As(err, &cmdErr) && strings.Contains(strings.ToLower(cmdErr.stderr), "already exists") {
			viewOut, viewErr := commandOutput(ctx, worktreePath, "gh", "pr", "view", branch, "--json", "url", "--jq", ".url")
			if viewErr == nil {
				return extractURL(viewOut), "pull request already existed", nil
			}
		}
		return "", "", fmt.Errorf("closeout: create github pr: %w", err)
	}
	return extractURL(out), "", nil
}

func extractURL(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			return line
		}
	}
	return strings.TrimSpace(out)
}

func runGitHubAuth(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("closeout: github auth check failed: %w", err)
	}
	return nil
}
