# Oneshot mode

Oneshot runs a task autonomously in three phases: plan, implement, review. Each phase is a fresh agent run with an empty conversation, working in a dedicated git worktree. The result is committed to a feature branch and can be opened as a pull request.

## Requirements

- **Sub-agents enabled** (`sub_agent.enabled: true`). The phase prompts require delegation, so the CLI and the TUI refuse to start or resume a run without it.
- **No `claude_subscription` models.** `steiner oneshot` and `--resume` refuse to run if the default model, the advisor, any phase model, or any sub-agent model uses a `claude_subscription` provider. See [Claude subscription](claude-subscription.md).
- **A model for every phase and the advisor.** Each phase uses its profile assignment or the profile's `default_model`. If one does not resolve, the run stops with a `no model configured` error before any run state is created. The active interactive model is never used as a fallback.
- **A git repository.**

## Running

From the command line:

```bash
steiner oneshot "<task>"
steiner --profile careful oneshot "<task>"
steiner oneshot --resume <run-id>
steiner oneshot --list
```

`--list`, `--resume`, and a task are mutually exclusive.

From an interactive session:

```text
/oneshot <task>
/oneshot --resume <run-id>
/oneshot-resume
```

`/oneshot-resume` opens a picker of resumable runs.

## What a run does

- **Worktree and branch.** The run works in `.steiner/worktrees/oneshot-<run-id>` on branch `oneshot/<slug>-<run-id>`. It starts from `origin/main` when that ref exists, otherwise from the repository's local `HEAD`. Your working tree is not touched.
- **Run state.** The manifest and lock live in `.steiner/oneshot/<run-id>/`. Planning artifacts and the final report (`final-report.json`) are written inside the worktree under `.steiner/plans/oneshot-<run-id>/`.
- **Approvals.** Oneshot does not prompt. `mutate` path approvals inside the run worktree are allowed; every other approval request is denied.
- **Advisor.** The advisor is always available to oneshot phases, using the profile's `advisor` model or `default_model`.
- **Sub-agents.** Children run in the background while the phase continues. A phase finishes only after every child it started has finished and its result is recorded.
- **Budget.** Each phase has the same completion-token budget as a normal run (`limits.max_tokens`), counted across all its turns. If it runs out with children still running, they are cancelled and the phase fails.

When the run finishes, the CLI prints the report path and the manifest as JSON.

## Steering input

While a run started with `/oneshot` is active, the composer sends your input to the current phase as a steering message instead of starting a new turn. Only exact `/exit`, `/thinking`, `/accent`, and `/accent <preset>` run as commands; any other input, including other slash commands, is steering.

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

A run is listed when its manifest is readable, its lock is absent or stale (no update for 15 minutes), and either a phase is incomplete or a failed closeout can be retried. **Status** reads `resume at <phase>` or `retry closeout`, with `(stale lock)` appended when a stale lock will be reclaimed. Runs held by a live process are not listed.

`--resume <run-id>` continues from the first incomplete phase without re-running finished ones. If every phase finished but closeout failed, it re-runs only closeout.

## Failures and interruptions

- A failed phase is recorded in the manifest with a timestamp. Inspect the worktree, fix what you need, then resume.
- SIGINT stops the run; resume picks up at the interrupted phase.
- If a phase is interrupted while children are running, resume restarts the phase and does not recover their results. It lists the worktree of each `code` or `documentor` child that was still running so you can salvage uncommitted work.
- A resumed phase starts a new session. The earlier attempt's session stays on disk.
