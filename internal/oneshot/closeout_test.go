package oneshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestCloseoutTitleFromOverviewH1(t *testing.T) {
	tests := []struct {
		name     string
		overview string
		report   FinalReport
		want     string
	}{
		{
			name:     "h1 present is used",
			overview: "# Add the parser\n\n## Request\nsomething\n",
			report:   FinalReport{TaskSummary: "fallback"},
			want:     "Add the parser",
		},
		{
			name:     "h2 heading is not treated as h1",
			overview: "## Not a title\n\nsome text\n",
			report:   FinalReport{TaskSummary: "fallback summary"},
			want:     "fallback summary",
		},
		{
			name:     "h1 not on first line is ignored",
			overview: "some preamble\n\n# Real title\n\nmore text\n",
			report:   FinalReport{TaskSummary: "fallback"},
			want:     "fallback",
		},
		{
			name:     "leading blank lines before the h1 is still found",
			overview: "\n\n  \n# Real title\n\nmore text\n",
			report:   FinalReport{TaskSummary: "fallback"},
			want:     "Real title",
		},
		{
			name:     "no h1 falls back to task summary",
			overview: "no heading here\n",
			report:   FinalReport{TaskSummary: "Ship the parser"},
			want:     "Ship the parser",
		},
		{
			name:     "no h1 but a fenced shell comment falls back to task summary",
			overview: "## Verification Strategy\n\n```bash\n# make test\ngo test ./...\n```\n",
			report:   FinalReport{TaskSummary: "Ship the parser"},
			want:     "Ship the parser",
		},
		{
			name:     "no h1 and no task summary falls back to slug",
			overview: "",
			report:   FinalReport{Slug: "ship-parser"},
			want:     "ship-parser",
		},
		{
			name:     "everything empty falls back to default",
			overview: "",
			report:   FinalReport{},
			want:     "oneshot closeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := closeoutTitle(tt.overview, tt.report); got != tt.want {
				t.Fatalf("closeoutTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAssembleCloseoutBody(t *testing.T) {
	overview := "# Add the parser\n\n## Request\nsomething\n\n## Overview\nAdd the parser and wire the command.\n\n## Key Decisions\nD1: use recursive descent.\n"
	review := "## Verdict\npassed\n\n## Findings\nnone\n"

	body := assembleCloseoutBody(overview, review, "/tmp/planning")

	if strings.Contains(body, "# Add the parser\n") {
		t.Fatalf("body should not contain the H1 title line:\n%s", body)
	}
	for _, want := range []string{
		"## Request",
		"something",
		"## Overview",
		"Add the parser and wire the command.",
		"## Key Decisions",
		"D1: use recursive descent.",
		"---",
		"## Verdict",
		"passed",
		"## Findings",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Commit history") {
		t.Fatalf("body should not contain a commit history section:\n%s", body)
	}
}

func TestAssembleCloseoutBodyNoReview(t *testing.T) {
	overview := "# Title\n\nBody text.\n"
	body := assembleCloseoutBody(overview, "", "/tmp/planning")
	if strings.Contains(body, "---") {
		t.Fatalf("body should not contain a review separator when review is empty:\n%s", body)
	}
	if !strings.Contains(body, "Body text.") {
		t.Fatalf("body missing overview text:\n%s", body)
	}
}

func TestAssembleCloseoutBodyTruncation(t *testing.T) {
	var lines []string
	line := strings.Repeat("x", 100)
	for i := 0; i < 700; i++ {
		lines = append(lines, line)
	}
	overview := "# Title\n\n" + strings.Join(lines, "\n") + "\n"

	body := assembleCloseoutBody(overview, "", "/tmp/planning-path")

	if len([]rune(body)) > closeoutBodyLimit+200 {
		t.Fatalf("body not capped near limit: got %d runes", len([]rune(body)))
	}
	if !strings.Contains(body, "Body truncated") || !strings.Contains(body, "/tmp/planning-path") {
		t.Fatalf("body missing truncation notice:\n%s", body[len(body)-200:])
	}
	if strings.HasSuffix(strings.TrimSuffix(body, "\n"), "x") {
		t.Fatalf("truncation should cut at a line boundary, not mid-line")
	}
}

func TestAssembleCloseoutBodyUnderLimitUnchanged(t *testing.T) {
	overview := "# Title\n\nShort body.\n"
	review := "review notes\n"
	body := assembleCloseoutBody(overview, review, "/tmp/planning")
	want := "Short body.\n\n---\n\nreview notes"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestAssembleCloseoutBodyNoH1LeavesFencedBlockIntact(t *testing.T) {
	overview := "## Verification Strategy\n\n```bash\n# make test\ngo test ./...\n```\n"
	body := assembleCloseoutBody(overview, "", "/tmp/planning")
	if !strings.Contains(body, "```bash\n# make test\ngo test ./...\n```") {
		t.Fatalf("fenced block should be left intact when there is no h1:\n%s", body)
	}
}

func TestCloseoutSkipsWhenAutoPRDisabled(t *testing.T) {
	projectRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		RemoteURL: "git@github.com:owner/repo.git",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := Closeout(context.Background(), config.Config{}, CloseoutInput{
		Manifest: Manifest{WorktreePath: filepath.Join(projectRoot, "worktree"), Branch: "feature"},
		Report:   FinalReport{Completion: true},
	})
	if err != nil {
		t.Fatalf("Closeout failed: %v", err)
	}
	if got, want := result.State, closeoutStateSkipped; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("expected no command execution, log status = %v", err)
	}
}

func TestCloseoutCreatesGitHubPr(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:    "feature",
		Remote:    "origin",
		RemoteURL: "git@github.com:owner/repo.git",
		Upstream:  "origin/main",
		PRURL:     "https://github.com/owner/repo/pull/42",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	result, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{
			WorktreePath: worktreePath,
			Branch:       "feature",
		},
		Report: FinalReport{
			Completion:    true,
			TaskSummary:   "Ship the parser",
			ReviewOutcome: "review passed",
		},
		Overview: "# Ship the parser\n\nImplement the parser and keep the flow bounded.",
		Review:   "## Verdict\npassed\n",
	})
	if err != nil {
		t.Fatalf("Closeout failed: %v", err)
	}
	if got, want := result.State, closeoutStateCreated; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := result.Provider, string(closeoutProviderGitHub); got != want {
		t.Fatalf("provider = %q, want %q", got, want)
	}
	if got, want := result.TargetBranch, "main"; got != want {
		t.Fatalf("target branch = %q, want %q", got, want)
	}
	if got, want := result.URL, "https://github.com/owner/repo/pull/42"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if strings.Contains(result.URL, "Creating pull request") {
		t.Fatalf("url should not contain the stderr progress line: %q", result.URL)
	}
	if !strings.Contains(result.Body, "Implement the parser") || !strings.Contains(result.Body, "## Verdict") {
		t.Fatalf("body missing expected content:\n%s", result.Body)
	}

	log := mustReadFile(t, logPath)
	for _, want := range []string{
		"git config --get branch.feature.remote",
		"git remote get-url origin",
		"git rev-parse --abbrev-ref --symbolic-full-name @{u}",
		"git push origin feature",
		"gh auth status",
		"gh pr create --title Ship the parser --body",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "--json") {
		t.Fatalf("log should not contain --json on the pr create call:\n%s", log)
	}
	if strings.Contains(log, "git log") {
		t.Fatalf("log should not contain a git log call:\n%s", log)
	}
}

func TestCloseoutGitHubDuplicatePrFallsBackToView(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:           "feature",
		Remote:           "origin",
		RemoteURL:        "git@github.com:owner/repo.git",
		Upstream:         "origin/main",
		PRURL:            "https://github.com/owner/repo/pull/42",
		GHPRCreateStatus: "1",
		GHPRCreateStderr: "a pull request for branch \"feature\" into branch \"main\" already exists",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	result, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{WorktreePath: worktreePath, Branch: "feature"},
		Report: FinalReport{
			Completion:    true,
			TaskSummary:   "Ship the parser",
			ReviewOutcome: "review passed",
		},
		Overview: "# Ship the parser\n\nBody.",
	})
	if err != nil {
		t.Fatalf("Closeout failed: %v", err)
	}
	if got, want := result.State, closeoutStateCreated; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := result.URL, "https://github.com/owner/repo/pull/42"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if got, want := result.Note, "pull request already existed"; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}

	log := mustReadFile(t, logPath)
	if !strings.Contains(log, "gh pr view feature --json url --jq .url") {
		t.Fatalf("log missing pr view fallback call:\n%s", log)
	}
}

func TestCloseoutGitHubOtherCreateFailureIsFatal(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:           "feature",
		Remote:           "origin",
		RemoteURL:        "git@github.com:owner/repo.git",
		Upstream:         "origin/main",
		PRURL:            "https://github.com/owner/repo/pull/42",
		GHPRCreateStatus: "1",
		GHPRCreateStderr: "authentication required",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	_, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{WorktreePath: worktreePath, Branch: "feature"},
		Report: FinalReport{
			Completion:    true,
			TaskSummary:   "Ship the parser",
			ReviewOutcome: "review passed",
		},
		Overview: "# Ship the parser\n\nBody.",
	})
	if err == nil {
		t.Fatalf("expected Closeout to fail")
	}

	log := mustReadFile(t, logPath)
	if strings.Contains(log, "pr view") {
		t.Fatalf("log should not contain a pr view fallback call:\n%s", log)
	}
}

// TestCloseoutGitHubBodyContainingAlreadyExistsIsNotMisclassified guards
// against classifying duplicate-PR failures by substring-matching the
// formatted error (which embeds argv, including the PR body). A real create
// failure whose stderr says nothing about a duplicate PR must stay fatal even
// when the model-authored overview/review body happens to contain the
// literal phrase "already exists".
func TestCloseoutGitHubBodyContainingAlreadyExistsIsNotMisclassified(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:           "feature",
		Remote:           "origin",
		RemoteURL:        "git@github.com:owner/repo.git",
		Upstream:         "origin/main",
		PRURL:            "https://github.com/owner/repo/pull/42",
		GHPRCreateStatus: "1",
		GHPRCreateStderr: "authentication required",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	_, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{WorktreePath: worktreePath, Branch: "feature"},
		Report: FinalReport{
			Completion:    true,
			TaskSummary:   "Ship the parser",
			ReviewOutcome: "review passed",
		},
		Overview: "# Ship the parser\n\nReuse the helper, it already exists in the utils package.",
	})
	if err == nil {
		t.Fatalf("expected Closeout to fail")
	}

	log := mustReadFile(t, logPath)
	if strings.Contains(log, "pr view") {
		t.Fatalf("log should not contain a pr view fallback call:\n%s", log)
	}
}

func TestCloseoutUsesGitLabPushOptions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:    "feature",
		Remote:    "origin",
		RemoteURL: "gitlab.com:group/repo.git",
		Upstream:  "origin/main",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	result, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{
			WorktreePath: worktreePath,
			Branch:       "feature",
		},
		Report: FinalReport{
			Completion:    true,
			TaskSummary:   "Ship the parser",
			ReviewOutcome: "review passed",
		},
		Overview: "Implement the parser and keep the flow bounded.",
	})
	if err != nil {
		t.Fatalf("Closeout failed: %v", err)
	}
	if got, want := result.State, closeoutStateCreated; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := result.Provider, string(closeoutProviderGitLab); got != want {
		t.Fatalf("provider = %q, want %q", got, want)
	}
	if got, want := result.Note, "merge request created via git push options"; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}

	log := mustReadFile(t, logPath)
	if !strings.Contains(log, "git push -o merge_request.create -o merge_request.title=Ship the parser") {
		t.Fatalf("gitlab push options missing from log:\n%s", log)
	}
	if strings.Contains(log, "gh pr create") || strings.Contains(log, "az repos pr create") {
		t.Fatalf("unexpected non-gitlab provider command in log:\n%s", log)
	}
}

func TestCloseoutCreatesAzurePr(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:    "feature",
		Remote:    "origin",
		RemoteURL: "https://dev.azure.com/org/project/_git/repo",
		Upstream:  "origin/main",
		PRURL:     "https://dev.azure.com/org/project/_git/repo/pullrequest/7",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	result, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{
			WorktreePath: worktreePath,
			Branch:       "feature",
		},
		Report: FinalReport{
			Completion:    true,
			TaskSummary:   "Ship the parser",
			ReviewOutcome: "review passed",
		},
		Overview: "Implement the parser and keep the flow bounded.",
	})
	if err != nil {
		t.Fatalf("Closeout failed: %v", err)
	}
	if got, want := result.State, closeoutStateCreated; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := result.Provider, string(closeoutProviderAzure); got != want {
		t.Fatalf("provider = %q, want %q", got, want)
	}
	if got, want := result.URL, "https://dev.azure.com/org/project/_git/repo/pullrequest/7"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}

	log := mustReadFile(t, logPath)
	if !strings.Contains(log, "az repos pr create --title Ship the parser --description") {
		t.Fatalf("azure pr create missing from log:\n%s", log)
	}
	if !strings.Contains(log, "git push origin feature") {
		t.Fatalf("azure push missing from log:\n%s", log)
	}
}

func TestCloseoutReportsUnsupportedProvider(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:    "feature",
		Remote:    "origin",
		RemoteURL: "ssh://example.com/owner/repo.git",
		Upstream:  "origin/main",
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := loadCloseoutConfig(t, true)
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	result, err := Closeout(context.Background(), config.Config{
		OneShot: cfg.OneShot,
	}, CloseoutInput{
		Manifest: Manifest{
			WorktreePath: worktreePath,
			Branch:       "feature",
		},
		Report: FinalReport{Completion: true},
	})
	if err != nil {
		t.Fatalf("Closeout failed: %v", err)
	}
	if got, want := result.State, closeoutStateUnsupported; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := result.Note, "unsupported provider for remote host \"example.com\""; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
	log := mustReadFile(t, logPath)
	if strings.Contains(log, "push ") || strings.Contains(log, "pr create") {
		t.Fatalf("unsupported provider should not attempt PR creation:\n%s", log)
	}
}

type closeoutScriptConfig struct {
	Branch           string
	Remote           string
	RemoteURL        string
	Upstream         string
	PRURL            string
	GHPRCreateStatus string
	GHPRCreateStderr string
}

func writeCloseoutScripts(t *testing.T, logPath string, cfg closeoutScriptConfig) string {
	t.Helper()

	binDir := t.TempDir()
	writeScript := func(name, body string) {
		t.Helper()
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s script: %v", name, err)
		}
	}

	writeScript("git", `#!/bin/sh
set -eu
log="${CLOSEOUT_LOG:?}"
if [ "${1:-}" = "-C" ]; then
  shift 2
fi
printf 'git %s\n' "$*" >> "$log"
case "${1:-}" in
  config)
    printf '%s\n' "${FAKE_REMOTE:-origin}"
    ;;
  remote)
    printf '%s\n' "${FAKE_REMOTE_URL:?}"
    ;;
  rev-parse)
    if [ "${2:-}" = "--abbrev-ref" ]; then
      printf '%s\n' "${FAKE_UPSTREAM:?}"
      exit 0
    fi
    ;;
  push)
    exit 0
    ;;
esac
`)

	writeScript("gh", `#!/bin/sh
set -eu
log="${CLOSEOUT_LOG:?}"
printf 'gh %s\n' "$*" >> "$log"
case "${1:-}" in
  auth)
    exit "${FAKE_GH_AUTH_STATUS:-0}"
    ;;
  pr)
    case "${2:-}" in
      create)
        shift 2
        while [ $# -gt 0 ]; do
          case "$1" in
            --title|--body|--base|--head)
              shift 2
              ;;
            --draft)
              shift
              ;;
            *)
              printf 'unknown flag: %s\n' "$1" >&2
              exit 1
              ;;
          esac
        done
        if [ -n "${FAKE_GH_PR_CREATE_STDERR:-}" ]; then
          printf '%s\n' "${FAKE_GH_PR_CREATE_STDERR}" >&2
        fi
        status="${FAKE_GH_PR_CREATE_STATUS:-0}"
        if [ "$status" != "0" ]; then
          exit "$status"
        fi
        printf 'Creating pull request for feature into main\n' >&2
        printf '%s\n' "${FAKE_PR_URL:?}"
        exit 0
        ;;
      view)
        printf '%s\n' "${FAKE_PR_URL:?}"
        exit 0
        ;;
    esac
    ;;
esac
exit 1
`)

	writeScript("az", `#!/bin/sh
set -eu
log="${CLOSEOUT_LOG:?}"
printf 'az %s\n' "$*" >> "$log"
if [ "${1:-}" = "repos" ] && [ "${2:-}" = "pr" ] && [ "${3:-}" = "create" ]; then
  shift 3
  while [ $# -gt 0 ]; do
    case "$1" in
      --title|--description|--source-branch|--target-branch|--repository|--output|--query)
        shift 2
        ;;
      *)
        printf 'unknown flag: %s\n' "$1" >&2
        exit 1
        ;;
    esac
  done
  printf '%s\n' "${FAKE_PR_URL:?}"
  exit 0
fi
exit 1
`)

	t.Setenv("CLOSEOUT_LOG", logPath)
	t.Setenv("FAKE_REMOTE", cfg.Remote)
	t.Setenv("FAKE_REMOTE_URL", cfg.RemoteURL)
	t.Setenv("FAKE_UPSTREAM", cfg.Upstream)
	t.Setenv("FAKE_PR_URL", cfg.PRURL)
	t.Setenv("FAKE_GH_AUTH_STATUS", "0")
	t.Setenv("FAKE_GH_PR_CREATE_STATUS", cfg.GHPRCreateStatus)
	t.Setenv("FAKE_GH_PR_CREATE_STDERR", cfg.GHPRCreateStderr)

	return binDir
}

func loadCloseoutConfig(t *testing.T, autoPR bool) config.Config { //nolint:unparam
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	contents := "oneshot:\n  auto_pr: false\n"
	if autoPR {
		contents = "oneshot:\n  auto_pr: true\n"
	}
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(config.LoadOptions{
		ProjectConfigPath: configPath,
		WorkingDir:        t.TempDir(),
		HomeDir:           t.TempDir(),
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
