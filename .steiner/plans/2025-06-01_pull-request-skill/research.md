# Research: Pull Request Skill Provider Coverage and CLI Commands

## Question

How to write a Steiner skill for creating pull requests that works across popular Git hosting providers: which providers to support, exact CLI commands per provider, auto-detection from `git remote -v` output, fallback behavior, and provider-agnostic skill wording.

## Findings

### Providers Worth Supporting

| Provider | Rationale | CLI Tool |
|---|---|---|
| **GitHub** | Dominant; official CLI widely installed | `gh` |
| **GitLab** | Strong #2; push-options require zero extra tools | `git push` options (preferred), `glab` (fallback) |
| **Azure DevOps** | Enterprise standard | `az repos pr` (Azure CLI + devops extension) |
| **Bitbucket Cloud** | Atlassian SaaS | No official CLI; `curl` to REST API or third-party `atlassian-cli` |
| **Bitbucket Server / Data Center** | Self-hosted enterprise | `curl` to REST API only |
| **Gitea / Codeberg / Forgejo** | Growing open-source alternative | `tea` |

### Exact Commands Per Provider

**GitHub**
```bash
gh pr create --title "<title>" --body "<body>" --base <target> --head <branch>
```
Requires `gh auth login`. `--fill` auto-derives from commits.

**GitLab (preferred — zero extra tools)**
```bash
git push -o merge_request.create -o merge_request.target=<target> origin <branch>
```
Fallback if `glab` installed:
```bash
glab mr create --target-branch <target> --title "<title>" --description "<body>"
```

**Azure DevOps**
```bash
az repos pr create --title "<title>" --description "<body>" --source-branch <branch> --target-branch <target>
```
Requires `az` + `azure-devops` extension. May need `--org` / `--project`.

**Bitbucket Cloud**
No official CLI. Options:
- Third-party `atlassian-cli` (`bb`) if installed
- `curl` to `POST /2.0/repositories/{workspace}/{repo_slug}/pullrequests`
Requires app password.

**Bitbucket Server / Data Center**
`curl` to `POST /rest/api/1.0/projects/{projectKey}/repos/{repoSlug}/pull-requests`
Remote format: `https://{host}/scm/{projectKey}/{repoSlug}.git`

**Gitea / Codeberg / Forgejo**
```bash
tea pr create --head <branch> --title "<title>" --description "<body>"
```
Requires `tea login add`.

### Auto-Detection from Git Remote

Parse `git remote get-url origin` (or current tracking remote), extract hostname, match:

| Hostname pattern | Provider |
|---|---|
| `github.com` | GitHub |
| hostname starts with `github.` | GitHub Enterprise |
| `gitlab.com` | GitLab Cloud |
| hostname contains `gitlab` | GitLab self-managed |
| `dev.azure.com` | Azure DevOps |
| `*.visualstudio.com` | Azure DevOps (legacy) |
| `bitbucket.org` | Bitbucket Cloud |
| hostname contains `bitbucket` (not `.org`) | Bitbucket Server/DC |
| `codeberg.org` | Codeberg/Forgejo |
| hostname contains `gitea` | Gitea |

Self-hosted heuristics can produce false positives; the skill should say "detected as likely X" and allow override.

### Fallback Ladder

1. Known provider + CLI installed → use CLI
2. Known provider + CLI missing → offer install command or REST API `curl`
3. Known provider but no CLI/API feasible → construct web URL for manual creation
4. Unknown provider → report unsupported, list supported providers
5. Auth not configured → tell user what auth step is needed

## Implications for Skill Wording

- Use "PR/MR" as generic terminology throughout.
- Make detection a mandatory first step; never ask "which provider?" unless detection fails.
- Prefer GitLab push-options over `glab` since they require zero installation.
- Each provider block should mention auth prerequisites.
- Ask for confirmation before pushing or creating.
- Never guess the provider.

## Risks and Uncertainties

1. **Bitbucket CLI gap**: No official CLI; `atlassian-cli` is third-party and may require purchase. Mitigation: always offer `curl` fallback.
2. **Self-hosted misdetection**: Hostname heuristics can false-positive. Mitigation: phrase as "likely" and allow override.
3. **Azure org/project parsing**: Remote URL embeds org/project; must extract and pass explicitly to `az repos pr create`.
4. **Bitbucket auth complexity**: Bitbucket Cloud uses app passwords, Server may use basic auth or PATs. Skill cannot handle credentials securely; must instruct user to set up auth separately.
5. **Gitea/Forgejo ambiguity**: Both run similar software. `tea` availability is a better signal than hostname alone.

## Sources

- `skills/review/SKILL.md` lines 161–195 (existing PR/MR preparation section)
- `internal/skill/loader.go` (skill discovery and SKILL.md format)
- GitHub CLI manual: `https://cli.github.com/manual/gh_pr_create`
- GitLab push options: `https://docs.gitlab.com/user/project/push_options/`
- GitLab CLI: `https://docs.gitlab.com/cli/mr/create/`
- Azure DevOps CLI: `https://learn.microsoft.com/en-us/cli/azure/repos/pr`
- Bitbucket Cloud REST API: `https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/`
- Bitbucket Server REST API: `https://developer.atlassian.com/server/bitbucket/rest/v805/api-group-pull-requests/`
- Git Credential Manager auto-detection: `https://github.com/git-ecosystem/git-credential-manager/blob/main/docs/autodetect.md`
- Gitea `tea` CLI: `https://gitea.com/gitea/tea`

## Open Questions

1. Should the skill include a "dry-run" mode that prints the command without executing? (Not in review skill currently.)
2. Should detection consider remotes beyond `origin` (e.g., `upstream` for forks)?
3. For Bitbucket Cloud, is `atlassian-cli` widespread enough to list as primary, or should `curl` be primary?
