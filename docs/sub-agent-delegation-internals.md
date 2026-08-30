User-facing documentation: [Sub-agent Delegation](sub-agent-delegation.md).

## Part 2 — Internals

### Architecture

```
┌─────────────────────────────────────────────┐
│  Parent Agent Loop (internal/agent)         │
│                                             │
│  Specialized sub-agent tools (explore,      │
│  research, code, evaluate, sanity_check, review, vision) call into    │
│  BuildChildRun() directly.                  │
│                                             │
│                 ▼                           │
│  ┌──────────────────────────────────┐       │
│  │ delegation.BuildChildRun()       │       │
│  │  - derive limits                 │       │
│  │  - build child prompt            │       │
│  │  - build child registries        │       │
│  │  - assemble RunRequest           │       │
│  └──────────────┬───────────────────┘       │
│                 │                           │
│                 ▼                           │
│  ┌──────────────────────────────────┐       │
│  │ delegation.SpawnDelegate()       │       │
│  │  - context timeout               │       │
│  │  - emit DelegationStarted        │       │
│  │  - runner.Run(childCtx, req)     │       │
│  │  - auto-extension loop (≤3x)     │       │
│  │  - summarisation turn            │       │
│  │  - emit DelegationComplete       │       │
│  └──────────────┬───────────────────┘       │
│                 │                           │
│                 ▼                           │
│  tool.ExecutionResult + ToolRetention       │
│  (persisted on parent conversation message) │
└─────────────────────────────────────────────┘
```

### Package layout

| Package               | Responsibility                                                                                                                               |
|-----------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `internal/delegation` | Contract types, tool definition, handler, bootstrapping, spawn logic, limits, result building, specialised agent types and tool constructors |
| `internal/agent`      | Retention metadata on messages, runner interface                                                                                             |
| `internal/tool`       | `ToolRetention` struct, `ExecutionResult.Retention` field, `Registry.Clone()`                                                                |
| `internal/prompt`     | Delegation instructions preamble injected when delegation is enabled                                                                         |
| `internal/output`     | Delegation lifecycle events (started, complete, failed, extension)                                                                           |
| `internal/tui`        | Rendering of delegation events with spinner, lifecycle tracking, collapsible output                                                          |
| `cmd/steiner`         | `buildActiveRegistry()` wires delegation tools into the active registry                                                                    |

### Active controller and context topology

`internal/delegation.ActiveController` owns the live delegate set. It keys registrations by agent ID, keeps deterministic registration order, stores the agent type and code worktree metadata, and creates a dedicated child context for each registration. `CancelAgent` cancels only that child context. `CancelAll` cancels every registered child in registration order, but does not remove registrations and never requests worktree disposal; finalization and the deferred unregister own those steps.

The controller is process-lifetime state. `cmd/steiner` constructs one controller when it builds the runtime, and shares that same instance with every per-turn `BuildDelegateRegistry` rebuild and with the interactive session's `DelegateCanceller`. Registrations therefore survive registry replacement between turns. The TUI does not inspect the controller map to render the selector; it receives lifecycle events and snapshots active rows from its own transcript state.

### Tool registration

When `SubAgent.Enabled` is `true`, `delegation.BuildDelegateRegistry` clones the base registry and registers the `follow_up` tool plus a specialised tool for each agent type (`explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, and conditionally `vision`). Specialised tools are thin wrappers over the same delegation infrastructure (`BuildChildRun` + `SpawnDelegate`) with a baked-in system prompt, a per-type tool allowlist (`AgentAllowedTools`), and a task-oriented schema. The `vision` tool additionally accepts an `image_id` parameter and is only registered when the selected profile's `sub_agents.vision` assignment is configured.

`fetch_url` is registered unconditionally in the base built-in tool set. `web_search` is registered conditionally — it is added to both the parent registry and the extended base registry (used for child bootstrapping) only when a `web.Searcher` backend is configured. When no search backend is configured, the `research` sub-agent type is excluded from delegation entirely, so no stub or unavailable tool is ever exposed.

### Code worktree provisioning

For `AgentTypeCode` only, `newSpecializedHandler` (`internal/delegation/specialized_tools.go`) runs a provisioning step **before** `BuildChildRun` is called. This step:

1. **Calls `DirtyPaths`** (`internal/delegation/worktree.go`) to collect any uncommitted or untracked files in the parent working tree, as best-effort information (failures are silently skipped). If changes are found, a warning is recorded describing them — the child worktree will not see these changes, which may be significant context loss.

2. **Calls `ProvisionCodeWorktree`** (`internal/delegation/worktree.go`) to create an isolated git worktree for the child agent. The worktree is created under `.steiner/worktrees/<processHash>/<sanitizedParentBranch>/<agentID>`, branched from the parent repository's current HEAD. The process hash and parent branch name are baked into the path to ensure collision-free isolation across process restarts and parallel executions. On provisioning success, `ProvisionCodeWorktree` returns a `CodeWorktree{Path, Branch}` struct. On failure, it returns an empty struct and a wrapped error; `newSpecializedHandler` fails the code-agent call rather than falling back to the parent or a shared tree.

3. **Overrides `SubAgentHandlerDeps.WorkDir`** to the provisioned worktree path. This field is subsequently consumed by `buildChildPrompt` (via `childPromptParams.workDir`) to set the child's `Prompt.ProjectRoot`, and by `buildChildRunRequest` (via `childRunRequestParams.WorkDir`) to construct the child's tool executor. Both paths use the same `SubAgentHandlerDeps.WorkDir` value, ensuring consistent isolation: the child's prompt context and its actual tool-execution root (for `mutate` writes, bash cwd, and `PathPolicy` enforcement) are both rooted at the isolated worktree, not the shared parent tree.

4. **Sets `Spec.SystemSuffix`** to `AgentSystemSuffix(AgentTypeCode)`, preserving code-agent-specific instructions after the shared system preamble.

5. **Enables post-run remediation** with `codeRemediationConfig(CodeWorktree)`. If the completed code run leaves the provisioned worktree dirty, `SpawnDelegate` runs a remediation turn that tells the child to stage only intended changes, commit them on the expected branch, and leave the worktree clean. The remediation config is saved with the session, so `follow_up` reuses the original worktree path and expected branch and applies the same remediation to later code follow-ups. Remediation state, conversation, turn counts, and cache usage continue through the normal session update path.

6. **Populates `Result` fields** (only for `AgentTypeCode`):
   - `WorktreePath` — the absolute path to the provisioned worktree.
   - `WorktreeBranch` — the branch name of the provisioned worktree (e.g. `delegate/a1b2c3d4/main/child-1`).
   - `Warnings` — a slice of human-readable warning strings covering dirty-tree changes and post-run remediation failures. Empty for successful provisioning of a clean tree.

All other agent types (`explore`, `research`, `evaluate`, `sanity_check`, `review`, `vision`) skip worktree provisioning and remediation entirely; their results always have empty `WorktreePath`, `WorktreeBranch`, and `Warnings` fields. `follow_up` reuses each child's originally-captured `agent.RunRequest` from `SessionStore` verbatim, including its executor already rooted at the original worktree, without re-provisioning.

### Active delegate lifecycle

A specialized or vision handler builds the child request, then registers the child with the shared `ActiveController`. For a code child, registration happens after worktree provisioning and request construction; for other types it happens after request construction. Registration occurs before dispatch gating. The handler passes the controller-created child context to the cache dispatch gate and to the child runner.

The started lifecycle event is emitted after registration and before the gate. It carries agent ID and type scope, and its payload also carries the plain agent-type string used by the TUI without an `internal/delegation` import. A follower waiting at the cache gate emits `delegation_cache_waiting` and waits on the registered child context, so it can be stopped before provider dispatch and is visible in the selector while waiting. If that context is already cancelled when the gate returns, the handler emits a scoped `StopReasonEvent` with reason `cancelled` and returns a cancelled result without starting the child runner. A child that reaches normal terminal handling keeps the existing complete or failed delegation events.

The handler defers `ActiveController.Unregister` until after session persistence and cancellation finalization. `follow_up` preserves the stored `Spec.AgentType`; when the session is a code session, it reconstructs the controller registration's worktree metadata from the session's remediation data, so follow-up cancellation refers to the original checkout. Registration failure is intentionally invisible to lifecycle UI: no delegate became active or visible, so handlers emit no started, stopped, or failed lifecycle event. Code provisioning is still cleaned up on that path, and duplicate IDs cannot replace or corrupt the existing controller row.

**Known limitation**: worktrees are never automatically removed. The `steiner worktrees --list`, `--prune`, and `--prune-all` commands manage cleanup, and operate only on worktrees git itself currently reports as real and delegation-owned (with `delegate/`-prefixed branches). A directory under `.steiner/worktrees/` that becomes orphaned or untracked by git (e.g. after a crash mid-provision, or after a manual `git worktree prune` outside the CLI) is not reachable by any of these commands and requires manual `rm -rf` by a human. This is a deliberate safety tradeoff — never delete a path git doesn't vouch for — rather than an oversight. Future extensions to the cleanup tooling should respect this constraint: only remove paths that git reports as real worktrees.

### Bootstrapping a child run

`BuildChildRun()` assembles the full `agent.RunRequest`:

**1. Derive limits.** `deriveChildLimits()` combines `SubAgentConfig` defaults with spec-level overrides using tighten-only semantics — an override is applied only when it is more restrictive than the configured default. Defaults: `MaxTurns` 15, `MaxTokens` 100,000. `timeout` is accepted as an optional parameter and defaults to no timeout.

**2. Build child prompt.** The child prompt is minimal: either the caller-provided `system_prompt` or a default, plus a single user message containing the task (and optional `context`). The system prompt is passed via `PromptOverrides` so the provider sees exactly one system message. When `Spec.Images` is non-empty, those images are attached to the first user message so the child model sees them immediately without spending a turn on a `read` call. The child also inherits the parent's sandbox state as plain values: `cmd/steiner` derives `SandboxEnabled` from the active runtime sandbox (`runtime.sandbox != nil && runtime.sandbox.Enabled()`) and the writable host-mount paths from `sandbox.host_mounts` (`Mode == "rw"`, in config order). The values are threaded `DelegateDeps` → `SubAgentHandlerDeps` as `SandboxEnabled` and `SandboxWritableMounts`; `buildChildPrompt` maps them to `AssemblyOptions.SandboxEnabled` and `AssemblyOptions.SandboxWritableMounts`, so the child's system preamble renders the same sandbox section as the parent's when the sandbox is active, unless a system prompt override replaces the standard preamble (an override drops the standard sections, including sandbox). Child executors carry the parent's sandbox wrapper directly: `cmd/steiner` resolves it once (`tool.Unsandboxed{}` when the runtime sandbox is off) and threads it `DelegateDeps` → `SubAgentHandlerDeps` as `Sandbox`, the same path `SandboxTmpDir` already follows. `buildChildRunRequest` passes it straight into `tool.NewExecutor` as the required `sandbox` parameter. From there, sandboxing for child `bash` and child subprocess-backed tools works exactly like the parent: `Executor.runPipeline` resolves a `ResolvedSandbox` (wrapper plus `readOnlyProject`) once per call and both dispatch paths consume it — there is no separate `CommandWrapper` closure for children.

The parent's execution-mode getter (`config.ExecutionMode`) is threaded the same way, as `ModeGetter`: `DelegateDeps` → `SubAgentHandlerDeps` → `childRunRequestParams`, and `buildChildRunRequest` wires it into the child executor via `WithModeGetter`. This is what makes a child's own `runPipeline` see plan mode when the parent is in plan mode, restricting the child's `mutate` calls to `.steiner/plans/` and mounting the project read-only for the child's `bash`/subprocess calls — matching the enforcement a parent-issued command would get, without any per-agent-type special-casing beyond the existing `readOnlyBash` flag for explore children.

**3. Build child registries.** Two registries are built from the parent via `ChildBootstrapOverrides.AllowedTools` (populated per agent type from `AgentAllowedTools(agentType)`, merged with `DelegateDeps.ExtraAllowedTools[agentType]` when the projection is non-nil):
- **Visible registry** — what the model can see and request: parent base registry tools filtered to `AllowedTools`, always excluding `follow_up` and `workflow_handoff`.
- **Execution registry** — same filtered tools but with all approval modes forced to `ApprovalModeAuto`.

If `AllowedTools` is empty, no tools are available to the child. This ensures children cannot delegate further, never block on approval, and only access the explicitly permitted tool set for their agent type.

`DelegateDeps.ExtraAllowedTools` is a per-agent-type projection of additional registered tool names built **externally** and consumed here. Delegation never reads MCP config or interprets tool provenance. When the projection is non-nil, handlers merge the built-in allowlist with `ExtraAllowedTools[agentType]` via `mergedAllowedTools`, producing a new sorted, deduplicated slice that never mutates the shared `agentAllowlists` map. Nil or empty projections grant no extra tools, preserving default-denied MCP behavior; names not present in the parent registry are ignored by `Registry.Subset`. Subsetting clones the original `ToolDef`s, so MCP handlers and provenance survive into the child registries unchanged.

**4. Assemble RunRequest.** Includes the parent's provider instance, a tool executor wrapping the execution registry, `ExtraParams` and `PromptSuffix` propagated from the parent's model config, and no explicit model override (child uses the selected profile's default assignment unless a per-type model alias is configured).

**5. Context delivery flags.** `buildChildPrompt` sets two independent
`AssemblyOptions` flags:

- `SkipAgents` — omits AGENTS.md (global + project). Set only for `vision`,
  which cannot read the repo.
- `SkipProjectContext` — omits project context `extra_files` only. Set for
  `explore`, `research`, `sanity_check`, and `vision`.

| Agent                            | `SkipAgents` | `SkipProjectContext` |
|----------------------------------|--------------|----------------------|
| `code`, `review`, `evaluate`     | No           | No                   |
| `explore`, `research`, `sanity_check` | No       | Yes                  |
| `vision`                         | Yes          | Yes                  |

AGENTS.md delivery is independent of project context: every agent type
receives AGENTS.md except `vision`. Project context `extra_files` go only to
`code`, `review`, and `evaluate`, which need full project awareness to
implement changes, review code, or evaluate design approaches. `follow_up`
replays the original child's stored request, so it is unaffected by either
flag.

**6. Prompt cache key reuse.** `buildChildRunRequest` sets `PromptCacheKey` on the child `agent.RunRequest`. When `SubAgentHandlerDeps.CacheKeyStore` is non-nil, it calls `CacheKeyStore.KeyFor(override.AgentType, provider.NewPromptCacheKey)`, which mints a key on first use and returns the same key for every subsequent delegation of that `AgentType`. When the store is nil, or `KeyFor` fails to produce a usable key, a fresh key is minted per call via `provider.NewPromptCacheKey()` (an entropy error leaves the key empty, which only disables provider-side caching for that child run).

`CacheKeyStore` (`internal/delegation/cache_keys.go`) is keyed by `AgentType`, not `AgentID`: `AgentID` is a fresh counter minted per delegation (`generateAgentID()` in `agent_id.go`) and is never stable across delegations of the same type, so keying by `AgentID` would make every lookup a guaranteed cache miss. Keying by `AgentType` instead lets repeated delegations to the same agent type (e.g. two separate `code` sub-agent calls in one session) share a provider-side cache shard. `PromptCacheKey` is a shard-routing hint consumed only by Codex/OpenAI-Responses traffic (`internal/provider/wire_responses.go`); Anthropic's wire adapter never reads it, so the store is a no-op there.

`CacheKeyStore` is process-lifetime scoped, matching `SessionStore`: it is instantiated once in `cmd/steiner` (`cliRuntime.delegationCacheKeyStore`) and threaded through `DelegateDeps.CacheKeyStore` → `SubAgentHandlerDeps.CacheKeyStore` on every delegation. It is **not** reset on `/new`, the same way `SessionStore.Reset()` has zero production callers today — a stale shard hint after a conversation boundary costs at most a cache miss, never a correctness issue, so resetting it is unnecessary.

The same store also holds the advisor's key, under a synthetic slot `cacheKeyAgentTypeAdvisor` (`"advisor"`, defined in `internal/delegation/agent_type.go`). `BuildDelegateRegistry` resolves it with the identical nil-safe fallback pattern used above (`CacheKeyStore.KeyFor` then a per-call mint) and passes it into `advisor.HandlerDeps.CacheKey`. This slot is deliberately absent from `AllAgentTypes()` and `validAgentTypeSet`: `KeyFor` performs no validation of its `AgentType` argument, but `IsDelegationTool` — which gates the parallel-tool dispatch path — calls `ValidAgentType`, so keeping `"advisor"` out of that set is what keeps the advisor on its existing serial-only path.

Beyond key reuse, `CacheKeyStore` also **staggers concurrent same-key dispatches** so a batch of same-`AgentType` children doesn't race each other over a cache that is only warm once a leader has populated it. The specialized and vision handlers call `CacheKeyStore.BeginDispatch(req.PromptCacheKey)` right after `BuildChildRun`: the first caller for a cache key becomes the leader and dispatches immediately; concurrent siblings sharing that key become followers and wait — bounded by a fixed `dispatchGateTimeout` of 10 seconds — until the leader's first streamed `ThinkingChunk`/`AssistantChunk` event (captured by a `dispatchReleaseSink` decorating the leader's `EventSink`), the leader's deferred safety-net release, the timeout, or cancellation. Gated followers emit a `delegation_cache_waiting` event, which the TUI renders as an hourglass glyph (`⧖`) with a live countdown to the gate deadline, cleared when the follower's own `DelegationStartedEvent` arrives. This is provider-agnostic: it staggers request *timing* for an identical prefix and helps whether or not `PromptCacheKey` itself is transmitted on the wire. Separately, pending-delegation display boxes are matched by originating tool-call ID (`Spec.ParentCallID` / `DelegationStartedEvent.CallID`) rather than FIFO arrival order, which also corrects a pre-existing ordering fragility for concurrent siblings generally, not just gated ones.

### Execution: SpawnDelegate

`SpawnDelegate()` orchestrates the child lifecycle:

1. **Timeout**: if `spec.Limits.Timeout > 0`, wraps context with `context.WithTimeout`.
2. **Emit** `DelegationStartedEvent` with agent ID, task preview (120 chars max), and the resolved model alias (`req.ResolvedModel.Alias`) so the tool box badge shows the alias assigned to the child instead of reverse-mapping its backend API ID.
3. **Run** the child agent loop via the `AgentRunner` interface.
4. **Auto-extension loop** (up to 3 iterations): if the child stopped due to `MaxTurns` AND its last message contains pending tool calls (mid-work), the loop extends by re-running with the accumulated conversation and an increased turn budget.
5. **Build result** from final state (maps `StopReason` → `Status`). Token counters (input, cache, and output) are accumulated across extension re-runs and prior follow-ups (`Spec.PriorTokenUsage`) rather than taken from the final state alone.
6. **Summarisation turn**: runs a single no-tool turn asking the model to summarise its work in ≤4000 chars.
7. **Emit** `DelegationCompleteEvent` or `DelegationFailedEvent`. `DelegationCompleteEvent` carries `InputTokens`/`CacheReadTokens`/`CacheCreateTokens` alongside the existing turn/tool/token counts; it is constructed via `NewDelegationCompleteEvent`, which takes a `DelegationCompleteParams` struct rather than positional arguments, so the TUI can render the child agent's cumulative cache hit rate in the tool box.
8. **Return** `tool.ExecutionResult` with `ToolRetention` metadata attached.

A child "needs extension" when `StopReason == StopReasonMaxTurns` AND the last assistant message has pending tool calls (interrupted mid-action). This prevents early termination when a delegate is actively working but hit its turn cap.

**Turn-budget checkpoint.** Independent of the extension loop, `internal/agent.Runner.Run` injects a convergence notice into the child's own conversation once it crosses 70% of the current run's `Limits.MaxTurns` (`turnBudgetNoticeFraction` in `internal/agent/turn_budget_notice.go`). This fires inside a single run, not only at an extension boundary, so even a run that never needs an extension gets the signal before it is too late. The notice text — "used N of M turns (R remaining) with E extension(s) remaining" — is built by `RunRequest.TurnBudgetNotice`, which only `internal/delegation` sets (`turnBudgetNoticeFunc` in `task.go`); the parent interactive run leaves it nil since `internal/agent` has no notion of delegate extensions. Because the closure is rebuilt fresh before every `runner.Run` call (the initial call in `SpawnDelegate` and each extension in `runChildToCompletion`), it always reports how many of `maxDelegateExtensions` remain at that point. The injected message is tagged by a content-prefix marker rather than `Message.Source`, because `Source` does not survive the `agent.Message` ↔ `provider.Message` round trip that the extension loop performs between runs; a later checkpoint supersedes the prior one in place rather than accumulating, so at most one such message is ever present in the conversation.

`StopReasonMaxTurns` and `StopReasonMaxTokens` map to `StatusPartial`. A partial result means the child's budget was exhausted before it could finish. Parent models must treat partial results conservatively — do not assume the delegated task succeeded, and retry or narrow scope rather than treating partial output as authoritative.

### Parallel tool execution

The parent `agent.RunRequest` may set `ParallelTool func(string) bool`, a predicate identifying tool calls eligible for concurrent execution. Child runs receive a nil predicate and remain serial; the predicate is consumed by `internal/agent`. `MaxParallelTools` bounds eligible calls, with zero meaning unbounded and one meaning serial execution.

`executeToolCalls` splits invocation from application. The execution path emits `ToolCallStarted` and `invokeTool` runs the executor, while `applyToolResult` updates `Conversation` and `Lineage`, emits budget events, and performs stop detection. Parallel results are applied in original call order, even when children finish in another order. This keeps conversation state and the prompt prefix deterministic.

A parallel batch receives one shared pre-batch conversation snapshot. Siblings therefore cannot see each other's results during execution; each result is applied only after invocation completes. Approval requests use the `ApprovalCoordinator` FIFO queue, so concurrent requests are presented and matched in queue order rather than racing user decisions.

### Result and retention

**Result** (returned to the parent model):

| Field               | Description                                           |
|---------------------|-------------------------------------------------------|
| `AgentID`           | Matches the request                                   |
| `Status`            | `complete`, `partial`, `failed`, or `cancelled`       |
| `Output`            | Last assistant message content                        |
| `Summary`           | Retained summary (≤4000 runes)                        |
| `TurnCount`         | Turns consumed by the child                           |
| `TokenCount`        | Tokens consumed by the child                          |
| `InputTokens`       | Cumulative uncached prompt tokens consumed by the child across extensions and follow-ups   |
| `CacheReadTokens`   | Cumulative cache-read tokens consumed by the child across extensions and follow-ups        |
| `CacheCreateTokens` | Cumulative cache-create tokens consumed by the child across extensions and follow-ups      |
| `StopReason`        | Populated on partial: `"max_turns"` or `"max_tokens"` |
| `Error`             | Populated on failure                                  |

The `follow_up` handler seeds `Spec.PriorTokenUsage` from the stored `ChildSession.TokenUsage`, so these token counters report the child agent's whole-life totals.

**ToolRetention** persists on the parent conversation message as metadata that is not sent to the provider:

| Field        | Description          |
|--------------|----------------------|
| `Kind`       | `"delegate_summary"` |
| `Summary`    | Condensed findings   |
| `AgentID`    | Child agent ID       |
| `Status`     | Result status        |
| `TurnCount`  | Turns consumed       |
| `TokenCount` | Tokens consumed      |

**Summarisation turn.** After the child completes, a follow-up single-turn (no tools allowed) asks the model to produce a concise summary. If the summarisation turn fails or returns empty, the raw output is truncated to 4000 runes as a fallback.

**Retention path.** The child agent's full transcript is not copied into the parent session. The parent keeps the delegate result plus a bounded summary. Compaction may later summarise older parent conversation state, including delegated work, through the normal baseline path.

### Host-side diagnostics vs. the model-facing result

`Result` (above) is what the parent **model** sees. It never carries tool-call traces, per-tool counters, or internal file paths — issue #601 explicitly bars provider-visible trace entries, tool-call counts, and internal paths from that contract. Diagnostics that would otherwise be useful only to a human, the TUI, or an offline script live on a separate **host-side** channel instead: the existing lifecycle debug log (`TraceLogger`/`traceCollector`, `internal/delegation/trace_log.go`), written to `<log-file>-delegation.log`. `SpawnDelegate` and `failedDelegateExecution` (`internal/delegation/task.go`) each append one additional `tc.add("tool_calls", …)` entry to that log carrying `trace_file`, `tool_calls_total`, `tool_calls_failed`, and a `tool_counts` map (tool name → call count) — never assigned to `Result` or any other model-facing field.

**Per-tool-call trace files.** A separate mechanism from `TraceLogger` records one JSONL line per tool call (not per lifecycle event) for a delegated child, at `.steiner/traces/<session-id>/<agent-id>.jsonl` (`internal/delegation/tool_call_trace.go`, `toolCallTraceWriter`). Each line is `{time, turn, tool, arg_bytes, result_bytes, ok, duration_ms, fail_class}` — sizes only, never argument or result bodies. `<session-id>` is a random 12-hex-character ID generated once per process (`processTraceSession`, `sync.Once`) and reused for every child spawned by that process run; there is no true interactive-session ID threaded into `internal/delegation` today, so this is a process-scoped stand-in. Trace session directories older than 7 days are pruned (best-effort) whenever a new writer is created.

The writer is constructed once per delegation in `buildChildRunRequest` and wraps the child's scoped event sink (`scopedToolCallTraceSink`), so it observes the same `ToolCallStartedEvent`/`ToolCallFinishedEventWithPreview` pairs (keyed by `CallID`) already emitted per tool call from `internal/agent/turn_progression.go` — no new plumbing into `internal/agent` was needed. A package-level registry (`registerToolCallTraceWriter`/`takeToolCallTraceWriter`, keyed by `AgentID`) lets `SpawnDelegate` retrieve and close the writer without a `BuildChildRun` signature change; a delegation cancelled before dispatch never reaches `SpawnDelegate`, so it leaves one small map entry and one open file handle for the process lifetime — a bounded, intentional tradeoff.

For `AgentTypeCode`, the child's `WorkDir` is overridden to its isolated git worktree, but trace files must never appear as untracked changes inside that checkout (it would corrupt the dirty-worktree check after remediation). `ChildBootstrapOverrides.ProjectRoot` carries the parent's actual project directory separately from `WorkDir` for this purpose; `buildChildRunRequest` places trace files under `ProjectRoot` (falling back to `WorkDir` when unset), never under the possibly-overridden `WorkDir` itself.

When a `mutate` call fails, the trace line's `fail_class` is set by matching the lowercased failure text against the same taxonomy and match order as `scripts/mutate-session-stats.mjs`'s `classify` function (`mutateFailClasses` in `tool_call_trace.go`) — the two lists must stay in sync. `fail_class` is omitted for every non-mutate call and every successful call.

### Cancellation finalization

Cancellation finalization handles an explicit targeted discard request. Without that request, a cancelled child leaves its session, result, and code worktree untouched, so the session remains available for `follow_up`. An explicit targeted code discard is recorded on the active controller; stop-all never sets that request.

The cancellation path waits for the child runner, including a cache-gated child, to return before finalization. For an explicit discard, finalization then proceeds in this order:

1. **Invalidate the session.** `SessionStore.Invalidate` deletes the stored session and creates an agent-ID tombstone. `Get`, `Save`, and `Update` are blocked for that ID until `Reset`, including late persistence from the cancelled child, so an invalidated session cannot be resurrected.
2. **Mark the result non-resumable.** `SessionResumable` becomes false.
3. **Correct the retention summary.** The retention output and summary no longer claim that the session is preserved for `follow_up`.
4. **Derive the relative worktree ID from controller metadata.** The finalizer reads the controller-held `CodeWorktree`, then derives its path relative to the project's `.steiner/worktrees` directory rather than trusting a result field.
5. **Prune with ownership checks.** It calls the existing `PruneCodeWorktree` under a 30-second cleanup context rooted in `context.Background()`, independent of the cancelled child context. The prune accepts only a git-known worktree with a `delegate/` branch and an in-bounds relative ID.

A successful prune removes the checkout and branch. A failed disposal leaves the session tombstoned and preserves files for manual recovery. Each attempted disposal emits a `DelegationWorktreeDisposalEvent` scoped with agent ID and, when available, agent type; the payload reports whether removal occurred and any error. A disposal event is separate from the child row's terminal lifecycle.

### Vision handler

The `vision` tool uses a dedicated handler (`newVisionHandler`) rather than the generic `newSpecializedHandler`. When invoked:

1. Validates `task` and `image_id` inputs.
2. Looks up the `ImageRef` from `ImageStore` (registered when the image was pasted).
3. Reads the image file from `.steiner/tmp/images/` and base64-encodes it.
4. Builds a `Spec` with `Images` populated — the image is attached to the sub-agent's first conversation message via `buildChildPrompt`.
5. Resolves the per-type model from the selected profile's `sub_agents.vision` assignment.
6. Calls `BuildChildRun` and `SpawnDelegate` with the vision allowlist (`["read"]`, plus any `ExtraAllowedTools[vision]`) and vision system prompt.
7. Saves the child session so the parent model can use `follow_up` for additional questions about the same image without re-uploading it.
8. Appends a `follow_up` reminder (with `agent_id`) to the returned result.

**ImageStore** is a goroutine-safe session-scoped registry in `internal/agent/image_store.go`. It maps auto-assigned IDs (`img-1`, `img-2`, …) to `ImageRef` values (file path, media type, dimensions, size). Pasted-image files are owned by the store, saved to `.steiner/tmp/images/` with `YYYYMMDD_HHMMSS_<hex>.ext` filenames, and deleted on agent exit (`imageStore.Cleanup()`). Read-produced images may also be registered, but their files can be externally stored under `.steiner/tmp/fetched/` or in repository paths; `ImageStore.Cleanup()` does not delete those files. `.steiner/tmp/fetched/` has its own age- and budget-based retention sweep (`builtin.PruneFetchedDir`, run at startup); `.steiner/tmp/images/` is untouched by it. `ImageStore` is wired from the composition root through `DelegateDeps.ImageStore` → `SpecializedToolDeps.ImageStore` → vision handler.

### Event lifecycle

Events emitted during delegation (via `output.EventSink`):

| Event                  | When                                      | Key fields                                                  |
|------------------------|-------------------------------------------|-------------------------------------------------------------|
| `delegation_started`   | After registration, before dispatch gate  | `agent_id`, `agent_type`, `task_preview`, scoped agent/type  |
| `delegation_cache_waiting` | While blocked at cache dispatch gate   | `agent_id`, `call_id`, `deadline`                            |
| `stop_reason`          | Pre-dispatch stop                         | `reason: "cancelled"`, scoped agent/type                   |
| `delegation_extension` | Each auto-extension iteration             | `agent_id`, `extension`, `max_extensions`                   |
| `delegation_complete`  | After child terminal handling             | `agent_id`, `status`, `turn_count`, `token_count`, `output` |
| `delegation_failed`    | On child run error                         | `agent_id`, `task_preview`, `error`                         |
| `delegation_worktree_disposal` | After explicit code discard         | scoped `agent_id`, type, `removed`, `error`                 |

Events emitted through the child request's event sink are scoped with the child agent ID and type. The started event is also scoped, and its payload carries the type as a plain string so consumers such as the TUI can render it without importing delegation. A pre-dispatch stop uses the scoped `stop_reason` event and no child run starts; otherwise existing complete or failed terminal events remain in use.

The TUI renders delegation lifecycle events with a spinner during execution, lifecycle state labels, and collapsible output panels for completed delegations. Extension events update an always-visible counter in the status bar.

### Interactive cancellation and TUI state

Interactive cancellation is represented by `CancelDelegate{AgentID, Discard}` and `CancelAllDelegates` actions. `internal/interactive` depends on the consumer-defined `DelegateCanceller` interface; the `cmd/steiner` adapter records an explicit discard request, then cancels the selected controller context, or calls controller `CancelAll`. Whole-run interruption remains `InterruptActiveRun` through `ActiveRunController.Interrupt`; it is not routed through `DelegateCanceller` and its behavior is unchanged.

While the content state reports at least one active delegate, Esc, Ctrl-C, and Ctrl-D open the stop modal in conversation, approval, and other applicable input modes. With no active delegate, the existing interrupt or exit handling is used. The selector takes a copy of `contentBuffer.ActiveDelegateRows()`, in deterministic transcript order, rather than reading the controller map. Each row renders the bold tool-box-coloured type, agent ID, and truncated task preview. It offers targeted stop, stop-all, whole-run stop, and dismiss; confirmations provide keep-working, with code targets adding keep-worktree as the default and explicit discard.

A scoped cancelled stop finalizes the active transcript row before late child terminal events can change it. The worktree disposal event is rendered independently as a status outcome, so disposal success or failure does not keep the delegate row active and does not determine the row's transcript position. This keeps transcript ordering and the cancellation snapshot independent of controller registration-map iteration. Targeted cancellation and child completion linearize under the controller lock: cancellation accepted first retains discard intent through finalization, while completion first returns an already-finished outcome and retains the worktree. The interactive adapter surfaces that outcome instead of silently dismissing it; cancel-all never requests discard.

### System prompt integration

When delegation is enabled, the system prompt preamble includes a delegation instructions block (`delegationInstructions` in `internal/prompt/system.go`) cast as the orchestrator role, not a rules dump. It has five parts, in order: a role statement (the model's job is to orchestrate sub-agents — it plans, dispatches with complete briefs, and verifies and integrates output, preserving its context for orchestration, and it is not the default implementation worker); a specialists table (agent, lane, and what not to use it for, covering `explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`); a numbered workflow (an ordered step list — initial `explore`, clarifying questions, further research, a Goal/Assumptions/Scope/Unknowns summary, a plan, breaking the plan into single-logical-unit steps, one `code` sub-agent per step, a single `review`, amendments via `code` with re-review, and a final `sanity_check` — where the advisor consultation step renders inline only when the advisor is enabled, renumbering the later steps); a delegation-vs-direct-work section (delegate by default, with local work limited to genuinely self-contained actions — a bounded lookup, a self-contained formatting action, or a tiny user-directed correction — plus a worked examples table); and a briefing section with the six-field task template (Objective, Context, Deliverable, Constraints, Success criteria, Checks to run).

The block is gated on `delegationEnabled`, not on `workflowMode`:

| Caller | `WorkflowMode` | `DelegationEnabled` | Receives the role? |
|--------|----------------|----------------------|---------------------|
| Interactive parent | `ParentWorkflowMode()` | `true` | Yes |
| Oneshot phase | `DelegatedChildWorkflowMode()` | `true` (`cfg.SubAgent.Enabled`) | Yes |
| Delegated child sub-agent | `DelegatedChildWorkflowMode()` | `false` (never set by `buildChildPrompt`) | No |

Oneshot phases run under `DelegatedChildWorkflowMode()` but still orchestrate — they dispatch `code` sub-agents per step — so they need the role; a gate on `workflowMode` would incorrectly exclude them. Delegated children never set `DelegationEnabled`, so they correctly never receive it. `delegationEnabled` and `advisorEnabled` are both part of the cache key in `CachedSystemPreamble` (`internal/agent/context_manager_base.go`), so gating the delegation canon and its advisor step on them introduces no per-turn non-determinism into the prompt prefix.

### Constraints and invariants

1. **One level only**: children never have access to `follow_up` or `workflow_handoff`.
2. **No approval prompts**: child tool execution is auto-approved.
3. **Default context manager**: children use the same baseline context manager path as the parent.
4. **Tighten-only overrides**: caller cannot exceed configured limits, only reduce them.
5. **Model resolution**: non-explicit sub-agent aliases fall back to the selected profile's default assignment; specialised per-type model aliases resolve before the child run is built. Vision remains disabled when no vision assignment is configured. Startup `--model` and `STEINER_MODEL` overrides affect only the active orchestrator, not these profile role assignments.
6. **Synchronous execution**: each delegate runs to completion before control returns to the parent.
7. **Filesystem shared**: children operate in the same workdir as the parent.
8. **Extension cap**: maximum 3 auto-extensions to prevent runaway children.
9. **Summary cap**: retention summaries capped at 4000 runes.
10. **No conversation leakage**: child conversation is not appended to parent; only the structured result and retention summary persist.
11. **Enforced allowlist**: `ChildBootstrapOverrides.AllowedTools` is enforced during child registry construction; only listed tools (minus `follow_up` and `workflow_handoff`) are visible and executable.
12. **Per-type allowlists**: each specialised agent type has its own tool allowlist, resolved via `AgentAllowedTools(agentType)` and passed as `ChildBootstrapOverrides.AllowedTools` — there is no user-configurable global allowlist.
13. **Extra tool projection**: `DelegateDeps.ExtraAllowedTools` adds per-agent-type registered tool names to child registries. Nil or empty projections grant nothing; unknown names are ignored by `Registry.Subset`; merged lists are sorted and deduplicated without mutating the built-in allowlists; original ToolDef handlers and MCP provenance are retained.
14. **Parallel fan-out**: eligible parent tool calls may execute concurrently within `MaxParallelTools`; child runs remain serial because their `ParallelTool` predicate is nil. Results are applied in call order against a shared pre-batch snapshot.
