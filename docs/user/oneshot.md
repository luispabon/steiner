# Oneshot Mode

Oneshot is a headless autonomous orchestration mode that runs steiner's agent loop as three distinct phases: plan, implement, review. Each phase is a fresh agent run with empty model context against a dedicated git worktree that starts from `origin/main` when that ref exists and falls back to the repository's local `HEAD` when it does not. Results are committed to a feature branch, and optionally pushed as a pull request.

Oneshot requires `sub_agent.enabled: true`: the plan, implement, and review phase prompts mandate delegation, so both the CLI (`steiner oneshot`, `--resume`) and the TUI (`/oneshot`, `/oneshot-resume`) refuse to start when sub-agents are disabled in config.

## Invocation

**Headless CLI**:

```bash
steiner oneshot "<task>"
steiner --profile fast oneshot "<task>"
steiner oneshot --resume <id>
steiner oneshot --list
```

**Interactive TUI** (from within `steiner`):

```text
/oneshot <task>
/oneshot --resume <id>
/oneshot --list
```

## Configuration

The `oneshot` config block controls PR closeout behavior. Per-phase model assignments live in the selected profile's `oneshot` map. Use raw `provider/model-id` references by default. Use an alias only when it supplies a stable name or persistent `ModelConfig` settings. `models.profiles.default` is required, and named profiles are partial overlays:

```yaml
providers:
  local:
    type: ollama
    base_url: http://localhost:11434/v1

oneshot:
  auto_pr: false

models:
  profiles:
    default:
      default_model: local/<model-id>
      oneshot:
        plan: local/<planner-model-id>
        implement: local/<implementer-model-id>
        review: local/<reviewer-model-id>
    careful:
      default_model: local/<model-id>
```

- Omitted phase assignments fall back to the selected profile's `default_model`.
- Select a profile at startup with `--profile <name>` for oneshot, interactive, or `--exec` runs.
- `auto_pr` is optional and defaults to `false`.

When `auto_pr` is true and review passes, the opened PR/MR is titled from the first H1 heading the plan phase writes to `overview.md`, falling back to the task string if no H1 is found. Its body strips the first H1 from `overview.md`, then appends the review outcome from `review.md` after a separator.

## Listing and resumable runs

`steiner oneshot --list` displays all resumable runs:

```text
  ID          Slug               Status      Phase
  abc123      refactor-auth      incomplete  implement
  def456      add-logging        completed   review
```

A run is resumable if its manifest exists and is readable, its branch exists, at least one phase is incomplete, and its lock is absent or stale. A run is `completed` when all three phases are complete.

## Sub-agents in the background

Sub-agents launched during a phase run in the background. The phase model keeps working, and each sub-agent's result is delivered to it as soon as it finishes. A phase completes only after every sub-agent it started has finished and its result has been recorded, so nothing is left running when the next phase begins.

Each phase has the same completion-token budget as a normal run (`limits.max_tokens`), counted across every turn the phase takes, including turns woken by sub-agent results. If the budget runs out while sub-agents are still running, the phase fails: the remaining sub-agents are cancelled and the failure is recorded in the manifest. The model is not told. Resume with `--resume <id>` to re-run the phase.

If a phase is interrupted while sub-agents are running, resume restarts the phase and does not deliver their lost results. It reports the worktree of each `code` sub-agent that was still running, so you can inspect or salvage its uncommitted work. Sub-agents without a worktree are not listed.

## Error handling

If a phase fails, the failure is recorded in the manifest with a timestamp. SIGINT aborts without rerunning previous phases. Inspect the worktree, fix issues, and resume with `--resume <id>`. If the run finished but closeout (PR/MR push) failed, the manifest records `closeout_state: failed` and `--resume <id>` re-runs only closeout.

For architecture, manifest schema, and phase contracts, see [Oneshot Internals](../internals/oneshot.md).
