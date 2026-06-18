package oneshot

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

const (
	closeoutStateSkipped     = "skipped"
	closeoutStateUnsupported = "unsupported"
	closeoutStateCreated     = "created"
	closeoutStateFailed      = "failed"
)

type closeoutProvider string

const (
	closeoutProviderGitHub closeoutProvider = "github"
	closeoutProviderGitLab closeoutProvider = "gitlab"
	closeoutProviderAzure  closeoutProvider = "azure"
)

// CloseoutInput carries the on-disk artifacts needed to build the final PR/MR body.
type CloseoutInput struct {
	Manifest       Manifest
	Report         FinalReport
	Overview       string
	CommitMessages []string
}

// CloseoutResult describes whether the optional auto-PR closeout ran and what it did.
type CloseoutResult struct {
	State        string `json:"state"`
	Provider     string `json:"provider,omitempty"`
	Remote       string `json:"remote,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	Title        string `json:"title,omitempty"`
	Body         string `json:"body,omitempty"`
	URL          string `json:"url,omitempty"`
	Note         string `json:"note,omitempty"`
}

// Closeout runs on the host process, not inside the model sandbox.
//
// That keeps the user's existing git credentials, SSH agent, gh auth state, and
// Azure CLI login visible to the push/PR command when oneshot.auto_pr is enabled.
//
//nolint:gocyclo
func Closeout(ctx context.Context, cfg config.Config, input CloseoutInput) (CloseoutResult, error) {
	if !cfg.OneShot.AutoPR {
		return CloseoutResult{
			State: closeoutStateSkipped,
			Note:  "oneshot.auto_pr is disabled",
		}, nil
	}
	if !input.Report.Completion {
		return CloseoutResult{
			State: closeoutStateSkipped,
			Note:  "review did not pass",
		}, nil
	}

	worktreePath := strings.TrimSpace(input.Manifest.WorktreePath)
	if worktreePath == "" {
		return CloseoutResult{State: closeoutStateFailed}, fmt.Errorf("closeout: manifest worktree path is required")
	}

	branch := pickFirst(strings.TrimSpace(input.Report.Git.Branch), strings.TrimSpace(input.Manifest.Branch))
	if branch == "" {
		return CloseoutResult{State: closeoutStateFailed}, fmt.Errorf("closeout: branch name is required")
	}

	remoteName := trackingRemote(ctx, worktreePath, branch)

	remoteURL, err := gitOutput(ctx, worktreePath, "remote", "get-url", remoteName)
	if err != nil {
		return CloseoutResult{State: closeoutStateFailed}, fmt.Errorf("closeout: resolve remote url: %w", err)
	}

	provider, err := detectCloseoutProvider(remoteURL)
	if err != nil {
		return CloseoutResult{
			State:  closeoutStateUnsupported,
			Remote: remoteName,
			Note:   err.Error(),
		}, nil
	}

	targetBranch, err := resolveTargetBranch(ctx, worktreePath, remoteName)
	if err != nil {
		return CloseoutResult{State: closeoutStateFailed}, err
	}

	commitMessages := sanitizeStrings(input.CommitMessages, finalReportItemLimit)
	if len(commitMessages) == 0 {
		commitMessages, err = collectCommitMessages(ctx, worktreePath, targetBranch, branch)
		if err != nil {
			return CloseoutResult{State: closeoutStateFailed}, err
		}
	}

	title := closeoutTitle(input.Report)
	body := AssembleCloseoutBody(input.Overview, commitMessages, input.Report)

	switch provider {
	case closeoutProviderGitHub:
		url, err := runGitHubCloseout(ctx, worktreePath, remoteName, branch, targetBranch, title, body)
		if err != nil {
			return CloseoutResult{State: closeoutStateFailed, Provider: string(provider), Remote: remoteName, TargetBranch: targetBranch, Title: title, Body: body}, err
		}
		return CloseoutResult{
			State:        closeoutStateCreated,
			Provider:     string(provider),
			Remote:       remoteName,
			TargetBranch: targetBranch,
			Title:        title,
			Body:         body,
			URL:          url,
		}, nil
	case closeoutProviderGitLab:
		if err := runGitLabCloseout(ctx, worktreePath, remoteName, branch, targetBranch, title, body); err != nil {
			return CloseoutResult{State: closeoutStateFailed, Provider: string(provider), Remote: remoteName, TargetBranch: targetBranch, Title: title, Body: body}, err
		}
		return CloseoutResult{
			State:        closeoutStateCreated,
			Provider:     string(provider),
			Remote:       remoteName,
			TargetBranch: targetBranch,
			Title:        title,
			Body:         body,
			Note:         "merge request created via git push options",
		}, nil
	case closeoutProviderAzure:
		url, err := runAzureCloseout(ctx, worktreePath, remoteName, branch, targetBranch, title, body)
		if err != nil {
			return CloseoutResult{State: closeoutStateFailed, Provider: string(provider), Remote: remoteName, TargetBranch: targetBranch, Title: title, Body: body}, err
		}
		return CloseoutResult{
			State:        closeoutStateCreated,
			Provider:     string(provider),
			Remote:       remoteName,
			TargetBranch: targetBranch,
			Title:        title,
			Body:         body,
			URL:          url,
		}, nil
	default:
		return CloseoutResult{
			State:  closeoutStateUnsupported,
			Remote: remoteName,
			Note:   "unsupported provider",
		}, nil
	}
}

// AssembleCloseoutBody combines the overview, commit history, and review outcome
// into the bounded description text used for the PR/MR body.
func AssembleCloseoutBody(overview string, commitMessages []string, report FinalReport) string {
	var b strings.Builder
	writeSection := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(title)
		b.WriteString("\n")
		for _, line := range lines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	overviewLines := sectionLines(overview, finalReportItemLimit)
	commits := sanitizeStrings(commitMessages, finalReportItemLimit)
	commitLines := make([]string, 0, len(commits)+1)
	if len(commits) == 0 {
		commitLines = append(commitLines, "- none")
	} else {
		for _, commit := range commits {
			commitLines = append(commitLines, "- "+commit)
		}
	}

	reviewLines := make([]string, 0, 1+len(report.Risks)+len(report.UnresolvedIssues))
	reviewSummary := compactText(report.ReviewOutcome)
	if reviewSummary == "" {
		reviewSummary = "review passed"
		if !report.Completion {
			reviewSummary = "review failed"
		}
	}
	reviewLines = append(reviewLines, "- outcome: "+reviewSummary)
	if len(report.Risks) > 0 {
		for _, risk := range sanitizeStrings(report.Risks, finalReportItemLimit) {
			reviewLines = append(reviewLines, "- risk: "+risk)
		}
	}
	if len(report.UnresolvedIssues) > 0 {
		for _, issue := range sanitizeStrings(report.UnresolvedIssues, finalReportItemLimit) {
			reviewLines = append(reviewLines, "- unresolved: "+issue)
		}
	}

	writeSection("Overview", overviewLines)
	writeSection("Commit history", commitLines)
	writeSection("Review outcome", reviewLines)

	return strings.TrimSpace(b.String())
}

func closeoutTitle(report FinalReport) string {
	title := compactText(report.TaskSummary)
	if title == "" {
		title = compactText(report.Slug)
	}
	if title == "" {
		return "oneshot closeout"
	}
	return title
}

func trackingRemote(ctx context.Context, worktreePath, branch string) string {
	remote, err := gitOutput(ctx, worktreePath, "config", "--get", "branch."+branch+".remote")
	if err == nil {
		remote = strings.TrimSpace(remote)
		if remote != "" {
			return remote
		}
	}
	return "origin"
}

func resolveTargetBranch(ctx context.Context, worktreePath, remote string) (string, error) {
	if upstream, err := gitOutput(ctx, worktreePath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		upstream = strings.TrimSpace(upstream)
		if upstream != "" {
			if _, branch, ok := strings.Cut(upstream, "/"); ok {
				branch = strings.TrimSpace(branch)
				if branch != "" {
					return branch, nil
				}
			}
			return upstream, nil
		}
	}

	for _, candidate := range []string{"main", "master"} {
		if _, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("closeout: could not determine target branch for remote %q", remote)
}

func collectCommitMessages(ctx context.Context, worktreePath, targetBranch, branch string) ([]string, error) {
	out, err := gitOutput(ctx, worktreePath, "log", "--no-merges", "--format=%s", targetBranch+".."+branch)
	if err != nil {
		return nil, fmt.Errorf("closeout: collect commit messages: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	messages := make([]string, 0, len(lines))
	for _, line := range lines {
		line = compactText(line)
		if line == "" {
			continue
		}
		messages = append(messages, line)
		if len(messages) >= finalReportItemLimit {
			break
		}
	}
	return messages, nil
}

func detectCloseoutProvider(remoteURL string) (closeoutProvider, error) {
	host := remoteHost(remoteURL)
	switch {
	case host == "":
		return "", fmt.Errorf("closeout: remote url %q has no host", remoteURL)
	case strings.Contains(host, "github.com") || strings.HasSuffix(host, ".github.com") || strings.Contains(host, "github"):
		return closeoutProviderGitHub, nil
	case strings.Contains(host, "gitlab.com") || strings.Contains(host, "gitlab"):
		return closeoutProviderGitLab, nil
	case strings.Contains(host, "dev.azure.com") || strings.Contains(host, "visualstudio.com") || strings.Contains(host, "azure"):
		return closeoutProviderAzure, nil
	default:
		return "", fmt.Errorf("unsupported provider for remote host %q", host)
	}
}

func remoteHost(remoteURL string) string {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return ""
	}

	if parsed, err := url.Parse(remoteURL); err == nil && parsed.Host != "" {
		return strings.ToLower(parsed.Host)
	}

	if at := strings.LastIndex(remoteURL, "@"); at >= 0 {
		remoteURL = remoteURL[at+1:]
	}
	if colon := strings.Index(remoteURL, ":"); colon >= 0 && !strings.Contains(remoteURL[:colon], "/") {
		return strings.ToLower(remoteURL[:colon])
	}

	return strings.ToLower(remoteURL)
}

func runGitHubCloseout(ctx context.Context, worktreePath, remoteName, branch, targetBranch, title, body string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("closeout: gh cli is required for github closeout: %w", err)
	}
	if err := runGit(ctx, worktreePath, "push", remoteName, branch); err != nil {
		return "", fmt.Errorf("closeout: push branch for github: %w", err)
	}
	if err := runGitHubAuth(ctx); err != nil {
		return "", err
	}
	out, err := commandOutput(ctx, worktreePath, "gh", "pr", "create", "--title", title, "--body", body, "--base", targetBranch, "--head", branch, "--json", "url", "--jq", ".url")
	if err != nil {
		return "", fmt.Errorf("closeout: create github pr: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func runGitHubAuth(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("closeout: github auth check failed: %w", err)
	}
	return nil
}

func runGitLabCloseout(ctx context.Context, worktreePath, remoteName, branch, targetBranch, title, body string) error {
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
		return fmt.Errorf("closeout: create gitlab merge request via push options: %w", err)
	}
	return nil
}

func runAzureCloseout(ctx context.Context, worktreePath, remoteName, branch, targetBranch, title, body string) (string, error) {
	if _, err := exec.LookPath("az"); err != nil {
		return "", fmt.Errorf("closeout: az cli is required for azure closeout: %w", err)
	}
	if err := runGit(ctx, worktreePath, "push", remoteName, branch); err != nil {
		return "", fmt.Errorf("closeout: push branch for azure devops: %w", err)
	}
	repository := repositoryNameFromRemote(ctx, worktreePath, remoteName)
	out, err := commandOutput(ctx, worktreePath, "az", "repos", "pr", "create", "--title", title, "--description", body, "--source-branch", branch, "--target-branch", targetBranch, "--repository", repository, "--output", "tsv", "--query", "url")
	if err != nil {
		return "", fmt.Errorf("closeout: create azure pr: %w", err)
	}
	return strings.TrimSpace(out), nil
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

func sectionLines(body string, limit int) []string {
	body = strings.TrimSpace(body)
	if body == "" || limit <= 0 {
		return nil
	}

	lines := make([]string, 0, limit)
	for _, line := range strings.Split(body, "\n") {
		line = compactText(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return lines
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

func commandOutput(ctx context.Context, worktreePath, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
