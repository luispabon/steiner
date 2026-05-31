---
name: pull-request
description: Create a pull request or merge request from the current branch by detecting the Git host, choosing a target branch, generating a title and body from branch commits, and using the provider-appropriate CLI or API. Use when the user asks to open a PR/MR, submit changes, or create a pull/merge request.
---

# Pull Request / Merge Request Creator

## Overview

Use this skill to create a PR/MR from the current branch. It works independently of the plan-implement-review loop. Users may invoke it at any time.

The skill detects the Git hosting provider, selects a target branch, assembles a title and body from branch history, and creates the PR/MR using the provider-appropriate method. It must ask for user confirmation before pushing or creating.

Stop and report blockers instead of widening scope.

## Input Contract

- Require a branch with commits ahead of the chosen target.
- Detect the remote from the current branch tracking remote, falling back to `origin`.
- Derive the provider from the remote URL hostname.
- Choose the target branch in this order:
  1. upstream or tracking base if clearly known
  2. `origin/main`
  3. `origin/master`
  4. ask the user

## Provider Detection

Inspect `git remote get-url <remote>` and match the hostname heuristically. Allow override if the user corrects the detected provider.

| Hostname pattern | Provider |
|---|---|
| `github.com` | GitHub |
| `gitlab.com` or known self-hosted GitLab | GitLab |
| `dev.azure.com` or `*.visualstudio.com` | Azure DevOps |
| `bitbucket.org` | Bitbucket Cloud |
| `bitbucket.*` or user-declared | Bitbucket Server/DC |
| `gitea.*`, `codeberg.org`, `forgejo.*` | Gitea/Codeberg/Forgejo |

If no pattern matches, ask the user to identify the provider or report that automatic creation is not supported.

## Auth Check

Before attempting creation, verify the expected auth tool is available:

- **GitHub**: `gh` CLI must be installed and authenticated (`gh auth status`).
- **GitLab**: `git` must be able to push to the remote (push-options path requires no extra tool).
- **Azure DevOps**: `az` CLI must be installed and the `devops` extension configured (`az extension list-available`).
- **Bitbucket Cloud / Server**: no official CLI; creation relies on `curl` REST calls with an app password or token. Ask the user to confirm credentials are available.
- **Gitea/Codeberg/Forgejo**: `tea` CLI is preferred; otherwise fall back to `curl` REST calls.

If the preferred tool is missing, attempt the fallback path or report the blocker clearly.

## Body Assembly

Build the PR/MR title and body from:

1. Commit messages on the current branch that are not on the target branch (`git log <target>..<branch> --format=%s`).
2. If commit messages are vague, contradictory, or missing, inspect targeted diffs (`git diff <target>...<branch>`) for the most significant files.
3. If available, include a brief summary of what changed and why.

Keep the body concise. Do not dump full diffs into the description unless the user explicitly asks.

## Per-Provider Creation

Use only the flow matching the detected provider. Confirm before pushing or creating.

### GitHub

1. Push the branch if it is not already on the remote.
2. Run:
   ```
   gh pr create --title "<title>" --body "<body>" --base <target> --head <branch>
   ```
3. If `gh` fails, report the error and suggest manual creation via the web UI.

### GitLab

1. Push the branch with push-options:
   ```
   git push -o merge_request.create -o merge_request.title="<title>" -o merge_request.description="<body>" -o merge_request.target=<target> <remote> <branch>
   ```
2. If the remote rejects push-options, fall back to `glab` CLI if available:
   ```
   glab mr create --title "<title>" --description "<body>" --source-branch <branch> --target-branch <target>
   ```
3. If neither works, report the error and suggest manual creation.

### Azure DevOps

1. Push the branch if it is not already on the remote.
2. Run:
   ```
   az repos pr create --title "<title>" --description "<body>" --source-branch <branch> --target-branch <target> --repository <repo>
   ```
3. If `az repos pr create` fails, report the error and suggest manual creation.

### Bitbucket Cloud

1. Push the branch if it is not already on the remote.
2. Use the Bitbucket Cloud REST API via `curl`:
   ```
   curl -X POST -u <user>:<app_password> \
     -H "Content-Type: application/json" \
     https://api.bitbucket.org/2.0/repositories/<workspace>/<repo>/pullrequests \
     -d '{
       "title": "<title>",
       "description": "<body>",
       "source": { "branch": { "name": "<branch>" } },
       "destination": { "branch": { "name": "<target>" } }
     }'
   ```
3. If credentials are missing or the call fails, report the blocker.

### Bitbucket Server / Data Center

1. Push the branch if it is not already on the remote.
2. Use the Server REST API via `curl`:
   ```
   curl -X POST -u <user>:<token> \
     -H "Content-Type: application/json" \
     https://<host>/rest/api/1.0/projects/<project>/repos/<repo>/pull-requests \
     -d '{
       "title": "<title>",
       "description": "<body>",
       "fromRef": { "id": "refs/heads/<branch>", "repository": { "slug": "<repo>", "project": { "key": "<project>" } } },
       "toRef": { "id": "refs/heads/<target>", "repository": { "slug": "<repo>", "project": { "key": "<project>" } } }
     }'
   ```
3. If credentials or project/repo identifiers are missing, report the blocker.

### Gitea / Codeberg / Forgejo

1. Push the branch if it is not already on the remote.
2. Prefer `tea` CLI if installed:
   ```
   tea pr create --title "<title>" --body "<body>" --base <target> --head <branch>
   ```
3. Otherwise use the Gitea REST API via `curl`:
   ```
   curl -X POST -H "Authorization: token <token>" \
     -H "Content-Type: application/json" \
     https://<host>/api/v1/repos/<owner>/<repo>/pulls \
     -d '{
       "title": "<title>",
       "body": "<body>",
       "head": "<branch>",
       "base": "<target>"
     }'
   ```
4. If the token or API path is unavailable, report the blocker.

## Confirmation Gate

Before pushing or creating:

1. Present the proposed title, target branch, and a one-line summary of the body length.
2. Ask the user to confirm or edit the title.
3. Only proceed after explicit user approval. No implicit assent.

## Completion

After the PR/MR is created, report:

- the provider used
- the target branch
- the PR/MR URL if returned by the tool or API
- any residual blockers (e.g., missing CI checks, draft status)

If creation is unsupported or blocked, report the reason and suggest manual next steps. Unsupported creation does not invalidate the branch or commits.
