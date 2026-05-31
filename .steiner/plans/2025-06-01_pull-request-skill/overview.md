# Overview: Built-in /pull-request Skill

## Request

Issue #95: Add a `/pull-request` built-in skill that:

1. Understands the repo's remote and branching structure
2. Generates an appropriate PR title and description from the branch changes
3. Creates the pull request on the remote

The skill must be standalone and independent of the `/review` skill. Users should not be expected to run the full plan-implement-review loop to create a PR.

## Overview

Add a new bundled skill `pull-request` by creating `skills/pull-request/SKILL.md`. The skill content instructs the model to:

- Detect the Git hosting provider from `git remote get-url origin` (or tracking remote) using hostname heuristics.
- Select the target branch (upstream base → `origin/main` → `origin/master` → ask user).
- Generate a PR/MR title and body from commit messages on the current branch, falling back to targeted diffs.
- Use the provider-appropriate CLI command to create the PR/MR, with per-provider auth checks and fallback behavior.
- Ask for user confirmation before pushing or creating.

**Scope boundaries:**
- Create only the skill Markdown file; no Go code changes are required because the skill loader auto-discovers bundled skills via `go:embed */SKILL.md`.
- Do not modify the `/review` skill.
- The skill is prompt-instruction only; it does not add new Go types, CLI flags, or TUI wiring.

**What will change:**
- New file: `skills/pull-request/SKILL.md`
- The skill will appear automatically in `steiner skills` and the TUI slash overlay after the next build.

**Risks:**
- Bitbucket Cloud and Server lack official CLIs; the skill must rely on `curl` REST API calls, which require auth setup the skill cannot automate.
- Self-hosted provider detection is heuristic-based and may misidentify; the skill wording must allow override.
- The skill content must stay concise enough to fit within the context budget alongside other enabled skills.

## Verification Strategy

| Check | Command | Cost | Notes |
|---|---|---|---|
| Build | `make build-binaries` | Cheap | Confirms `go:embed` picks up the new skill directory |
| Skills list | `./bin/steiner skills` | Cheap | Should list `pull-request` after build |
| Unit tests | `go test ./internal/skill/...` | Cheap | Existing loader tests should still pass |
| Full suite | `make check` | Expensive | Run before handoff |

No new tests are strictly required because the skill is pure Markdown and the loader already tests bundled FS discovery. A sanity check that the skill loads without parse errors is sufficient.

## Decision Log

| Decision | Choice | Rationale |
|---|---|---|
| Research required? | Yes, approved | Needed to determine which providers to support, exact CLI commands, auto-detection heuristics, and fallback behavior across GitHub, GitLab, Azure DevOps, Bitbucket, and Gitea/Codeberg. |
| Standalone vs integrated with `/review` | Standalone | User explicitly requested independence from the plan-implement-review loop; `/review` remains untouched. |
| Providers to support | GitHub, GitLab, Azure DevOps, Bitbucket Cloud, Bitbucket Server/DC, Gitea/Codeberg/Forgejo | Covers the vast majority of Git hosting used by Steiner's target audience. |
| Bitbucket primary path | `curl` REST API | No official CLI exists; third-party `atlassian-cli` is not guaranteed to be installed. |
| GitLab preferred path | `git push` push-options | Zero extra tooling required; works on any GitLab instance. |
| Detection approach | Hostname heuristics from `git remote get-url origin` | Same pattern used by Git Credential Manager; good enough for a skill without HTTP probing. |
