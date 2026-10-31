# Context management internals

User guide: [Context management](../user/context-management.md).

Parent context is bounded by delegation (child transcripts stay out of the conversation), per-source byte budgets, and compaction into new conversation generations.

## Delegation

Child turns stay out of the parent transcript, and the parent receives a bounded result. See [Sub-agent delegation](sub-agent-delegation.md).

A `documentor` child selects `DelegatedDocumentorWorkflowMode`, so `sectionDocumentorChild` renders its duties and write-scope text into that child's memoized preamble. Prompt composition is in [Sub-agent delegation internals](sub-agent-delegation.md#canon-and-workflow); the write rules are in the [user guide](../user/sub-agent-delegation.md#documentor-type).

## Prompt assembly

The source order is fixed in `internal/prompt/source_plan.go`. Static sources come first so the prefix stays stable for cache reuse.

| Step | Source (`ContextSource`) | Budget | Notes |
|------|--------------------------|--------|-------|
| 1 | `preamble` | none | Memoized per session |
| 2 | `global_agents_md`, `project_agents_md` | none | Never truncated |
| 3 | `project_context` | 8000 bytes (`fallbackProjectContextBudgetBytes`) | Truncated when over |
| 4 | `skill` | 106496 bytes (`defaultSkillBudgetBytes`) | Exec and oneshot only |
| 5 | `phase_prompt` | none | Oneshot phases |
| 6 | `session_date` | none | See [Session date assembly](#session-date-assembly) |
| 7 | `conversation` | none | Pass-through |

- `applyBudget` (`internal/prompt/source_render.go`) marks over-budget project context and skill blocks as `Truncated`. `AssemblyPolicy.Budgets` overrides the defaults; negative values are rejected (`internal/prompt/budget.go`).
- `IsSystemZone` covers the preamble, phase prompt, both AGENTS.md sources, and `conversation_summary`. Other sources are user role (`blockMessage`). `renderBlocks` merges consecutive blocks of the same role.
- Interactive sessions pass no skill names to static assembly. The enabled set is held by `interactive.Skills` (seeded from `Dependencies.SkillNames`) and reaches the model through the envelopes below.

### Preamble and static cache

- `CachedSystemPreamble` (`internal/agent/context_manager_base.go`) rebuilds the preamble only when an input changes: override, delegation enabled, async sub-agents, orchestration level, advisor, LSP, workflow mode, system suffix, sandbox enabled, or sandbox writable mounts.
- File-backed sources live in `prompt.StaticContextCache` (`AssemblyOptions.CachedStaticContext`). Each partition reloads only when its key changes: AGENTS.md paths; project root, budget, extra and ignore files; skill roots and enabled skill names. The skill key excludes `SkillsBundledFS`, so a cache must always pair with one bundled FS.
- A change to `AssemblyOptions.StaticContextScope` drops every partition (`beginScope`, `internal/prompt/assembler.go`).
- Caches are created per interactive session, per oneshot phase, per exec run, and per delegated child.

### Interactive skill delivery

Interactive sessions keep skills out of the static prefix, so toggling a skill never changes the cacheable part of a request. `internal/interactive/skill_injection.go` puts envelopes ahead of the user's text in the next submitted message:

```
<steiner-skill name="..." state="active|inactive" bytes="N">
...
</steiner-skill>
```

- `bytes` is the body length, so envelopes are length-delimited.
- On submit, `skillDeltaBlocks` diffs the enabled set against `EffectiveSkills`. Deactivations come first, then activations for newly enabled skills. Existing messages are never rewritten.
- `EffectiveSkills` folds envelopes across user messages in order. An activation replaces that skill's block and moves it to the end. A deactivation removes it.
- `SplitSkillBlocks` (`internal/prompt/skill_blocks.go`) reads an optional leading mode notice and then envelopes. It stops at the first malformed envelope and keeps the rest of the text as is.
- A body over `prompt.SkillContentCapBytes` is cut on a UTF-8 rune boundary and ends with `[truncated: skill exceeded the 106496-byte cap]`. Loading such a skill emits `skill_truncated` with the original size and the cap.
- On resume, replayed envelopes emit `skill_state` events (`internal/interactive/replay.go`).

## Request event snapshots

`output.NewAPIRequestEvent` (`internal/output/event_model_call.go`) builds the per-request event behind the context report, and `interactive.snapshotSink` (`internal/interactive/wiring.go`) stores it for later reporting.

- `MessageHashes` are the first four bytes of `sha256` over `provider.AppendMessageHashInput`. One buffer is reused across a request, so the hash input is never materialized as a string, and cache diagnostics hashes messages through the same append path.
- `PromptBytes` is the sum of each message's role and content length. `BlocksBytes` is the sum of `ContextBlock.ByteSize`.
- `ToolsBytes` is the encoded length of the tool definitions, computed by `json.Marshal` on every request. Tools are re-cloned into each request, so the encoding is not memoized by slice identity: an in-place edit of a reused slice would otherwise report a stale size.
- Messages, tools, blocks, and the `MaxTokens` value are deep-copied into the event, so later mutation of the conversation cannot reach it. `snapshotSink` then stores its own deep copy through `SnapshotStore.Store`, because every later sink receives the same payload and nothing guarantees it stays unmodified.
- `Snapshot` and `ParentSnapshot` clone before returning, so a reader cannot mutate stored state.

## Session date assembly

- `prompt.NewSessionDate` (`internal/prompt/session_date.go`) builds the date. Sub-agents receive the parent's value through the `SessionDate` field of the delegation dependencies (`internal/delegation/registry.go`).
- The date block sits after the static sources and before the conversation, and is never budgeted. It is user role, so it stays out of the system-zone prefix. It merges into an adjacent user block, or starts one after a system-zone phase prompt.

## Token estimation

`EstimateChatRequestTokenEstimate` (`internal/provider`) returns a `TokenEstimate` with `Tokens`, `RawTokens`, `Strategy`, and `Confidence`.

- Model IDs in `ctokActiveModelIDs` (`internal/provider/token_estimator_claude.go`, exact match after whitespace trimming) use `claudeTextCounter`. It calls ctok `CountContent`, which omits the single-message framing that ctok `Count` adds. Steiner does not add that framing back. Strategy `ctok`, confidence `low`.
- The ctok counter loads once. A failed load is returned on every call. The counter never falls back to another tokenizer.
- Calibration: `prepareTurn` fits the normal request once per model call and `executeChatRequest` reuses its raw count through `ChatRequest.RawPromptTokenEstimate`. The request is re-estimated only when `stripImagesIfVisionDisabled` changes the request, on the inline vision retry, or for compaction requests. A direct call without a prefit estimates the request itself. `ObserveUsage` records a per-model ratio from provider usage, and the strategy becomes `provider_usage`. Once samples exist, `calibrationForModel` scales whichever base estimate is selected, including ctok estimates. `Tokens` is calibrated and `RawTokens` is not.
- Fit checks use `Tokens`. `RawEstimatedPromptTokens` keeps the raw value for events and diagnostics.

## Compaction

Compaction runs when the context is too large. Two limits apply:

- **Soft trigger**: `compaction_threshold_tokens` when set (it must be below the effective context window), otherwise 70% of the window. Native providers compare it with the estimated prompt (`shouldCompactPrompt`, `internal/prompt/token_budget.go`). Stateful providers compare it with reported usage (see below). Field rules are in the [configuration reference](../user/configuration.md).
- **Hard fit**: `ModelTokenBudget.FitRequest` computes the calibrated prompt plus the completion reserve plus `SafetyMarginTokens`, and the total must fit `ContextSize`. Compaction requests are checked with `FitCompactionRequest`, which uses its own reserve.

`explicitMaxTokensValue` takes the first positive limit in this order: `MaxTokens`, then the `max_tokens`, `max_completion_tokens`, or `max_output_tokens` keys in `ExtraParams`, then the same keys in `Params`. `lowerToExplicitMaxTokens` lowers the reserve to that value when it is smaller, or sets the reserve when it is unset. With no explicit value, `clampUnsetCompletionReserve` clamps the reserve to the space left after the prompt and safety margin. The clamp floor is `min(reserve, provider.DefaultMaxOutputTokens)`.

### Stages

| Stage | Turns retained | Used when |
|-------|----------------|-----------|
| Normal | 3 most recent | First attempt |
| Emergency | 1 most recent | Normal did not apply, or the result still needs compaction |

- Older turns are assembled through normal prompt assembly, followed by a final user message that asks for a summary. The request carries the same tool definitions as a normal turn (`provider.CloneTools`).
- Normal mode uses `internal/prompt/templates/compaction_default_body.md.tmpl`, which asks for eight numbered sections: task and goal, repository state, work completed, key findings and decisions, problems, remaining work, verification, and user preferences. Emergency mode uses `compaction_emergency.md.tmpl` and asks for a shorter, lossier summary.
- `deriveSummaryMaxTokens` (`internal/provider/resolved_model.go`) caps summary output as a share of the context window, bounded by a minimum and maximum, and never above the model's max output.
- The summary becomes the `SummaryPrefix` of a new `ConversationGeneration`, followed by the retained turns. `ConversationLineage` keeps every generation. The latest one is active.
- Manual `/compact` uses the runner's prompt assembly options, so skills, project context, and preamble match a normal turn. Its tools come from `snapshotTools` (`internal/interactive/compaction_helpers.go`), which reads the last recorded parent request. With no parent request recorded, the tool list is empty.
- A main summary request carries the latest generation's backend state. For a stateful provider, an in-place summary retires the generation's live transcript. `summarizeCompactionStage` records that retirement in `CompactionOutcome.BackendStateCleared` independently of `Applied`, reading the response's `ClearBackendState`, the request-scoped signal from `provider.WithBackendStateRetirement`, and an error already marked by `provider.BackendStateRetired`. A failed or cancelled summary may deliver or lose its terminal chunk, so the signal and the marker report the retirement when the chunk is lost.
- `clearRetiredBackendState` drops only the latest generation's backend state, including when a stage applies nothing or fails. On those paths the empty or failed outcome's conversation and pending archive are not adopted, and generation IDs do not change. An error that follows a clear keeps its text and chain and is marked, so `provider.BackendStateRetired` reports it. `CompactWithLineage` reports the clear as its `cleared` result. Manual compaction (`manualCompaction`, `internal/interactive/compaction.go`) drops the state when that result is true or the error satisfies `provider.BackendStateRetired`.

### Compaction fallback

`RunRequest.CompactionFallback` names a second model for compaction summary calls. The zero value disables it. The CLI runner sets it per run request (`cliRunner.compactionFallback`, `cmd/steiner/runner_run.go`), and delegated children set it through `delegation.NewCompactionFallback` (`internal/delegation/compaction_fallback.go`, called from `internal/delegation/bootstrap.go`). Config fields are in the [configuration internals](configuration.md#compaction-fallback).

Selection (`compactWithFallback`, `internal/agent/compaction_fallback.go`):

- Selection runs once per compaction operation. The whole two-stage operation (normal, then emergency when needed) runs on the same target, and both stages share one summary request.
- Nothing is read or resolved when `CompactionFallback.After` is not positive or `Resolve` is nil.
- The clock is `RunRequest.Clock` when set, otherwise `time.Now`. `compactionCacheCold` treats the cache as cold when no main request is recorded (zero `LastMainRequest.At`), or when the idle time is greater than `After`. A warm cache runs on main.
- For a cold cache, `selectCompactionFallback` resolves the target. A resolve error, or a target with no provider, fails the attempt (see rerun below). A target with the same provider alias and backend model ID as main is not selected, and the operation runs on main.
- When the fallback applies a summary, the outcome records `CompactionModel` (`ProviderAlias/BackendModelID`), `FallbackStatus` `used`, and `FallbackReason`. A selected fallback that applies nothing returns without those fields or a main rerun. The reason is `no recent request` when no main request is recorded. Otherwise it is `idle <m>m > <n>m` in whole minutes. When rounding makes the two minute values equal, it uses exact durations (`idle <duration> > <duration>`) so the reason still shows idle greater than the threshold.

Summary target and main (`summarizeCompactionStages.run`, `summarizeCompactionStage`):

- Retention split, the anchor move, and fit checks on the retained tail and resulting state use the main request.
- The summary output cap (`compactionSummaryMaxTokensForMode`) and the plan fit (`newCompactionExecutionPlanWithMode`, `FitCompactionRequest`) use the summary request's `ModelBudget`. On fallback that is the target's budget.
- A plan that does not fit the target returns `errCompactionFallbackNotFit`. On main, the same failure is a not-applied outcome, and the emergency stage runs.

Rerun (`compactionOnMainAfterFallback`):

- A failed attempt (fit failure, provider error, resolve or build error, or empty fallback summary) is discarded. The whole operation reruns on main from the original `RunState`. The outcome then records `FallbackStatus` `skipped`. `FallbackReason` is `context too large for <ref>` only when the error is `errCompactionFallbackNotFit`. Any other error gives `fallback failed: <error>`.
- No rerun happens when `ctx.Err()` is set (parent cancellation or the turn timeout) or the error is `context.Canceled`. The error is returned as is.
- An error from the main rerun is returned.

Backend isolation (`compactionFallbackRequest`):

- The fallback request is a copy of the run request, used for summary calls only. It takes the target's provider, resolved model, budget, and compaction prompt override. Tools, conversations, and string lists are cloned. It clears `ParentTransportSession`, sets `CacheBaseline` to nil, and clears `CompactionFallback`. It is marked `compactionFallbackTarget`, and it is never passed to `Runner.Run`.
- Provider, executor, context manager, event sink, and `DrainInbox` are shared with the main run.
- `buildCompactionRequestWithMode` sets `BackendState` to nil for fallback targets, so a fallback summary does not send the main generation's backend state. The retirement signal, `ClearBackendState`, and `BackendStateCleared` apply only to main summaries: `compactWithFallback` discards a fallback outcome's clear, and `hideFallbackRetirement` hides a fallback error's marker when cancellation returns that error. `TransportSession` comes from `compactionFallbackIdentity` when the main session is set, and a fresh random key otherwise, so a stateful provider never treats a fallback summary as the main session.
- Fallback calls write `cold_start` false and leave the process-wide cold-start latch alone (`emitCacheDiagnostic`).
- Cache and transport identities for the fallback request are in [prompt caching internals](prompt-caching.md#compaction-fallback-identities).

`LastMainRequest` (type in `internal/agent/run_request.go`, recorded in `internal/agent/turn_progression.go`, carried by `internal/agent/conversation_driver.go`, seeded in `internal/agent/runner.go`):

- `turnProgressor.executeModelCall` sets `LastMainRequest` after each successful main model call, with `At` from the injected clock. Compaction calls, fallback calls, and failed calls do not set it.
- `ConversationDriver` carries the value across episodes. `finish` replaces it when an episode succeeds or its output is non-zero. Compaction and lineage advancement leave it unchanged. `LastMainRequest()` returns it.
- `seedLastMainRequest` keeps `RunRequest.SeedLastMainRequest` only when its provider alias, backend model ID, and `PromptCacheKey` all match the request. Otherwise the seed is zero. `Runner.Run` and `Runner.CompactWithLineage` apply it.
- Seeds come from the previous owner. Interactive `driverRun` (`internal/interactive/driver_run.go`) keeps its seed when a failed run returns zero. Manual `/compact` (`manualCompaction`, `internal/interactive/compaction.go`) passes the driver's `LastMainRequest()`. Delegated children keep `ChildSession.LastMainRequest` (`internal/delegation/session.go`) and pass it to continuation requests (`buildContinuationRequest`, `internal/delegation/follow_up.go`). A oneshot phase starts at zero (see [oneshot internals](oneshot.md#phase-driver)).

Memoization (`CompactionFallbackTargets`, `internal/delegation/compaction_fallback.go`):

- Built providers are memoized by provider alias, backend model ID, and cache scope (`compactionTargetKey`). The cache scope is the main run's `PromptCacheKey`. For a delegated child, that is the child's cache key (`childCacheKey`, `internal/delegation/bootstrap.go`). Failed builds are not memoized. Targets are not closed.
- The session ID is not part of the key. The provider is built with the session ID of the first call for its key, and later calls with a different session ID reuse it.
- `Resolve` calls `ResolveModel` on every compaction that reaches it. Only the built target is memoized.

### Compaction history archive

`agent.HistoryArchive` (`internal/agent/history_archive.go`) is optional on `RunRequest`. `summarizeCompactionStage` calls `withHistoryNote` after a summary is returned and before it builds the next state. `withHistoryNote` removes old lines beginning with `HistoryNoteMarker`, calls `Note`, and adds the current archive note to the summary. A `Note` error emits a compact conversation warning and applies the summary without a note. A successful note keeps the source messages in `CompactionOutcome.pendingArchive`.

`compactConversationForBudgetWithSteering` adopts `outcome.State` before calling `commitHistoryArchive`. Appends happen in source order only after adoption. An append error emits the same `archive compacted history: ...` warning and stops further appends; the adopted state and its archive note remain in the conversation.

`session.HistoryArchive` (`internal/session/history_archive.go`) implements the interface for the session returned by its `sessionID` callback. `renderHistory` (`internal/session/history_render.go`) writes Markdown oldest first. It preserves user and assistant text, tool-call summaries and status marks, image placeholders, and active skill markers. It omits tool result bodies, reasoning, `display_file` calls, and sub-agent result bodies. Summary rendering removes history note lines, and skill envelopes are stripped from retained messages by `stripRetainedSkillBlocks` before active skills are re-injected into the new conversation generation.

The session store creates `history.md` in the per-session directory under `~/.config/steiner/sessions/<sessionID>/` when the first non-empty archive append succeeds. `HistoryPath`, `AppendHistory`, and `ReadHistory` are in `internal/session/store.go`. `finishRemovalLocked` removes that directory during session deletion and eviction. Existing compactions are not backfilled.

`wireInteractiveRunner` (`cmd/steiner/interactive_session.go`) supplies an archive keyed by the current interactive session. `phaseHistoryArchive` and `phaseRunner.RunPhase` (`cmd/steiner/cmd_oneshot.go`) key oneshot archives by the phase session. The exec runner leaves `historyArchive` nil, and delegated child runners do not receive one, so archiving is limited to interactive and oneshot phase runs.

### Retention anchor, sub-agents, and skills

- After the normal stage picks its tail, `anchorRealUserRetention` (`internal/agent/compaction_candidate.go`) moves the tail back to the latest real user message (`IsRealUserMessage`: user role, not a sub-agent result) if the cut left it in the summarized part. The extension is kept only if the extended tail still fits and something remains to summarize. The emergency stage does not anchor.
- `appendPendingSubAgentsLine` (`internal/agent/compaction.go`) appends one `sub_agent_result` user message listing pending sub-agents at the tail.
- Compaction strips skill envelopes from retained messages (`stripRetainedSkillBlocks`), then re-injects the active envelopes as one user message ahead of the retained turns (`replayedSkillMessage`, `internal/agent/compaction_skills.go`). `activeSkillBlocks` scans the whole generation, so an activation in the summarized part still counts. A disabled skill drops out at the next compaction.

### Stateful transcript providers

For `claude_subscription`, the CLI owns the transcript.

- `ParentUsage` (`internal/agent/parent_usage.go`) holds the last parent response's usage in memory. Interactive runs carry it into the next run (`internal/interactive/driver_run.go`). It is dropped when the model or context window differs, and it is never saved.
- After a stateful compaction, the first parent response arms `StatefulFloor` from its `PromptTokens` when that value reaches the selected soft threshold but not the cap. The selected threshold (`statefulUsageReachesSelectedThreshold`) is `compaction_threshold_tokens` when set, otherwise the percent threshold. The soft trigger then waits until usage reaches `min(floor + 10% of window, 90% of window)`.
- At or above 90% (`compactionCapUsagePercent`), the run is marked cap-reached. Its next compaction decision stops the run with `request exceeds context window` without calling the compactor, and emits a critical `likely_lossy` session health event (`statefulCapStop`).
- Summary requests for `claude_subscription` use `claudeSubSummaryTurn`; routing is in [Claude subscription](claude-subscription.md#compaction).

### Escalation and replacement

- `turnProgressor.compactionCount` (`internal/agent/turn_progression.go`) counts applied compaction stages in one run. Each applied outcome adds `outcome.StageCount` (1 or 2) in `compactConversationForBudgetWithSteering`, so one two-stage compaction adds 2. It starts at zero in `newTurnProgressor`. Manual `/compact` starts from zero too.
- `compactionEscalationForFit` (`internal/agent/compaction_escalation.go`) maps the stage count and post-compaction fit to a health event. Below `compactionWarningThreshold` (2): `info`/`stable`, or `warning`/`fragile` when fragile. At 2: `warning`/`fragile`, or `critical`/`likely_lossy` when fragile. From `compactionCriticalThreshold` (3): `critical`/`likely_lossy`. A budget is fragile when the estimated prompt exceeds the hard limit by at least 20% (`compactionBudgetIsFragile`).
- `replacementGuardLocked` (`internal/interactive/driver_host.go`) refuses conversation replacement while driver admissions exist, the driver is busy, or sub-agents are still running (`pendingRefusalLocked`). A refused replacement returns before any swap.

## Durable context state

`ContextState` (`internal/agent/context_state.go`) has two fields:

| Field | Contents |
|-------|----------|
| `RecentToolCalls` | Up to three `summarizeRecentToolCalls` entries from the latest lineage generation, oldest first. Each is a tool name plus summarized arguments. Rebuilt every turn by `enrichContextState`. |
| `TurnCount` | Turn count of the run. |

- `Clone` deep-copies `RecentToolCalls`.
- Argument summaries cut values at 40 characters, and bash commands at 80. `cwd`, `path`, and bash commands are redacted against the absolute cwd root.
- The summary is not in `ContextState`. It lives in the active generation's `SummaryPrefix`.
- File tracking lives on `baseContextManager.fileTracker`, not in `ContextState`. `PostIngestion` rebuilds it from `read` results with a positive turn and never changes messages.
- `dedupReadResult` (`internal/agent/read_dedup.go`) replaces a fresh `read` result with a short note pointing at the turn of that read. The match requires the same path, line range, total lines, and `file_hash` as the most recent matching read. Line-capped reads and reads without `file_hash` are not deduplicated.
- `observeToolResult` (`internal/agent/context_manager_tool_result.go`) returns output from other tools as given.

## Invariants

- Lineage keeps every generation. The latest one is active.
- The cached prefix is byte-stable within a session: the memoized preamble, the static cache partitions, and the sorted tool definitions. Skill toggles change only the next user message.
- Child runs use `agent.Runner` and the same compaction path.
- Summaries are capped. Compaction is lossy, but its output size is bounded.
