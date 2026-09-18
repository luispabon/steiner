package oneshot

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

func runAzureCloseout(ctx context.Context, req closeoutRequest) (string, string, error) {
	if _, err := exec.LookPath("az"); err != nil {
		return "", "", fmt.Errorf("closeout: az cli is required for azure closeout: %w", err)
	}
	if err := runGit(ctx, req.WorktreePath, "push", req.RemoteName, req.Branch); err != nil {
		return "", "", fmt.Errorf("closeout: push branch for azure devops: %w", err)
	}
	repository := repositoryNameFromRemote(ctx, req.WorktreePath, req.RemoteName)
	out, err := commandOutput(ctx, req.WorktreePath, "az", "repos", "pr", "create", "--title", req.Title, "--description", req.Body, "--source-branch", req.Branch, "--target-branch", req.TargetBranch, "--repository", repository, "--output", "tsv", "--query", "url")
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
