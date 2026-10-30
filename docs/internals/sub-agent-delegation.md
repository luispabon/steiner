# Sub-agent delegation internals

User guide: [Sub-agent delegation](../user/sub-agent-delegation.md).

## Call sequence

1. `cliRunner.run` (`cmd/steiner/runner.go`) calls `delegation.BuildDelegateRegistry` (`internal/delegation/registry.go`), which registers `sub_agent`, `follow_up`, and the advisor tool.
2. A `sub_agent` call with `type: vision` goes to `newVisionHandler` (`vision_tool.go`). Other types go to `newSpecializedHandler` (`specialized_tools.go`). `follow_up` goes to `runFollowUp` (`follow_up.go`).
3. `newSpecializedHandler` (`specialized_tools.go`) and `newVisionHandler` (`vision_tool.go`) install a `Prepare` hook. For a worktree-isolated type (`UsesWorktree` in `agent_type.go`: `code` and `documentor`), the hook provisions the worktree (`provisionCodePlan`, `specialized_worktree.go`). Every hook then builds the child `agent.RunRequest` through `BuildChildRun` (`bootstrap.go`). `follow_up` has no `Prepare` hook.
4. `superviseDelegate` (`specialized_execute.go`) hands the job to the `Supervisor`, which calls `Spawn` in async mode or `SpawnAndWait` otherwise. Specialized, vision, and `follow_up` calls all reach it. In `Supervisor.execute` (`supervisor.go`), the supervisor runs `Prepare` and then registers the child with `ActiveController`. Jobs without `Prepare` (`follow_up`) register in `startQueuedLocked` (`supervisor_queue.go`) when they start.
5. The supervised job runs `SpawnDelegate` (`task.go`), which returns a `tool.ExecutionResult` with retention metadata.
6. Session persistence, unregistration, and cleanup follow.

`follow_up` resumes the child session stored in `SessionStore` (`internal/delegation/session.go`).

## Tool registration

- The `sub_agent` schema in `specialized_tools.go` requires `type` and six brief fields: `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, and `checks`.
- `research` is omitted when `deps.Searcher` is nil (`registry.go`). `vision` is omitted when `Models.Effective.SubAgents["vision"]` is empty or `ImageStore` is nil, and it requires `image_id`.
- Per-type allowlists are in `agent_type.go` (`agentAllowlists`). `documentor` clones `codeAgentAllowlist`, so its narrower write surface is enforced by `documentorWriteScope`, not by a shorter tool list. `mergedAllowedTools` (`specialized_tools.go`) sorts and dedupes the allowed tool names.

## Write scope

- `tool.WriteScope` (`internal/tool/write_scope.go`) matches writable files by final component: exact extension (leading dot included) or exact basename. Matching is case-sensitive and directory components never grant eligibility.
- `Executor.WithWriteScope` (`internal/tool/executor.go`) attaches a scope to a child executor. `runPipeline` (`internal/tool/execution_pipeline.go`) applies it to the effective policy after plan-mode restrictions, so both limits hold. `PathPolicy.ensureWriteScopeAllowed` (`internal/tool/policy.go`) checks root containment and the name rules against both the lexical path and its symlink-resolved form, denies an existing directory whose name matches, and fails closed when the policy has no root.
- `mutate` resolves both `move` endpoints through the policy (`planMove`, `internal/tool/builtin/mutate_ops.go`), so a move inside the scope cannot escape it.
- `agentWriteScope` (`internal/delegation/agent_type.go`) returns `documentorWriteScope` for `AgentTypeDocumentor`. `buildChildRunRequest` (`bootstrap.go`) installs it on the child executor, so the initial run, follow-ups, and commit remediation reuse the same restriction.
- The restriction covers `mutate` paths only. `bash`, MCP tools, and other tools that write through their own mechanisms are outside it; worktrees share git metadata with the parent repository, and hardlinks or races can bypass name checks. The user-facing rules and limits are in [Sub-agent delegation](../user/sub-agent-delegation.md#documentor-type).

## Supervisor and active controller

`Supervisor` (`internal/delegation/supervisor.go`) owns child jobs. `cmd/steiner/runtime_build.go` builds it with `NewSupervisor` and builds the `ActiveController` with `NewActiveController`. `MaxParallel` comes from `cfg.SubAgent.MaxParallel`, with a floor of 1.

- **Caps.** At most `MaxParallel` children run at once. Others wait in the queue. Running plus queued children are capped at `2 * MaxParallel`. Beyond that, `Spawn` and `SpawnAndWait` return `ErrOutstandingCap` (`supervisor_queue.go`).
- **Context.** Children run on `context.WithoutCancel(handlerCtx)` (`supervisor_queue.go`). Cancellation reaches them only through the supervisor's cancel methods.
- **Cancellation.** `CancelAgent` and `CancelAll` (`supervisor.go`) take a `CancelCause` (`cancel_cause.go`). The interactive canceller passes `CancelCauseUser` (`cmd/steiner/interactive_session.go`). Phase shutdown passes `CancelCauseSystem` (`cmd/steiner/phase_driver.go`). A queued job cancelled before it starts gets its result from `OnCancelledBeforeStart`.
- **Shutdown.** `Shutdown` (`supervisor_shutdown.go`) rejects new spawns with `ErrSupervisorClosed`, cancels children, and waits up to the shorter of the caller's context and `JoinTimeout` (10 seconds by default, `supervisor.go`). Jobs still unresolved get `ErrSupervisorClosed` results.

`ActiveController` (`internal/delegation/active.go`) tracks live children by agent ID. `RegisterWithCancel` stores each child's cancel function, agent type, and worktree, and returns `ErrAgentAlreadyActive` for a duplicate ID. The child's context is created in `Supervisor.enqueue` (`context.WithCancel(context.WithoutCancel(handlerCtx))`). `CancelAgentWithDiscard` cancels one child.

## Async path

`AsyncSubAgents` is fixed for a session. `newDelegateDeps` (`cmd/steiner/runner.go`) sets it from `asyncSubAgents()` (`cmd/steiner/runner_run.go`), which is true for every run mode except `exec`. In `exec`, calls block until the child finishes.

In async mode, `superviseDelegate` returns an `AckResult` (`ack.go`) with status `running` or `queued` and the agent ID needed for `follow_up`.

The conversation driver (`internal/agent/conversation_driver.go`) calls `Supervisor.MarkDelivered` (`supervisor_async.go`) on delivery and coalesces completions within `completionCoalesceWindow` (five seconds, `internal/agent/conversation_driver_policy.go`). Deliveries emit `output.SubAgentsDeliveredEvent`. `output.SubAgentResultsUnansweredEvent` is emitted from `internal/interactive/unanswered_results.go`.

- **Ledger.** `Supervisor.Ledger()` lists outstanding children, and `IsPending` reports whether one is still outstanding (`supervisor_async.go`). Sessions persist the ledger as `SubAgentLedger` (`internal/agent/sub_agent_types.go`, `internal/session/session.go`) and group state as `DelegationGroups` (`internal/agent/delegation_groups.go`, `internal/session/session.go`).
- **Groups.** `jobGroup` (`supervisor_groups.go`) holds one label's members within a tool batch. `sealBatch` seals the batch's groups. `releaseGroupLocked` holds completions until a group is sealed and every member has a final result, then posts them together, ordered by `Seq`.
- **follow_up admission.** `follow_up` is rejected at `FollowUpCount >= SubAgent.MaxFollowUps`. In async sessions it is also rejected while the agent is still pending (`Supervisor.IsPending`) (`follow_up.go`).

### Clearing held results

`Supervisor.ClearFinishedSubAgents(scope)` (`supervisor_async.go`) is the only path that drops an unacknowledged result without delivering it. It runs under `Supervisor.mu` and is all-or-nothing: every unacknowledged job must match `scope` exactly, already hold a completion, and have reached `phaseDone`. Otherwise it changes nothing and returns the outstanding count, leaving the completions and groups available for delivery. An empty scope matches jobs enqueued without a group scope; it is not a wildcard.

On success it acknowledges and prunes each job, removes it from its `jobGroup` (`discardGroupMemberLocked`, `supervisor_groups.go`), and deletes any group left empty, so a later `SealGroupBatch` cannot post the discarded members. Nothing reaches the completion sink, and a later `MarkDelivered` finds the jobs already acknowledged. The discarded jobs also leave `Supervisor.Ledger()`, so the old driver's retire save (`writeLineageAs`, `internal/interactive/driver_host.go`) omits them from the replaced session's `SubAgentLedger`. `/clear` calls this with the live driver's group scope before swapping drivers; `/new`, session switching, and forking still refuse every pending result through `pendingRefusalLocked` (`internal/interactive/persistence.go`).

## Worktree provisioning

For a worktree-isolated type (`UsesWorktree` in `agent_type.go`: `code` and `documentor`), the `Prepare` hook (`provisionCodePlan`, `specialized_worktree.go`) runs these steps at dequeue, before `BuildChildRun`:

1. `DirtyPaths` (`worktree.go`) reports uncommitted and untracked parent changes, best effort. Found paths add a warning, because the worktree starts from parent HEAD and omits them.
2. `ProvisionCodeWorktree` (`worktree.go`) creates a git worktree on a `delegate/` branch from parent HEAD, at `.steiner/worktrees/<processHash>/<sanitizedBranch>/<agentID>`. A failure fails the call.
3. `codeRemediationConfig` (`specialized_worktree.go`) sets post-run remediation for the worktree.
4. `WorktreePath`, `WorktreeBranch`, and `Warnings` are host-only result fields. The model receives only the project-relative `worktree_path`, for worktree-isolated results and follow-ups (see Result and retention).

Other types skip provisioning. `denyFollowUpOnDeadCodeWorktree` (`follow_up.go`) runs before the child is registered and rejects a worktree-isolated follow-up whose worktree is missing or invalid.

`steiner worktrees` (`cmd/steiner/cmd_worktrees.go`) supports `--list`, `--prune <id>`, and `--prune-all`. Pruning (`worktree_prune.go`) removes only git-reported worktrees on `delegate/` branches. A directory that git does not track, such as one left by a crash during provisioning, must be removed by hand.

## Bootstrapping a child run

`BuildChildRun` (`internal/delegation/bootstrap.go`) builds the child `RunRequest`:

1. **Limits.** `SubAgent` defaults in `internal/config/defaults.go` are `MaxTurns` 120 and `MaxTokens` set from `DefaultSubAgentMaxTokens`. Timeout defaults to none. `DefaultLimits` (`limits.go`) raises `MaxTurns` to at least `config.MinSubAgentMaxTurns`.
2. **Prompt.** The child receives one user message carrying the six-field brief. Sandbox state (`SandboxEnabled` and `sandbox.WritableHostMounts`) is passed to the child as plain values (`cmd/steiner/runner.go`).
3. **Registries.** `buildChildRegistries` takes `Registry.Subset` of the parent base registry and always excludes `follow_up` and `workflow_handoff`. Child tool events classify only as `ParallelClassTool` or `ParallelClassNone` (`bootstrap.go`).
4. **Executor.** `scopedToolExecutor` (`bootstrap.go`) sets the child agent ID with `tool.WithApprovalAgentScope`. Approval code in `internal/agent/approval.go` and `internal/interactive/wiring.go` reads that scope. For `explore` and `evaluate` children with the sandbox enabled, `readOnlyBash` applies `tool.WithReadOnlyProjectBash`. The parent's mode getter is wired in with `WithModeGetter`, and `agentWriteScope` adds `WithWriteScope` for restricted types.
5. **Advisor.** The advisor tool is added only when the child's allowlist contains it and `AdvisorForChild` is set. `code`, `documentor`, `review`, and `evaluate` allowlist the advisor (`agent_type.go`). Uses are capped by `AdvisorConfig.MaxUsesPerSubAgent` (default 1) and counted in `AdvisorBudgetStore` (`advisor_budget.go`).
6. **Context flags.** `SkipAgents` is true only for `vision`. `SkipProjectContext` is true for every type except `code`, `documentor`, `review`, and `evaluate` (`childContextSkips`, `bootstrap.go`).
7. **Mode.** `checkPlanModeWriteDenial` (`specialized_tools.go`) denies worktree-isolated types (`UsesWorktree`, so `code` and `documentor`) in plan mode. `denyFollowUpInPlanMode` (`follow_up.go`) denies `follow_up` when the child has `mutate`.
8. **Workflow mode.** `childWorkflowMode` (`bootstrap.go`) maps `code` to `DelegatedCodeSubAgentWorkflowMode` and `documentor` to `DelegatedDocumentorWorkflowMode`. `explore`, `research`, `evaluate`, `sanity_check`, `review`, and `vision` map to `DelegatedNonCodeChildWorkflowMode`. Other types use `DelegatedChildWorkflowMode`.
9. **Cache key.** `CacheKeyStore.KeyFor` (`cache_keys.go`) assigns `PromptCacheKey`. Cache rules are in [Prompt caching](prompt-caching.md).

Child model resolution is in `resolveModel` (`specialized_tools.go`). It uses the per-type alias from `Models.Effective.SubAgents`, else the profile's `DefaultModel`, else the parent model. Assignments are read through `CurrentEffectiveModelAssignments` on each spawn, so a profile switch reaches later spawns. A `vision` child with no alias reuses the parent model.

## Execution

`SpawnDelegate` (`task.go`):

1. Applies `Limits.Timeout` when set and records the start trace. Callers emit `delegation_started` before calling it (`emitDelegateStarted` in `specialized_execute.go` and `executeFollowUp` in `follow_up.go`).
2. Runs the child, then maps its final state to a status.
3. Emits `delegation_complete` or `delegation_failed` and returns the result.

Stop reasons (`result.go`):

- `complete` maps to `complete`.
- `max_turns` and `max_tokens` map to `partial`.
- `cancelled` maps to `partial` when the child had turns and produced output or tool calls, otherwise to `cancelled`. The child session stays resumable.
- `error` and `usage_limit` map to `failed`. A `usage_limit` failure carries `usageLimitDelegateNotice` (`task.go`), which tells the parent to delegate a fresh sub-agent.
- Any other reason maps to `failed` with `unknown stop reason: <reason>`.

Failure classification reads the child context snapshot `childCtx.Err()` that `SpawnDelegate` (`task.go`) captures when `runner.Run` returns, before the deferred limit cancel. `failedDelegateExecution` and `runStateFields` use that snapshot, while the runner error supplies the failure message and usage-limit notice. A provider transport timeout that wraps `context.DeadlineExceeded` on a live child context is therefore `failed`; only a done child context maps to `partial` or `cancelled` and marks the session resumable. The provider retries that timeout inside the child only before visible stream output; once visible chunks have been emitted, it returns the timeout so the child run fails.

Token counters accumulate across follow-ups, seeded from `Spec.PriorTokenUsage`.

`internal/agent` appends turn-budget notices at 50, 75, and 90 percent of the budget (`turnBudgetNoticeFractions`, `internal/agent/turn_budget_notice.go`). A follow-up counts from its own start turn (`RunState.BudgetStartTurn`). The notices do not change the limit.

`CacheKeyStore.BeginDispatch` (`cache_keys.go`) lets the first child for a cache key dispatch at once. Later children with the same key wait until the leader releases the gate or `dispatchGateTimeout` (10 seconds) elapses. Waiting emits `delegation_cache_waiting`.

## Parallel tool classification

`bootstrap.go` sets `RunRequest.ParallelClassOf`. `internal/agent/delegation_admission.go` uses it to identify delegation calls.

## Result and retention

`Result` (`internal/delegation/contract.go`) carries the terminal status (`complete`, `partial`, `failed`, or `cancelled`), output, turn and token counts, stop reason, and worktree fields. The provider receives only the projection `DelegationResultEnvelope` (`internal/agent/delegation_projection.go`), built by `Result.ProjectToolResult` (`result.go`):

| Field | Content |
|-------|---------|
| `output` | Exact child output |
| `status` | Omitted on success. Otherwise `partial`, `cancelled`, or `failed` |
| `reason` | The error for failures (`unknown failure` when empty), or a fallback such as `limit reached` |
| `continuation` | `agent_id` of a persisted child session that can be resumed. Set only when the child is persisted. |
| `worktree_path` | Project-relative path of a worktree-isolated child's worktree. |

Delegation results also set `ToolRetention` with kind `delegate_summary` (`RetentionKindDelegateSummary`, `task.go`).

## Host-side diagnostics vs. the model-facing result

Host-only diagnostics stay out of the provider payload described above.

- `NewTraceLoggerWithDiagnostics` (`trace_log.go`) writes through the diagnostics writer as `KindTool` records from `SourceSubAgent` when one is set. Otherwise it writes to the file path it is given, and writes nothing when that path is empty. `LogPath` derives that path from the log file as `<base>-delegation<ext>`, using `.log` when the log file has no extension. Treat the file as sensitive, like `--log-file`.
- `SpawnDelegate` and `failedDelegateExecution` (`task.go`) add a `tool_calls` entry with the trace file and tool-call counts.
- Per-tool traces are JSONL files at `.steiner/traces/<session-id>/<agent-id>.jsonl` (`tool_call_trace.go`). Traces are pruned after `toolCallTraceRetention` (seven days). The session ID is the threaded `SessionID`: the interactive session ID (`cmd/steiner/runner.go`) or the oneshot run ID (`cmd/steiner/cmd_oneshot.go`). `traceSessionID` falls back to `processTraceSession`, a random per-process ID, when it is empty.
- Failure classes in trace records follow `mutateFailClasses`, which mirrors `classify` in `scripts/mutate-session-stats.mjs`. Keep the two in sync.

## Cancellation

- `CancelDelegate{AgentID, Discard}` and `CancelAllDelegates` reach `Supervisor.CancelAgent` or `Supervisor.CancelAll` through the consumer-defined `DelegateCanceller`.
- Finalization runs for a targeted cancel with `Discard` set. After the child run ends, it:
  1. Calls `SessionStore.Invalidate`, which deletes the session and records an agent-ID tombstone. `Get` and `Save` refuse tombstoned IDs, so a late save cannot restore the session.
  2. Removes the delegate worktree with `PruneCodeWorktree` (`worktree_prune.go`) under `cancellationCleanupTimeout` (30 seconds, `cancellation_finalize.go`).
- Each disposal attempt emits `delegation_worktree_disposal` (`NewDelegationWorktreeDisposalEvent`, `internal/output/event_delegation.go`).

## Vision handler

`newVisionHandler` (`vision_tool.go`) validates the brief and `image_id`, resolves the image from `ImageStore`, and base64-encodes it (`loadVisionImageBlock`). It runs the child on the `sub_agents.vision` assignment with the image block in the request.

`ImageStore` (`internal/agent/image_store.go`) maps `img-N` IDs to `ImageRef` values. Its images directory is `.steiner/tmp/images` under the working directory.

## Event lifecycle

Delegation events (`internal/output/event_types.go`): `delegation_accepted`, `delegation_queued`, `delegation_started`, `delegation_cache_waiting`, `delegation_complete`, `delegation_failed`, and `delegation_worktree_disposal`.

`Supervisor.enqueue` (`supervisor_queue.go`) emits `delegation_accepted` for each accepted job and `delegation_queued` only when the job waits for a running slot.

## Grouped card render invariant

Grouped card boxes are cached in `settledGroupRows` and `groupBoxCache` (`internal/tui`). `markDelegationDirty` (`content_events_delegation.go`) sets `renderDirty` and calls `invalidatePrefixIfCached`. `dirtyDelegationCard` (`delegation_jump.go`) sets `renderDirty` and bumps `gen`. After any entry change, call one of them. A direct `renderDirty` writer must also call `invalidatePrefixIfCached` or bump `gen`, or the settled prefix serves stale output. `AdvanceDelegationSpinners` must not set `renderDirty`. Inputs that change a settled entry's box belong in `settledRowsKey`.

## Canon and workflow

- `delegationInstructions` in `internal/prompt/system.go` is gated on `DelegationEnabled`. The `## Your sub-agents` roster renders from the `specialists` slice in `internal/prompt/specialists.go`. Edit the slice, not the rendered markdown.
- `sectionDocumentorChild` (`internal/prompt/system.go`) renders `documentor_child.md.tmpl` only in `DelegatedDocumentorWorkflowMode`, so its duties and write-scope text stay isolated from the parent and other child modes.
- Cache inputs in `internal/agent/context_manager_base.go` include `DelegationEnabled`.
- Consumer files (`skills/*/SKILL.md` and `internal/oneshot/prompts/*.md`) reuse canon blocks. Drift rules are in [canon drift checks](../maintenance/canon-drift-checks.md). The shared blocks are pinned by `skills/shared_blocks_test.go` and `internal/oneshot/prompts_shared_test.go`.

## Constraints

- Sub-agents cannot nest.
- Concurrent children are bounded by `MaxParallel`.
- Diagnostics and traces are host-only. The model receives only the projection.
- A write scope is a file-name guard on `mutate`, not a security boundary. Tools that bypass the path policy, shared git metadata, and filesystem races are outside it.
