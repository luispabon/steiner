package oneshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestAssembleCloseoutBody(t *testing.T) {
	report := FinalReport{
		TaskSummary:   "Ship the parser",
		ReviewOutcome: "review passed",
		Completion:    true,
		Risks:         []string{"regression risk remains"},
	}

	body := AssembleCloseoutBody(
		"Add the parser and wire the command.",
		[]string{"initial parse support", "tightened validation"},
		report,
	)

	for _, want := range []string{
		"Overview",
		"Add the parser and wire the command.",
		"Commit history",
		"- initial parse support",
		"- tightened validation",
		"Review outcome",
		"- outcome: review passed",
		"- risk: regression risk remains",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
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
		CommitLog: "add parser support\ntighten validation\n",
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
			Risks:         []string{"small follow-up risk"},
		},
		Overview: "Implement the parser and keep the flow bounded.",
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
	if !strings.Contains(result.Body, "Commit history") || !strings.Contains(result.Body, "small follow-up risk") {
		t.Fatalf("body missing expected content:\n%s", result.Body)
	}

	log := mustReadFile(t, logPath)
	for _, want := range []string{
		"git config --get branch.feature.remote",
		"git remote get-url origin",
		"git rev-parse --abbrev-ref --symbolic-full-name @{u}",
		"git log --no-merges --format=%s main..feature",
		"git push origin feature",
		"gh auth status",
		"gh pr create --title Ship the parser --body",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestCloseoutUsesGitLabPushOptions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "closeout.log")
	binDir := writeCloseoutScripts(t, logPath, closeoutScriptConfig{
		Branch:    "feature",
		Remote:    "origin",
		RemoteURL: "gitlab.com:group/repo.git",
		Upstream:  "origin/main",
		CommitLog: "gitlab change\n",
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
		CommitLog: "azure change\n",
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
	Branch    string
	Remote    string
	RemoteURL string
	Upstream  string
	CommitLog string
	PRURL     string
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
  log)
    printf '%b' "${FAKE_COMMITS:?}"
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
    if [ "${2:-}" = "create" ]; then
      printf '%s\n' "${FAKE_PR_URL:?}"
      exit 0
    fi
    ;;
esac
exit 1
`)

	writeScript("az", `#!/bin/sh
set -eu
log="${CLOSEOUT_LOG:?}"
printf 'az %s\n' "$*" >> "$log"
if [ "${1:-}" = "repos" ] && [ "${2:-}" = "pr" ] && [ "${3:-}" = "create" ]; then
  printf '%s\n' "${FAKE_PR_URL:?}"
  exit 0
fi
exit 1
`)

	t.Setenv("CLOSEOUT_LOG", logPath)
	t.Setenv("FAKE_REMOTE", cfg.Remote)
	t.Setenv("FAKE_REMOTE_URL", cfg.RemoteURL)
	t.Setenv("FAKE_UPSTREAM", cfg.Upstream)
	t.Setenv("FAKE_COMMITS", cfg.CommitLog)
	t.Setenv("FAKE_PR_URL", cfg.PRURL)
	t.Setenv("FAKE_GH_AUTH_STATUS", "0")

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
