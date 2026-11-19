# Oneshot internals

User guide: [Oneshot](../user/oneshot.md).

Phase behaviour comes from the prompts [`plan.md`](https://github.com/luispabon/steiner/blob/main/internal/oneshot/prompts/plan.md), [`implement.md`](https://github.com/luispabon/steiner/blob/main/internal/oneshot/prompts/implement.md), and [`review.md`](https://github.com/luispabon/steiner/blob/main/internal/oneshot/prompts/review.md). This page covers only what the engine enforces.

Code: `internal/oneshot` (orchestration), `cmd/steiner` (`cmd_oneshot.go`, `phase_driver.go`, `runtime_build.go`), and `internal/interactive` (`phase_control.go`). Model configuration is in [configuration](../user/configuration.md).

## Preflight

- `NewOrchestrator` calls `RequireModels` (`preflight.go`) before any run state exists. Each phase needs a model reference from its `oneshot` assignment, falling back to `default_model`. The advisor uses its own assignment, falling back to `default_model`. A blank reference returns `config.ErrNoModelConfigured`. Nothing falls back to the active orchestrator model.
- `buildRuntimeWithRoots` runs the same check through `preflightRuntimeModels`, so an empty config fails before the worktree, lock, or manifest exists. Errors wrap as `oneshot <phase> phase model: %w` or `oneshot advisor model: %w`.
- `runOneshotTask` and `runOneshotResume` then call `RequireSubAgents` (fails when sub-agents are disabled). There is no provider-type gate: `claude_subscription` is accepted in every role. `newPhaseRunner` passes the runtime `providerFactory` through unchanged, so each phase, its advisor and sub-agents, and its compaction fallback build the same way.
- `buildRuntimeWithRoots` creates a `ClaudeSubscriptionPool` per phase runtime (`newClaudeSubscriptionPool`) with the phase worktree as the child `WorkDir`. The pool locates and version-checks the CLI lazily on its first acquire, so a missing CLI, an old version, or a signed-out login fails the phase when it first uses the CLI instead of blocking the run before state exists. `closeRuntime` (called when the phase runner finishes) closes the pool and terminates its CLI processes.

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
- `session.EnsureSteinerProjectDir`, shared with the CLI runtime, writes `.steiner/.gitignore` containing `*` in the project root and the worktree, and resume setup reuses it. Symlink rules: [Sessions and persistence](architecture.md#sessions-and-persistence).

`Orchestrator.Run` order: provision the worktree, acquire the run lock (`Release` runs on return, via `defer`), derive the interrupt context, create the planning dir, write the manifest with every phase `pending`, `report_state: pending`, and `finalization_version: 1`, run each phase, then `finalizeRun`. Concurrent runs never share a branch, worktree, manifest, or lock because run IDs are unique.

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
- Approvals: the phase runner gets a nil `tool.ApprovalResponder`. The executor treats that as a denial: a promptable path violation becomes `policy_denied` in `normalizeExecutionInput` and `handleBashDenial` never runs, so no unsandboxed retry is granted. Edits inside the worktree need no approval. An explicit `paths.project_root_only: false` still disables the containment check, so the denial does not apply.
- Advisor: enabled, with `MaxUsesPerRun` per phase: plan 4, implement 3, review 4.
- Cache identity: `SessionID` and `PromptCacheKey` are the run ID. One `agent.NewCacheBaselineStore()` per `Run` or `Resume` call is shared by all phases.
- Seed: one user message from `phaseSeedConversation` with the task, phase, run ID, branch, worktree, and the paths `overview.md` and `plan.yaml` in the planning dir.
- Phase prompt: a core static source (`ContextSourcePhasePrompt`) that bypasses byte budgets. Source order (`source_plan.go`): preamble, agents, project context, skills, phase prompt, session date, conversation.

`CheckBoundary` (`boundary.go`) runs after `RunPhase` returns. It fails when a required artifact is missing or when `git status --porcelain=v1 --untracked-files=all` lists any entry outside `.steiner/`. Required artifacts: plan needs `overview.md` and `plan.yaml`; implement adds `execution.md`; review adds `review.md` and still requires `execution.md`. Commits are not checked.

Committing planning artifacts is prompt-mediated, not engine-enforced. Each phase prompt has the model run `git check-ignore -q .steiner/plans/` from the run worktree and commit the artifacts only when that path is not ignored; implementation changes are committed either way. `EnsureSteinerProjectDir` (`internal/session`) creates `.steiner/.gitignore` holding `*` only when the file is missing, so a project that tracks its own `.steiner/.gitignore` can keep planning artifacts under version control.

## Phase driver

`runPhaseOnDriver` (`cmd/steiner/phase_driver.go`) hosts each phase on an `agent.ConversationDriver`:

1. The runtime's `Supervisor` is the driver's `Background` and completion source.
2. The phase prompt is submitted. `WaitQuiescent` waits until the driver is idle with no sub-agent pending. Then the boundary check runs.
3. `MaxTokensPerEpisode` is `limits.max_tokens`. `ErrEpisodeBudgetExhausted` fails the phase.
4. On any failure (budget, run error, cancellation, heartbeat loss, wait error), the supervisor shuts down with `CancelCauseSystem` and then the driver closes. The driver loop is joined before `RunPhase` returns.

`phaseDriverEvents` forwards conversation-state events to the session sink when `RegisterControl` is set (interactive runs). Headless runs drop them.

Each phase creates its own driver. The driver's input is the phase's fresh seed message (`phaseConversation`, `internal/oneshot/orchestrator.go`), which is the only message in `PhaseRunInput.Conversation`. The driver starts with no history and an empty `ConversationLineage`. `PhaseRunInput` has no `LastMainRequest` field, so `ConversationDriver.LastMainRequest()` starts at zero. Within the phase, the driver carries the last successful main request across episodes (`ConversationDriver.LastMainRequest()`). A compaction that runs before the phase's first successful main call sees a zero value and treats the cache as cold. `RunResult.LastMainRequest` is returned for every phase, but no code in `internal/oneshot` reads it, so the value does not reach the next phase.

Each phase runtime has its own `agent.ImageStore`, and composer markers in steers name the launching session's store instead. `runPhaseOnDriver` therefore installs `phaseSteerImporter` (`cmd/steiner/phase_images.go`) as `DriverOptions.ProcessSteers`: at every steer drain, and in `phaseControl.Submit` for prompts routed to the phase, `importPhaseSteer` decodes each block's data, calls `ImageStore.Import` to write it under the phase store's folder with a media-type extension, and rewrites the marker of that attachment to the phase copy's local ID. A copy that fails becomes `[image unavailable]` with a `conversation_warning`, and the steer text and its other images still deliver. Every other `[img-N]` reference in the text is left unchanged. IDs are phase-local, so a reference the phase does not hold fails its lookup. The source `FilePath` is never used. `ProcessSteers` runs under the driver lock and performs those bounded file writes there, so its warnings are queued for emission instead of emitted in place, and it must do no network or user-facing work. The phase control is registered as a pointer, so `PhaseRouter` release compares identities rather than values, and a gate rejects submits once the phase starts tearing down, warning that the steer was not delivered. Imported files live under the phase runtime's ephemeral image folder, so they do not survive the phase.

`runPhaseOnDriver` returns the phase result as follows:

- Success: the driver snapshot's conversation and lineage, the last assistant reply, the token total and stop reason from the phase's run record, and `LastMainRequest`.
- Failure: a `RunResult` with only `LastMainRequest` set. Conversation, reply, lineage, token count, and stop reason are empty. The returned error is chosen in this order: `ErrEpisodeBudgetExhausted` (wrapped as `run phase`), the run record's error, `ctx.Err()`, then the wait error.

Phase runners build compaction fallback providers through the runtime `providerFactory` carried on `cliRunner` (`cmd/steiner/cmd_oneshot.go`). A `claude_subscription` fallback model builds through the phase runtime's Claude subscription pool, the same path as the phase's main model. A fallback build failure reruns on main with status `skipped` ([context management internals](context-management.md#compaction-fallback)).

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
| `finalization_version`, `report_state`, `closeout_intent` | Finalization progress (see Final report). All three are optional, so a manifest without them decodes as a legacy run. |
| `created_at`, `updated_at` | Timestamps. |

Phase status values: `pending`, `running`, `done`, `failed`, `skipped`. Resume treats `done` and `skipped` as complete. No code path writes `skipped`.

## Resume

`Orchestrator.Resume` (`resume.go`) serves `steiner oneshot --resume <id>`:

1. Reads `run.json` through `readResumeManifest`. This pre-lock copy serves only the early error checks in that helper, an unreadable manifest or an empty run ID; nothing after the lock acts on it.
2. Acquires the run lock before checking the worktree, then re-reads the manifest under it through `readResumeManifest`, which also fills a blank `branch` or `worktree_path` from the run identity. Classification, worktree handling, and finalization use that under-lock copy, so a manual resolution that lands between the first read and lock acquisition is not overwritten. Stale locks are reclaimed as under Run lock.
3. If `worktree_base` is blank, it is set to the current start point and saved to the manifest.
4. `ensureResumeWorktree`: an existing worktree is checked for the expected branch (`verifyWorktree`). A missing worktree is re-provisioned at `worktree_base`.
5. `resumeFromManifest` restarts the first phase that is neither `done` nor `skipped`, with a new session. Finished phases are never rerun. With no incomplete phase it calls `finalizeRun` only when `reportPending`, `closeoutOutcomeUnknown`, or `closeoutRetryable` holds; otherwise it returns an error that the run is already complete. That gate is the only duplicate-call protection: `finalizeRun` has no settled-state early return. An unknown outcome passes the gate, and `finalizeRun` returns the manual-resolution error without calling `Closeout`; a `retry_authorized` manifest is the only state that reaches the closeout call again.

Before restarting, `reportOrphanedWorktrees` loads the interrupted phase's session (when the session store implements `Load`), reads its `SubAgentLedger`, and emits an `orphaned` phase indicator for each sub-agent entry with a `WorktreePath`.

`ListResumableRuns` (`list.go`) assigns a synthetic resume phase when every phase is complete: `report` when the report is pending (`reportPending`), `resolve` when the closeout outcome is unknown (`closeoutOutcomeUnknown`), and `closeout` when an operator authorised a retry (`closeoutRetryable`, which also requires every phase complete). `resumableRunStatus` renders them as `finish report`, `resolve closeout`, and `retry closeout`, and an incomplete phase as `resume at <phase>`, appending ` (stale lock)` for a stale lock. Runs with a recorded closeout result and a written report are not listed, and live-locked runs are skipped.

The engine never removes worktrees. `CleanupWorktree` (`worktree.go`) has no non-test caller.

### Mid-implement resume

Resume runs no artifact check before the restarted phase. `CheckBoundary` runs only after the phase returns, so a resumed implement phase must still write `execution.md` before it finishes.

## Interrupts and failures

- The run context comes from `InterruptFactory`. The default is `signal.NotifyContext(os.Interrupt)`. The TUI uses `context.WithCancel`.
- `finalizePhaseFailure` marks the phase `failed` and writes the manifest, skipped when the lock was lost. It emits the indicator `cancelled` for lock loss, heartbeat failure, and `RunPhase` errors. It emits `boundary` for runner construction, session creation or save, and `CheckBoundary` failures.
- The run lock is released on return, and the worktree stays in place for inspection or resume.
- A failure report (`tryFailureReport`) is written when `Run` returns an error and the manifest has a run ID (`reportFailureIfNeeded`). `Resume` applies the same condition in a `defer` block. It does nothing once `report_state` is `written`, so a failed closeout or a failed persistence step leaves `final-report.json` untouched.

## Final report

Finalization progress lives in three optional manifest keys: `finalization_version` (`1`, stamped when the run is created, before any phase runs), `report_state` (`pending` when the run is created, `written` once `final-report.json` is on disk), and `closeout_intent` (`pending` while an external closeout may have started without a recorded result).

`closeoutOutcomeUnknown` (`orchestrator.go`) is true for a pending `closeout_intent` or a persisted `closeout_state: unknown`. A manifest with `finalization_version` set is otherwise settled. A manifest without it is unknown only when `closeout_state` is empty or `failed`.

`finalizeRun` decides `manualRequired := closeoutOutcomeUnknown(*manifest)` before it touches the version fields, because writing them records that an earlier closeout outcome is unknown. It then calls `GenerateFinalReport`, which writes `final-report.json` to the planning dir through `WriteFinalReport` (`report.go`, mode 0600); a generation error returns and persists nothing. Progress is persisted before each external effect:

1. One write sets `report_path`, `report_state: written`, and `finalization_version: 1`, and records the closeout posture. When `manualRequired` holds, it sets `closeout_state: unknown` with the note `closeout outcome unknown; manual resolution required`, and leaves any pre-existing pending intent in place, since that intent is one of the reasons the run is ambiguous. Otherwise it sets `closeout_intent: pending` when `closeoutExternalAttempt` (`closeout.go`) reports that `Closeout` can reach a provider runner. That helper returns false, with the note `Closeout` records instead, when `oneshot.auto_pr` is off or the report is not a completed run. The posture shares the write with the report state, so a crash cannot leave a versioned manifest that looks settled while an external request is still possible. A run whose closeout is provably skipped sets no intent and leaves any existing `closeout_state` unchanged.
2. When `manualRequired` holds, the write above has already persisted the unknown marker. A `boundary` indicator is emitted, and the call returns an error naming the run and pointing at `steiner oneshot --resolve`. `Closeout` is not called.
3. `overview.md` and `review.md` are read from the planning dir.
4. `Closeout` is called with the manifest, the report, and those two documents.
5. A closeout error records `closeout_state: unknown` with the error as the note and leaves any pending intent in place, then returns the error.
6. A closeout result records `closeout_url`, `closeout_provider`, `closeout_state`, and `closeout_note`, and clears `closeout_intent`.

A failed write returns its error, with the indicator `persist report state`, and stops the sequence, leaving the previous manifest state, so closeout is not attempted after a failed state write.

For a run that is not already ambiguous and whose closeout can reach a provider runner, the single write records the written report and a pending `closeout_intent`, so a crash after it and before `Closeout` leaves the run needing manual resolution even though no request may exist; over-reporting is the intended direction. On the ambiguous branch the same write records `closeout_state: unknown`, so the ambiguity is durable across attempts. A run whose closeout is provably skipped records neither key, so a crash after the write leaves no ambiguous outcome, which is safe because no external request was possible. A crash before the write leaves the previous report state, and the next finalization re-derives the same posture.

A closeout error and a closeout result that cannot be persisted both leave the pending intent in place, so the run counts as unknown either way. A provably skipped closeout has no intent, and a result write that fails leaves any earlier `closeout_state` in place: a run with no other closeout state reads as settled, while a preserved `retry_authorized` keeps it resumable even though no external request was made.

Report fields: `run_id`, `slug`, `task_summary`, `branch`, `worktree_path`, `planning_path`, `report_path`, `current_phase`, `status` (`success` or `failure`), `completion`, `phase_sessions`, `git` (`branch`, `commit`, `short_commit`, `dirty`, `files_changed`), `changes_made`, `files_changed`, `validation`, `review_outcome`, `review_fix_iterations`, `assumptions`, `risks`, `unresolved_issues`, `failure` (only when the run did not pass: `attempted`, `blocked`, `changes_left_behind`, `left_behind_summary`), and `generated_at`. List fields hold at most 8 items. Text fields hold at most 240 runes.

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
- States: `created` (with `closeout_url` and `closeout_provider`), `unsupported`, `skipped`, `failed`, `unknown`, and `retry_authorized`. `Closeout` returns the first four. Finalization records `unknown` when the call errors, because the request may have reached the provider, and a result it cannot persist leaves any pending `closeout_intent` in place, which classifies the run as unknown in the same way. Manual resolution records `created` with an operator-supplied URL or `retry_authorized`. Every closeout error and every persisted `closeout_intent` without a recorded result is an unknown external outcome and is never retried automatically. A manifest without `finalization_version` whose `closeout_state` is empty or `failed` counts as unknown too, since it cannot prove the external request did not happen. Such a run becomes resumable again only through [manual resolution](#manual-closeout-resolution).

## Manual closeout resolution

`ResolveCloseout` (`resolve.go`) records an operator decision for a run whose closeout outcome is unknown, or a URL that supersedes an authorised retry. It serves `steiner oneshot --resolve`.

- Validation (`validateResolution`): a `ResolutionRecordURL` trims the URL and requires an `http` or `https` scheme and a host; a `ResolutionRetry` takes no URL. `resolutionIdentity` rejects a manifest without a run ID and an identity that names a different run.
- It reads the manifest, acquires the run's exclusive lock through `RunLockFactory` (default `AcquireRunLock`), the same lock `Resume` takes, then re-reads the manifest under the lock so a concurrent run or resolution is not overwritten.
- It rejects a run whose outcome is not unknown (`closeoutOutcomeUnknown`) with an error naming the run's state, so a recorded result cannot be replaced. The one exception is a URL on a `retry_authorized` run, which `closeoutRetryable` reports as eligible: the request the authorised attempt would open may already exist, so recording it supersedes the retry. A retry resolution still requires an unknown outcome, so it cannot be repeated on a run that already authorised one.
- It records `created` with `closeout_url` for a URL, or `retry_authorized` for a retry, clears `closeout_intent` and `closeout_note`, and stamps `finalization_version`.
- It never calls `Closeout`. A `retry_authorized` run reaches closeout through the next resume only. A URL recorded over an authorised retry leaves every phase complete, the report written, and `closeout_state: created`, so the next `Resume` reports the run as already complete and calls no closeout.

`runOneshotResolve` (`cmd/steiner/cmd_oneshot.go`) builds a runtime without a model requirement, so resolution needs no phase models or sub-agents. The CLI takes `--resolve <run-id>` with exactly one of `--url <url>` or `--retry`, and rejects it alongside `--list`, `--resume`, or a task (`cmd/steiner/commands.go`).

## TUI integration

Interactive runs use `routeControlToOneshot`:

- Phase events are multiplexed onto the session sink (`output.NewMultiSink`).
- Phase divider: `Phase: <name>` in the scrollback, with no timestamp.
- Status bar: `phase · <name>` while the run is active.
- Sidebar: `Oneshot - <phase>` while the run is active.
- Indicator states emitted by the engine (`events.go`): `starting`, `boundary`, `completed`, `cancelled`, `orphaned`. `phaseIndicatorRunning` is defined but nothing emits it.
- Control routing: one `interactive.PhaseRouter` per run is set with `Session.SetActivePhaseControl` and passed to the orchestrator as `RegisterControl` (`router.Register`). While it is set, the session driver is detached from the steer queue (`DetachSteers`). Release reattaches it (`AttachSteers`). Between phases the router has no target: steers wait in the queue, and cancel requests report that no phase is active.
- The orchestrator runs under `Session.RunBackground`, and `CancelBackground` cancels it. On quit, `runQuitSequence` cancels background runs first, which takes the cancellation path above. `awaitRuns` then joins them.
- Steering-only composer: see [Steering input](../user/oneshot.md#steering-input) for command and input behavior.
- `OneshotFinishedEvent` is emitted from the run goroutine on success and on error. Its handler clears `oneshotRunning` and the phase chrome fields.

## Tests

Integration and resume tests use real git worktrees (`integration_test.go`, `resume_test.go`, `worktree_test.go`).
