package oneshot

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

func runAzureCloseout(ctx context.Context, worktreePath, remoteName, branch, targetBranch, title, body string) (string, string, error) {
	if _, err := exec.LookPath("az"); err != nil {
		return "", "", fmt.Errorf("closeout: az cli is required for azure closeout: %w", err)
	}
	if err := runGit(ctx, worktreePath, "push", remoteName, branch); err != nil {
		return "", "", fmt.Errorf("closeout: push branch for azure devops: %w", err)
	}
	repository := repositoryNameFromRemote(ctx, worktreePath, remoteName)
	out, err := commandOutput(ctx, worktreePath, "az", "repos", "pr", "create", "--title", title, "--description", body, "--source-branch", branch, "--target-branch", targetBranch, "--repository", repository, "--output", "tsv", "--query", "url")
	if err != nil {
		return "", "", fmt.Errorf("closeout: create azure pr: %w", err)
	}
	return strings.TrimSpace(out), "", nil
}

func repositoryNameFromRemote(ctx context.Context, worktreePath, remoteName string) string {
	remoteURL, err := gitOutput(ctx, worktreePath, "remote", "get-url", remoteName)
	if err != nil {
		return remoteName
	}
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return remoteName
	}

	if parsed, err := url.Parse(remoteURL); err == nil {
		if name := pathBaseCandidate(parsed.Path); name != "" {
			return name
		}
	}
	if name := pathBaseCandidate(remoteURL); name != "" {
		return name
	}
	return remoteName
}

func pathBaseCandidate(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return ""
	}
	if strings.Contains(path, ":") {
		path = strings.Split(path, ":")[len(strings.Split(path, ":"))-1]
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	return base
}
