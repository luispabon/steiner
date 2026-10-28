# Oneshot internals

User guide: [Oneshot](../user/oneshot.md).

Phase behaviour comes from the prompts [`plan.md`](../../internal/oneshot/prompts/plan.md), [`implement.md`](../../internal/oneshot/prompts/implement.md), and [`review.md`](../../internal/oneshot/prompts/review.md). This page covers only what the engine enforces.

Code: `internal/oneshot` (orchestration), `cmd/steiner` (`cmd_oneshot.go`, `phase_driver.go`, `runtime_build.go`), and `internal/interactive` (`phase_control.go`). Model configuration is in [configuration](../user/configuration.md).

## Preflight

- `NewOrchestrator` calls `RequireModels` (`preflight.go`) before any run state exists. Each phase needs a model reference from its `oneshot` assignment, falling back to `default_model`. The advisor uses its own assignment, falling back to `default_model`. A blank reference returns `config.ErrNoModelConfigured`. Nothing falls back to the active orchestrator model.
- `buildRuntimeWithRoots` runs the same check through `preflightRuntimeModels`, so an empty config fails before the worktree, lock, or manifest exists. Errors wrap as `oneshot <phase> phase model: %w` or `oneshot advisor model: %w`.
- `runOneshotTask` and `runOneshotResume` then call `RequireSubAgents` (fails when sub-agents are disabled) and `RequireProviders` (rejects `claude_subscription` in the default, advisor, oneshot, and sub-agent aliases). `newPhaseRunner` also rejects `claude_subscription` in its provider factory with `ErrClaudeSubscription`.

## Run identity and provisioning

The run ID is 8 random bytes, hex-encoded (`generateRunID`). The slug comes from `SlugFromTask`: lowercased, runs of non-alphanumerics become `-`, capped at 48 bytes, and `run` when empty.

| Item | Value |
|---|---|
| Branch | `oneshot/<slug>-<id>` |
| Worktree | `<project>/.steiner/worktrees/oneshot-<id>` |
| State dir | `<project>/.steiner/oneshot/<id>` (holds `run.json` and `run.lock`) |
| Planning dir | `<worktree>/.steiner/plans/oneshot-<id>` |

`ProvisionWorktree` (`worktree.go`):

- The start point is the `origin/main` commit when it resolves, otherwise `HEAD`. Its SHA is stored as `worktree_base`.
- An existing `refs/heads/<branch>` is checked out. Otherwise the branch is created with `-b`.
- A stale worktree path is pruned and removed first.
- `ensureSteinerProjectDir` writes `.steiner/.gitignore` containing `*`.

`Orchestrator.Run` order: provision the worktree, acquire the run lock (`Release` runs on return, via `defer`), derive the interrupt context, create the planning dir, write the manifest with every phase `pending`, run each phase, then `finalizeRun`. Concurrent runs never share a branch, worktree, manifest, or lock because run IDs are unique.

## Run lock

`acquireRunLock` (`lock.go`) creates `run.lock` exclusively. It holds a `LockRecord` with `owner`, `acquired_at`, `updated_at`, and `generation`.

- If the file exists and its mtime is older than `defaultLockStaleAfter` (15 minutes), the caller re-reads it and checks that owner, `updated_at`, and mtime still match. If they match, the stale file is removed and recreated. Three attempts are made, then `errLockHeld` is returned.
- `startPhaseHeartbeat` refreshes the lock every `defaultLockStaleAfter/3` (5 minutes). It also refreshes at phase start and after a phase succeeds.
- `Heartbeat` and `Release` check that owner and `acquired_at` still match. A mismatch returns `errLockLost`. A failed heartbeat cancels the running phase. A lost lock makes `Release` a no-op, and `finalizePhaseFailure` skips its manifest write, so a foreign lock is never modified.

## Sandbox and worktree

Each phase runtime is built by `newPhaseRunner` through `buildRuntimeWithRoots`, with the main checkout as `projectRoot` and the run worktree as `workDir` (`phaseParams`, `cmd_oneshot.go`). `buildRuntimeSandbox` (`runtime_build_sandbox.go`) passes `projectRoot` to `sandbox.New` as the writable root and `workDir` as the `--chdir` target. Sandboxed commands therefore start in the worktree, and the main checkout is the sandbox root.

## Phase execution

Phases run in order: plan, implement, review. `runPhase` builds a fresh runner for each phase:

- A new `PhaseRunner` and `prompt.StaticContextCache`, in workflow mode `prompt.DelegatedChildWorkflowMode()`.
- `WorkDir` is the worktree. `ProjectRoot` stays the main checkout, which holds the manifest. `ProjectAgentsPath` is `<worktree>/AGENTS.md`.
- Approvals: `worktreeAutoApprover` approves only `mutate` path requests inside the worktree (`tool.PathWithinRoot`). Every other approval is denied.
- Advisor: enabled, with `MaxUsesPerRun` per phase: plan 4, implement 3, review 4.
- Cache identity: `SessionID` and `PromptCacheKey` are the run ID. One `agent.NewCacheBaselineStore()` per `Run` or `Resume` call is shared by all phases.
- Seed: one user message from `phaseSeedConversation` with the task, phase, run ID, branch, worktree, and the paths `overview.md` and `plan.yaml` in the planning dir.
- Phase prompt: a core static source (`ContextSourcePhasePrompt`) that bypasses byte budgets. Source order (`source_plan.go`): preamble, agents, project context, skills, phase prompt, session date, conversation.

`CheckBoundary` (`boundary.go`) runs after `RunPhase` returns. It fails when a required artifact is missing or when `git status --porcelain=v1 --untracked-files=all` lists any entry outside `.steiner/`. Required artifacts: plan needs `overview.md` and `plan.yaml`; implement adds `execution.md`; review adds `review.md` and still requires `execution.md`. Commits are not checked.

## Phase driver

`runPhaseOnDriver` (`cmd/steiner/phase_driver.go`) hosts each phase on an `agent.ConversationDriver`:

1. The runtime's `Supervisor` is the driver's `Background` and completion source.
2. The phase prompt is submitted. `WaitQuiescent` waits until the driver is idle with no sub-agent pending. Then the boundary check runs.
3. `MaxTokensPerEpisode` is `limits.max_tokens`. `ErrEpisodeBudgetExhausted` fails the phase.
4. On any failure (budget, run error, cancellation, heartbeat loss, wait error), the supervisor shuts down with `CancelCauseSystem` and then the driver closes. The driver loop is joined before `RunPhase` returns.

`phaseDriverEvents` forwards conversation-state events to the session sink when `RegisterControl` is set (interactive runs). Headless runs drop them.

## Sessions

`newPhaseSession` creates a session titled `<phase> phase: <task>` before the phase runs, records its ID in `phase_session_ids`, and writes the manifest. `PhaseRunInput.Session` carries the ID and a `Save` callback for that session.

Each driver snapshot saves the session with its outstanding sub-agent ledger (`sub_agent_ledger`, `internal/session/session.go`), so a crash mid-phase leaves the ledger on disk. After the phase, `saveResult` saves once more with the last stored ledger. It does nothing when the phase recorded no conversation lineage.

Each phase attempt has one session. Resume starts the interrupted phase with a new session and leaves the old one on disk. Compaction history is archived per phase session. `phaseHistoryArchive` (`cmd/steiner/cmd_oneshot.go`) keys each archive by the phase session ID. Archive storage and write rules are in [Compaction history archive](context-management.md#compaction-history-archive). The phase session archive is not a run-level archive.

## Run manifest

`run.json` is written with `fsatomic.WriteFile` at mode 0600. `created_at` is set once and `updated_at` on every write. The `Manifest` struct (`manifest.go`) defines these keys:

| Key | Notes |
|---|---|
| `run_id`, `slug`, `task`, `branch`, `worktree_path` | Set when the run is created. |
| `worktree_base` | Start commit SHA. Resume fills it when blank. `GenerateFinalReport` requires it. |
| `model_snapshot` | `default_model` and `phase_models` (phase to model reference). |
| `current_phase` | Current phase name. |
| `phase_statuses` | Phase to status (below). |
| `phase_session_ids` | Phase to session ID. |
| `commit_milestones` | `{phase, commit, message, recorded_at}` entries. No code path writes them. |
| `report_path` | Location of `final-report.json`. |
| `closeout_url`, `closeout_provider`, `closeout_state`, `closeout_note` | Closeout result (see Closeout). |
| `created_at`, `updated_at` | Timestamps. |

Phase status values: `pending`, `running`, `done`, `failed`, `skipped`. Resume treats `done` and `skipped` as complete. No code path writes `skipped`.

## Resume

`Orchestrator.Resume` (`resume.go`) serves `steiner oneshot --resume <id>`:

1. Reads `run.json`. Blank `branch` or `worktree_path` is filled from the run identity.
2. Acquires the run lock before checking the worktree. Stale locks are reclaimed as under Run lock.
3. If `worktree_base` is blank, it is set to the current start point and saved to the manifest.
4. `ensureResumeWorktree`: an existing worktree is checked for the expected branch (`verifyWorktree`). A missing worktree is re-provisioned at `worktree_base`.
5. `resumeFromManifest` restarts the first phase that is neither `done` nor `skipped`, with a new session. When no phase is incomplete and `closeout_state` is `failed`, only `finalizeRun` runs.

Before restarting, `reportOrphanedWorktrees` loads the interrupted phase's session (when the session store implements `Load`), reads its `SubAgentLedger`, and emits an `orphaned` phase indicator for each sub-agent entry with a `WorktreePath`.

`ListResumableRuns` labels a run whose resume phase is `closeout` as `retry closeout`, or `retry closeout (stale lock)` when the lock is stale.

The engine never removes worktrees. `CleanupWorktree` (`worktree.go`) has no non-test caller.

### Mid-implement resume

Resume runs no artifact check before the restarted phase. `CheckBoundary` runs only after the phase returns, so a resumed implement phase must still write `execution.md` before it finishes.

## Interrupts and failures

- The run context comes from `InterruptFactory`. The default is `signal.NotifyContext(os.Interrupt)`. The TUI uses `context.WithCancel`.
- `finalizePhaseFailure` marks the phase `failed` and writes the manifest, skipped when the lock was lost. It emits the indicator `cancelled` for lock loss, heartbeat failure, and `RunPhase` errors. It emits `boundary` for runner construction, session creation or save, and `CheckBoundary` failures.
- The run lock is released on return, and the worktree stays in place for inspection or resume.
- A failure report (`tryFailureReport`) is written when `Run` returns an error and the manifest has a run ID (`reportFailureIfNeeded`). `Resume` applies the same condition in a `defer` block.

## Final report

`finalizeRun` writes `final-report.json` to the planning dir (mode 0600) through `WriteFinalReport` (`report.go`), then runs closeout. Fields: `run_id`, `slug`, `task_summary`, `branch`, `worktree_path`, `planning_path`, `report_path`, `current_phase`, `status` (`success` or `failure`), `completion`, `phase_sessions`, `git` (`branch`, `commit`, `short_commit`, `dirty`, `files_changed`), `changes_made`, `files_changed`, `validation`, `review_outcome`, `review_fix_iterations`, `assumptions`, `risks`, `unresolved_issues`, `failure` (only when the run did not pass: `attempted`, `blocked`, `changes_left_behind`, `left_behind_summary`), and `generated_at`. List fields hold at most 8 items. Text fields hold at most 240 runes.

## Closeout

Closeout (`closeout.go`) runs on the host, not in the sandbox.

- Skipped unless `oneshot.auto_pr` is true and the report's `completion` is passing. The state is `skipped` with a note.
- Remote: `branch.<name>.remote` when set, else `origin`. The provider comes from the remote host: `github.com` or `github.*` is GitHub, `gitlab.com` or `gitlab.*` is GitLab, `dev.azure.com` or `visualstudio.com` is Azure DevOps. Any other host sets state `unsupported`.
- Target branch: the branch's upstream (`@{u}`), else `refs/remotes/<remote>/main`, else `master`. If none resolve, closeout fails.
- Title: the first non-blank line of `overview.md` when it is an H1, else `task_summary`, else `slug`, else `oneshot closeout`.
- Body (`assembleCloseoutBody`): `overview.md` minus its leading H1 and the blank lines after it, then a `---` separator and `review.md`. The separator and review are added only when the trimmed review is non-empty. Without a leading H1, the whole trimmed overview is used. Bodies over 60,000 runes are cut at a line boundary with a note naming the planning dir. The user-facing title and body rule is in [Oneshot](../user/oneshot.md).
- GitHub: pushes the branch, then runs `gh pr create` (requires `gh` on `PATH`).
- GitLab: pushes with `merge_request` push options. It returns no URL, so `closeout_url` stays empty and the note says the merge request was created through push options.
- Azure DevOps: pushes, then runs `az repos pr create` (requires `az` on `PATH`).
- States: `created` (with `closeout_url` and `closeout_provider`), `unsupported`, `skipped`, or `failed`. On error the manifest records `closeout_state: failed` and the error in `closeout_note`. The run stays resumable (see Resume).

## TUI integration

Interactive runs use `routeControlToOneshot`:

- Phase events are multiplexed onto the session sink (`output.NewMultiSink`).
- Phase divider: `Phase: <name>` in the scrollback, with no timestamp.
- Status bar: `phase · <name>` while the run is active.
- Sidebar: `Oneshot - <phase>` while the run is active.
- Indicator states emitted by the engine (`events.go`): `starting`, `boundary`, `completed`, `cancelled`, `orphaned`. `phaseIndicatorRunning` is defined but nothing emits it.
- Control routing: one `interactive.PhaseRouter` per run is set with `Session.SetActivePhaseControl` and passed to the orchestrator as `RegisterControl` (`router.Register`). While it is set, the session driver is detached from the steer queue (`DetachSteers`). Release reattaches it (`AttachSteers`). Between phases the router has no target: steers wait in the queue, and cancel requests report that no phase is active.
- The orchestrator runs under `Session.RunBackground`, and `CancelBackground` cancels it. On quit, `runQuitSequence` cancels background runs first, which takes the cancellation path above. `awaitRuns` then joins them.
- Steering-only composer: while a run is active, only `/exit`, `/thinking`, and `/accent` (with or without arguments) run as commands. Other input is sent as a steering message.
- `OneshotFinishedEvent` is emitted from the run goroutine on success and on error. Its handler clears `oneshotRunning` and the phase chrome fields.

## Tests

Integration and resume tests use real git worktrees (`integration_test.go`, `resume_test.go`, `worktree_test.go`).
