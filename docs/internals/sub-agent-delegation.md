# Sub-agent delegation internals

User-facing documentation: [Sub-agent delegation](../user/sub-agent-delegation.md).

## Call sequence

1. `cliRunner.run` (`cmd/steiner/runner.go`) calls `delegation.BuildDelegateRegistry` (`internal/delegation/registry.go`), which registers `sub_agent`, `follow_up`, and the advisor tool.
2. A `sub_agent` call with `type: vision` goes to `newVisionHandler` (`vision_tool.go`). Other types go to `newSpecializedHandler` (`specialized_tools.go`). `follow_up` goes to `runFollowUp` (`follow_up.go`).
3. `code` provisions a worktree first. Each handler then calls `BuildChildRun` (`bootstrap.go`) to build the child `agent.RunRequest`.
4. The handler registers the child with `ActiveController`. Specialized, vision, and `follow_up` calls all reach `superviseDelegate` (`specialized_execute.go`), which calls `Supervisor.Spawn` in async mode or `Supervisor.SpawnAndWait` otherwise.
5. The supervised job runs `SpawnDelegate` (`task.go`), which returns a `tool.ExecutionResult` with retention metadata.
6. Session persistence, unregistration, and cleanup follow.

`follow_up` resumes the child session stored in `SessionStore` (`internal/delegation/session.go`).

## Tool registration

- The `sub_agent` schema in `specialized_tools.go` has a `type` enum and six required fields: `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, and `checks`.
- `research` is omitted when `deps.Searcher` is nil (`registry.go`). `vision` is omitted when `Models.Effective.SubAgents["vision"]` is empty or `ImageStore` is nil, and it requires `image_id`.
- Per-type allowlists are in `agent_type.go` (`agentAllowlists`). `mergedAllowedTools` (`specialized_tools.go`) sorts and dedupes the allowed tool names.

## Supervisor and active controller

`Supervisor` (`internal/delegation/supervisor.go`) owns child jobs. `cmd/steiner/runtime_build.go` builds it with `NewSupervisor` and builds the `ActiveController` with `NewActiveController`. `MaxParallel` comes from `cfg.SubAgent.MaxParallel`, with a floor of 1.

- **Caps.** At most `MaxParallel` children run at once. Others wait in the queue. Running plus queued children are capped at `2 * MaxParallel`. Beyond that, `Spawn` and `SpawnAndWait` return `ErrOutstandingCap` (`supervisor_queue.go`).
- **Context.** Children run on `context.WithoutCancel(handlerCtx)` (`supervisor_queue.go`). Cancellation reaches them only through the supervisor's cancel methods.
- **Cancellation.** `CancelAgent` and `CancelAll` (`supervisor.go`) cancel with `CancelCauseUser` (`cancel_cause.go`). A queued job cancelled before it starts gets its result from `OnCancelledBeforeStart`.
- **Shutdown.** `Shutdown` (`supervisor_shutdown.go`) rejects new spawns with `ErrSupervisorClosed`, cancels children, and waits up to the shorter of the caller's context and `JoinTimeout` (10 seconds by default, `supervisor.go`). Jobs still unresolved get `ErrSupervisorClosed` results.

`ActiveController` (`internal/delegation/active.go`) tracks live children by agent ID, with agent type and code worktree metadata. `RegisterWithCancel` gives each child a cancellable context and returns `ErrAgentAlreadyActive` for a duplicate ID. `CancelAgentWithDiscard` cancels one child. `CancelAll` only cancels: it does not unregister children or dispose their worktrees. The child cleanup path handles unregistration and disposal (see Cancellation).

## Async path

`AsyncSubAgents` is fixed for a session. `newDelegateDeps` (`cmd/steiner/runner.go`) sets it from `asyncSubAgents()` (`cmd/steiner/runner_run.go`), which is true for every run mode except `exec`. In `exec`, calls block until the child finishes.

In async mode, `superviseDelegate` returns an `AckResult` (`ack.go`) with status `running` or `queued` and the agent ID needed for `follow_up`.

The conversation driver (`internal/agent/conversation_driver.go`) calls `Supervisor.MarkDelivered` (`supervisor_async.go`) on delivery and coalesces completions within `completionCoalesceWindow` (five seconds, `internal/agent/conversation_driver_policy.go`). Deliveries emit `output.SubAgentsDeliveredEvent`. `output.SubAgentResultsUnansweredEvent` is emitted from `internal/interactive/unanswered_results.go`.

- **Ledger.** `Supervisor.Ledger()` lists outstanding children, and `IsPending` reports whether one is still outstanding (`supervisor_async.go`). Interactive sessions persist the ledger as `SubAgentLedger` and group state as `DelegationGroups` (`internal/agent/sub_agent_types.go`, `internal/agent/conversation_driver_state.go`).

## Code worktree provisioning

For `code`, `newSpecializedHandler` (`specialized_tools.go`) runs these steps before `BuildChildRun`:

1. `DirtyPaths` (`worktree.go`) reports uncommitted and untracked parent changes, best effort. Found paths add a warning, because the worktree starts from parent HEAD and omits them.
2. `ProvisionCodeWorktree` (`worktree.go`) creates a git worktree on a `delegate/` branch from parent HEAD, at `.steiner/worktrees/<processHash>/<sanitizedBranch>/<agentID>`. A failure fails the call.
3. `codeRemediationConfig` (`specialized_worktree.go`) sets post-run remediation for the worktree.
4. `WorktreePath`, `WorktreeBranch`, and `Warnings` are host-only result fields (see Result and retention).

Other types skip provisioning. `denyFollowUpOnDeadCodeWorktree` (`follow_up.go`) runs before the child is registered and rejects a `code` follow-up whose worktree is missing or invalid.

`steiner worktrees` (`cmd/steiner/cmd_worktrees.go`) supports `--list`, `--prune <id>`, and `--prune-all`. Pruning (`worktree_prune.go`) removes only git-reported worktrees on `delegate/` branches. A directory that git does not track, such as one left by a crash during provisioning, must be removed by hand.

## Bootstrapping a child run

`BuildChildRun` (`internal/delegation/bootstrap.go`) builds the child `RunRequest`:

1. **Limits.** `SubAgent` defaults in `internal/config/defaults.go` are `MaxTurns` 120 and `MaxTokens` set from `DefaultSubAgentMaxTokens`. Timeout defaults to none. `DefaultLimits` (`limits.go`) raises `MaxTurns` to at least `config.MinSubAgentMaxTurns`.
2. **Prompt.** The child receives one user message carrying the six-field brief. Sandbox state (`SandboxEnabled` and `sandbox.WritableHostMounts`) is passed to the child as plain values (`cmd/steiner/runner.go`).
3. **Registries.** `buildChildRegistries` takes `Registry.Subset` of the parent base registry and always excludes `follow_up` and `workflow_handoff`. Child tool events classify only as `ParallelClassTool` or `ParallelClassNone` (`bootstrap.go`).
4. **Executor.** `scopedToolExecutor` (`bootstrap.go`) sets the child agent ID with `tool.WithApprovalAgentScope`. Approval code in `internal/agent/approval.go` and `internal/interactive/wiring.go` reads that scope. For `explore` and `evaluate` children with the sandbox enabled, `readOnlyBash` applies `tool.WithReadOnlyProjectBash`. The parent's mode getter is wired in with `WithModeGetter`.
5. **Advisor.** The advisor tool is added only when the child's allowlist contains it and `AdvisorForChild` is set. `code`, `review`, and `evaluate` allowlist the advisor (`agent_type.go`). Uses are capped by `AdvisorConfig.MaxUsesPerSubAgent` (default 1) and counted in `AdvisorBudgetStore` (`advisor_budget.go`).
6. **Context flags.** `SkipAgents` is true only for `vision`. `SkipProjectContext` is true for every type except `code`, `review`, and `evaluate` (`childContextSkips`, `bootstrap.go`).
7. **Mode.** `checkPlanModeCodeDenial` (`specialized_tools.go`) denies `code` in plan mode. `denyFollowUpInPlanMode` (`follow_up.go`) denies `follow_up` when the child has `mutate`.
8. **Workflow mode.** `childWorkflowMode` (`bootstrap.go`) maps `code` to `DelegatedCodeSubAgentWorkflowMode`. `explore`, `research`, `evaluate`, `sanity_check`, `review`, and `vision` map to `DelegatedNonCodeChildWorkflowMode`. Other types use `DelegatedChildWorkflowMode`.
9. **Cache key.** `CacheKeyStore.KeyFor` (`cache_keys.go`) assigns `PromptCacheKey`. Cache rules are in [Prompt caching](prompt-caching.md).

## Execution

`SpawnDelegate` (`task.go`):

1. Applies `Limits.Timeout` when set and emits `delegation_started`.
2. Runs the child, then maps its final state to a status.
3. Emits `delegation_complete` or `delegation_failed` and returns the result.

Stop reasons (`result.go`):

- `complete` maps to `complete`.
- `max_turns` and `max_tokens` map to `partial`.
- `cancelled` maps to `partial` when the child had turns and produced output or tool calls, otherwise to `cancelled`. The child session stays resumable.
- `error` and `usage_limit` map to `failed`. A `usage_limit` failure carries `usageLimitDelegateNotice` (`task.go`), which tells the parent to delegate a fresh sub-agent.
- Any other reason maps to `failed` with `unknown stop reason: <reason>`.

Token counters accumulate across follow-ups, seeded from `Spec.PriorTokenUsage`.

`internal/agent` appends turn-budget notices at 50, 75, and 90 percent of the budget (`turnBudgetNoticeFractions`, `internal/agent/turn_budget_notice.go`). A follow-up counts from its own start turn (`RunState.BudgetStartTurn`). The notices do not change the limit.

`CacheKeyStore.BeginDispatch` (`cache_keys.go`) lets the first child for a cache key dispatch at once. Later children with the same key wait until the leader releases the gate or `dispatchGateTimeout` (10 seconds) elapses. Waiting emits `delegation_cache_waiting`.

## Parallel tool classification

`bootstrap.go` sets `RunRequest.ParallelClassOf`. `internal/agent/delegation_admission.go` uses it to identify delegation calls.

## Result and retention

`Result` (`internal/delegation/contract.go`) carries the terminal status (`complete`, `partial`, `failed`, or `cancelled`), output, turn and token counts, stop reason, and worktree fields. The provider receives only the projection from `DelegationResultEnvelope` (`result.go`):

| Field | Content |
|-------|---------|
| `output` | Exact child output |
| `status` | Omitted on success. Otherwise `partial`, `cancelled`, or `failed` |
| `reason` | The error for failures (`unknown failure` when empty), or a fallback such as `limit reached` |

Delegation results also set `ToolRetention` with kind `delegate_summary` (`RetentionKindDelegateSummary`, `task.go`).

## Host-side diagnostics vs. the model-facing result

Host-only diagnostics stay out of the provider payload described above.

- The trace log (`trace_log.go`) writes to `<log file>-delegation` with the log file's extension. Treat it as sensitive, like `--log-file`.
- `SpawnDelegate` and `failedDelegateExecution` (`task.go`) add a `tool_calls` entry with the trace file and tool-call counts.
- Per-tool traces are JSONL files at `.steiner/traces/<session-id>/<agent-id>.jsonl` (`tool_call_trace.go`). Traces are pruned after `toolCallTraceRetention` (seven days). The session ID is fixed per process (`processTraceSession`).
- Failure classes in trace records follow `mutateFailClasses`, which mirrors `classify` in `scripts/mutate-session-stats.mjs`. Keep the two in sync.

## Cancellation

- `CancelDelegate{AgentID, Discard}` and `CancelAllDelegates` reach `Supervisor.CancelAgent` or `Supervisor.CancelAll` through the consumer-defined `DelegateCanceller`.
- Finalization runs for a targeted cancel with `Discard` set. After the child run ends, it:
  1. Calls `SessionStore.Invalidate`, which deletes the session and records an agent-ID tombstone. `Get` and `Save` refuse tombstoned IDs, so a late save cannot restore the session.
  2. Removes the code worktree with `PruneCodeWorktree` (`worktree_prune.go`) under `cancellationCleanupTimeout` (30 seconds, `cancellation_finalize.go`).
- Each disposal attempt emits `delegation_worktree_disposal` (`NewDelegationWorktreeDisposalEvent`, `internal/output/event_delegation.go`).

## Vision handler

`newVisionHandler` (`vision_tool.go`) validates the brief and `image_id`, resolves the image from `ImageStore`, and base64-encodes it (`loadVisionImageBlock`). It runs the child on the `sub_agents.vision` assignment with the image block in the request.

`ImageStore` (`internal/agent/image_store.go`) maps `img-N` IDs to `ImageRef` values. Its images directory is `.steiner/tmp/images` under the working directory.

## Event lifecycle

Delegation events (`internal/output/event_types.go`): `delegation_started`, `delegation_cache_waiting`, `delegation_complete`, `delegation_failed`, and `delegation_worktree_disposal`.

## Grouped card render invariant

Grouped card boxes are cached in `settledGroupRows` and `groupBoxCache` (`internal/tui`). `markDelegationDirty` (`content_events_delegation.go`) sets `renderDirty` and calls `invalidatePrefixIfCached`. `dirtyDelegationCard` (`delegation_jump.go`) sets `renderDirty` and bumps `gen`. After any entry change, call one of them. A direct `renderDirty` writer must also call `invalidatePrefixIfCached` or bump `gen`, or the settled prefix serves stale output. `AdvanceDelegationSpinners` must not set `renderDirty`. Inputs that change a settled entry's box belong in `settledRowsKey`.

## Canon and workflow

- `delegationInstructions` in `internal/prompt/system.go` is gated on `DelegationEnabled`. The `## Your sub-agents` roster renders from the `specialists` slice in `internal/prompt/specialists.go`. Edit the slice, not the rendered markdown.
- Cache inputs in `internal/agent/context_manager_base.go` include `DelegationEnabled`.
- Consumer files (`skills/*/SKILL.md` and `internal/oneshot/prompts/*.md`) reuse canon blocks. Drift rules are in [canon drift checks](../maintenance/canon-drift-checks.md). The shared blocks are pinned by `skills/shared_blocks_test.go` and `internal/oneshot/prompts_shared_test.go`.

## Constraints

- Sub-agents cannot nest.
- Concurrent children are bounded by `MaxParallel`.
- Diagnostics and traces are host-only. The model receives only the projection.
