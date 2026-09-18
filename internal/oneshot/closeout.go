package oneshot

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

const (
	closeoutStateSkipped     = "skipped"
	closeoutStateUnsupported = "unsupported"
	closeoutStateCreated     = "created"
	closeoutStateFailed      = "failed"

	// closeoutBodyLimit caps the assembled PR/MR body comfortably under GitHub's
	// 65536-character limit.
	closeoutBodyLimit = 60000
)

type closeoutProvider string

const (
	closeoutProviderGitHub closeoutProvider = "github"
	closeoutProviderGitLab closeoutProvider = "gitlab"
	closeoutProviderAzure  closeoutProvider = "azure"
)

type closeoutRunner func(context.Context, string, string, string, string, string, string) (string, string, error)

var closeoutRunners = map[closeoutProvider]closeoutRunner{
	closeoutProviderGitHub: runGitHubCloseout,
	closeoutProviderGitLab: runGitLabCloseout,
	closeoutProviderAzure:  runAzureCloseout,
}

// CloseoutInput carries the on-disk artifacts needed to build the final PR/MR body.
type CloseoutInput struct {
	Manifest Manifest
	Report   FinalReport
	Overview string
	Review   string
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

	title := closeoutTitle(input.Overview, input.Report)
	body := assembleCloseoutBody(input.Overview, input.Review, input.Report.PlanningPath)

	runner, ok := closeoutRunners[provider]
	if !ok {
		return CloseoutResult{
			State:  closeoutStateUnsupported,
			Remote: remoteName,
			Note:   "unsupported provider",
		}, nil
	}

	url, note, err := runner(ctx, worktreePath, remoteName, branch, targetBranch, title, body)
	if err != nil {
		return CloseoutResult{State: closeoutStateFailed, Provider: string(provider), Remote: remoteName, TargetBranch: targetBranch, Title: title, Body: body}, err
	}
	result := CloseoutResult{
		State:        closeoutStateCreated,
		Provider:     string(provider),
		Remote:       remoteName,
		TargetBranch: targetBranch,
		Title:        title,
		Body:         body,
		URL:          url,
		Note:         note,
	}
	return result, nil
}

// assembleCloseoutBody combines the overview and review outcome, verbatim,
// into the description text used for the PR/MR body.
func assembleCloseoutBody(overview, review, planningPath string) string {
	body := stripOverviewTitle(overview)

	var b strings.Builder
	b.WriteString(body)
	if reviewText := strings.TrimSpace(review); reviewText != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
		b.WriteString(reviewText)
	}

	result := strings.TrimRight(b.String(), " \t\n")
	return truncateCloseoutBody(result, planningPath)
}

// truncateCloseoutBody caps result at closeoutBodyLimit runes, cutting at the
// last newline before the limit so no line is truncated mid-way.
func truncateCloseoutBody(result, planningPath string) string {
	runes := []rune(result)
	if len(runes) <= closeoutBodyLimit {
		return result
	}

	truncated := string(runes[:closeoutBodyLimit])
	if idx := strings.LastIndex(truncated, "\n"); idx >= 0 {
		truncated = truncated[:idx]
	}
	truncated = strings.TrimRight(truncated, " \t\n")
	return truncated + fmt.Sprintf("\n\n_Body truncated. Full planning artifacts under %s._", planningPath)
}

// overviewTitleLineIndex returns the index of the overview's H1 title line,
// or -1 if none exists. Per prompts/plan.md, the H1 must be the document's
// first non-blank line; a `# ` line appearing later (e.g. inside a fenced
// code block of shell commands) is not a title.
func overviewTitleLineIndex(lines []string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			return i
		}
		return -1
	}
	return -1
}

// stripOverviewTitle removes the first `# ` H1 line and any blank lines
// immediately following it, since the title lives in the PR title field.
func stripOverviewTitle(overview string) string {
	lines := strings.Split(overview, "\n")
	titleIdx := overviewTitleLineIndex(lines)
	if titleIdx < 0 {
		return strings.TrimSpace(overview)
	}

	rest := lines[titleIdx+1:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	return strings.TrimSpace(strings.Join(rest, "\n"))
}

// closeoutTitle names the PR/MR from the overview's H1, falling back to the
// task summary or slug. A missing H1 must not fail a run that already passed
// review, so this always returns a usable title.
func closeoutTitle(overview string, report FinalReport) string {
	lines := strings.Split(overview, "\n")
	if titleIdx := overviewTitleLineIndex(lines); titleIdx >= 0 {
		trimmed := strings.TrimSpace(lines[titleIdx])
		if title := compactText(strings.TrimPrefix(trimmed, "# ")); title != "" {
			return title
		}
	}

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

// commandError carries a command's real stderr alongside the formatted error
// text, so callers can classify failures on stderr instead of the formatted
// message (which embeds argv, including user-controlled PR body content).
type commandError struct {
	name   string
	args   []string
	err    error
	stdout string
	stderr string
}

func (e *commandError) Error() string {
	detail := strings.TrimSpace(e.stderr)
	if detail == "" {
		detail = strings.TrimSpace(e.stdout)
	}
	var cappedArgs []string
	for _, arg := range e.args {
		if len(arg) > 200 {
			cappedArgs = append(cappedArgs, arg[:200]+"...")
		} else {
			cappedArgs = append(cappedArgs, arg)
		}
	}
	return fmt.Sprintf("%s %s: %s: %s", e.name, strings.Join(cappedArgs, " "), e.err, detail)
}

func (e *commandError) Unwrap() error {
	return e.err
}

func commandOutput(ctx context.Context, worktreePath, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = worktreePath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", &commandError{
			name:   name,
			args:   args,
			err:    err,
			stdout: stdout.String(),
			stderr: stderr.String(),
		}
	}
	return stdout.String(), nil
}
