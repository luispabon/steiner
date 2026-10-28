# Context management internals

User guide: [Context management](../user/context-management.md).

Parent context is bounded by delegation (child transcripts stay out of the conversation), per-source byte budgets, and compaction into new conversation generations.

## Delegation

Child turns stay out of the parent transcript, and the parent receives a bounded result. See [Sub-agent delegation](sub-agent-delegation.md).

## Prompt assembly

The source order is fixed in `internal/prompt/source_plan.go`. Static sources come first so the prefix stays stable for cache reuse.

| Step | Source (`ContextSource`) | Budget | Notes |
|------|--------------------------|--------|-------|
| 1 | `preamble` | none | Memoized per session |
| 2 | `global_agents_md`, `project_agents_md` | none | Never truncated |
| 3 | `project_context` | 8000 bytes (`fallbackProjectContextBudgetBytes`) | Truncated when over |
| 4 | `skill` | 98304 bytes (`defaultSkillBudgetBytes`) | Exec and oneshot only |
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
- A body over `prompt.SkillContentCapBytes` is cut on a UTF-8 rune boundary and ends with `[truncated: skill exceeded the 98304-byte cap]`. Loading such a skill emits `skill_truncated` with the original size and the cap.
- On resume, replayed envelopes emit `skill_state` events (`internal/interactive/replay.go`).

## Session date assembly

- `prompt.NewSessionDate` (`internal/prompt/session_date.go`) builds the date. Sub-agents receive the parent's value through the `SessionDate` field of the delegation dependencies (`internal/delegation/registry.go`).
- The date block sits after the static sources and before the conversation, and is never budgeted. It is user role, so it stays out of the system-zone prefix. It merges into an adjacent user block, or starts one after a system-zone phase prompt.

## Token estimation

`EstimateChatRequestTokenEstimate` (`internal/provider`) returns a `TokenEstimate` with `Tokens`, `RawTokens`, `Strategy`, and `Confidence`.

- Model IDs in `ctokActiveModelIDs` (`internal/provider/token_estimator_claude.go`, exact match after whitespace trimming) use `claudeTextCounter`. It calls ctok `CountContent`, which omits the single-message framing that ctok `Count` adds. Steiner does not add that framing back. Strategy `ctok`, confidence `low`.
- The ctok counter loads once. A failed load is returned on every call. The counter never falls back to another tokenizer.
- Calibration: `ObserveUsage` records a per-model ratio from provider usage, and the strategy becomes `provider_usage`. Once samples exist, `calibrationForModel` scales whichever base estimate is selected, including ctok estimates. `Tokens` is calibrated and `RawTokens` is not.
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

`ContextState` (`internal/agent/context_state.go`) has three fields:

| Field | Contents |
|-------|----------|
| `RecentToolCalls` | Up to three `summarizeRecentToolCalls` entries from the latest lineage generation, oldest first. Each is a tool name plus summarized arguments. Rebuilt every turn by `enrichContextState`. |
| `TurnCount` | Turn count of the run. |
| `CompactionCount` | Copied through state but never incremented in non-test code. The live per-run count is `turnProgressor.compactionCount`. |

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
