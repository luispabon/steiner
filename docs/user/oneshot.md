# Oneshot mode

Oneshot runs a task autonomously in three phases: plan, implement, review. Each phase is a fresh agent run with an empty conversation, working in a dedicated git worktree. The result is committed to a feature branch and can be opened as a pull request.

## Requirements

- **Sub-agents enabled** (`sub_agent.enabled: true`). The phase prompts require delegation, so the CLI and the TUI refuse to start or resume a run without it.
- **A model for every phase and the advisor.** Each phase uses its profile assignment or the profile's `default_model`. If one does not resolve, the run stops with a `no model configured` error before any run state is created. The active interactive model is never used as a fallback.
- **Any provider, including `claude_subscription`.** Phases, the advisor, sub-agents, and the compaction fallback can use a `claude_subscription` model. The CLI version and sign-in checks run lazily when a phase runtime first needs the CLI, so a missing or signed-out CLI fails that phase and resume retries it after you fix the login. See [Claude subscription](claude-subscription.md).
- **A git repository.**

## Running

From the command line:

```bash
steiner oneshot "<task>"
steiner --profile careful oneshot "<task>"
steiner oneshot --resume <run-id>
steiner oneshot --list
steiner oneshot --resolve <run-id> --url <url>
steiner oneshot --resolve <run-id> --retry
```

`--list`, `--resume`, `--resolve`, and a task are mutually exclusive. `--url` and `--retry` require `--resolve`, exclude each other, and exactly one of them is required.

From an interactive session:

```text
/oneshot <task>
/oneshot --resume <run-id>
/oneshot-resume
```

`/oneshot-resume` opens a picker of resumable runs.

## What a run does

- **Worktree and branch.** The run works in `.steiner/worktrees/oneshot-<run-id>` on branch `oneshot/<slug>-<run-id>`. It starts from `origin/main` when that ref exists, otherwise from the repository's local `HEAD`. Your working tree is not touched.
- **Run state.** The manifest and lock live in `.steiner/oneshot/<run-id>/`. Planning artifacts and the final report (`final-report.json`) are written inside the worktree under `.steiner/plans/oneshot-<run-id>/`. The manifest also carries finalization progress: `finalization_version`, `report_state`, and `closeout_intent`.
- **Approvals.** Oneshot never prompts and never approves. Edits inside the run worktree need no approval, and every approval request is denied: a `mutate` or `bash` path outside the worktree is refused instead of approved, and a sandbox denial is returned to the model without an unsandboxed retry. Because `paths.project_root_only` defaults to `true`, this refusal is what keeps a run inside its worktree. Setting it to `false` explicitly turns the refusal off, and then a run can write outside the worktree; keep the default for autonomous runs. MCP tools that require approval are denied.
- **Advisor.** The advisor is always available to oneshot phases, using the profile's `advisor` model or `default_model`.
- **Sub-agents.** Children run in the background while the phase continues. A phase finishes only after every child it started has finished and its result is recorded.
- **Budget.** Each phase has the same completion-token budget as a normal run (`limits.max_tokens`), counted across all its turns. If it runs out with children still running, they are cancelled and the phase fails.

When the run finishes, the CLI prints the report path and the manifest as JSON.

## Steering input

While a run started with `/oneshot` is active, the composer sends your input to the current phase as a steering message instead of starting a new turn. A pasted image in a steer is copied into the phase's image store, and the marker for that attachment is rewritten to the phase copy's ID. An image whose copy fails is shown as unavailable with a visible warning while the rest of the steer is still delivered. Other image references in the text are left unchanged; image IDs are phase-local. Only exact `/exit`, `/thinking`, `/accent`, and `/accent <preset>` run as commands; any other input, including other slash commands, is steering.

## Pull request closeout

With `oneshot.auto_pr: true` and a completed review phase, regardless of the review verdict, Steiner pushes the branch and opens a pull or merge request on GitHub, GitLab, or Azure DevOps, using your existing git credentials. GitHub needs the `gh` CLI and Azure DevOps needs `az`. It is off by default. See [Configuration](configuration.md#oneshot-block).

The title is the first line of `overview.md` when that line is an H1 heading; otherwise the task summary, then the run slug, then `oneshot closeout`. The body is `overview.md` without its H1, followed by the review outcome after a `---` separator when there is one.

## Models

Per-phase models live in the selected profile's `oneshot` map; unset phases use the profile's `default_model`. Select a profile with `--profile <name>`.

```yaml
models:
  profiles:
    default:
      default_model: local/<model-id>
      oneshot:
        plan: local/<planner-model-id>
        implement: local/<implementer-model-id>
        review: local/<reviewer-model-id>
```

See [Provider and model setup](provider-and-model-setup.md).

## Listing and resuming

`steiner oneshot --list` shows resumable runs, newest first:

```text
#    Run ID           Phase                Status             Lock         Updated            Task
```

A run is listed when its manifest is readable, its lock is absent or stale (no update for 15 minutes), and work remains: a phase is incomplete, the final report still has to be written, the closeout outcome is unknown, or you authorised one more closeout attempt. **Status** reads one of:

- `resume at <phase>`: a phase is incomplete.
- `finish report`: every phase is done and `final-report.json` still has to be written.
- `resolve closeout`: the closeout outcome is unknown and needs [manual resolution](#resolving-an-unknown-closeout).
- `retry closeout`: you authorised one closeout attempt, which the next resume runs.

`(stale lock)` is appended when a stale lock will be reclaimed. Runs held by a live process are not listed, and neither are runs with a recorded closeout result and a written report.

`--resume <run-id>` continues from the first incomplete phase without re-running finished ones. When every phase is done, resume only finalizes: it writes the final report, and it runs closeout again only for a run you authorised with `--resolve <run-id> --retry`. An unknown closeout outcome is never retried automatically; resume returns an error naming the run and points at resolution instead.

### Resolving an unknown closeout

A run reaches this state when a closeout request was interrupted or failed after it may have reached the provider, so Steiner cannot tell whether a pull or merge request exists. A manifest written before Steiner recorded finalization progress is treated the same way, because it cannot prove closeout did not run. Steiner never checks the provider for an existing request.

Check the provider first, then record the outcome:

- `steiner oneshot --resolve <run-id> --url <url>` records a pull or merge request you found. The URL must be `http` or `https` with a host.
- `steiner oneshot --resolve <run-id> --retry` authorises one more closeout attempt, for when you confirmed that no request exists.

Resolution takes the same exclusive run lock as `--resume`, refuses a run that is not awaiting manual resolution, and never contacts the provider itself. `final-report.json` in the worktree stays valid either way.

## Failures and interruptions

- A failed phase is recorded in the manifest with a timestamp. Inspect the worktree, fix what you need, then resume.
- SIGINT stops the run; resume picks up at the interrupted phase.
- If a phase is interrupted while children are running, resume restarts the phase and does not recover their results. It lists the worktree of each `code` or `documentor` child that was still running so you can salvage uncommitted work.
- A resumed phase starts a new session. The earlier attempt's session stays on disk.
