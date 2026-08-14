# Context Management — Internals

User-facing documentation: [Context Management](context-management.md).

Every turn in the main conversation accumulates tokens — model output, tool calls, tool results. Long contexts cost more, degrade reasoning quality, and eventually hit provider limits. Steiner counters this in three lines of defense, ordered by effectiveness:

1. **Delegation** — work never enters the parent context at all.
2. **Per-source byte budgets** — when context does accumulate, budgets prevent any single source from dominating.
3. **Compaction** — when the context window approaches 70%, older turns are summarised and replaced.

---

## Delegation as context management

Sub-agents are the primary mechanism for keeping the parent context lean. When the model delegates exploration, code changes, or research to a child agent, the full turn-by-turn transcript of that work never enters the parent conversation. Only a structured result and a bounded summary (≤1000 chars) return.

Contrast this with doing the same work inline: every `read`, `grep`, `bash`, and model response accumulates as permanent conversation history, rapidly filling the window and triggering compaction. Delegation avoids the problem entirely — it's context management by isolation.

### How delegate summaries persist

1. After a child agent completes, a **summarisation turn** asks the child model to summarise its work in ≤1000 characters.
2. The summary is stored as `ToolRetention` (`kind: delegate_summary`) on the tool result message.
3. This retention is attached to the parent's `Message.Retention` field when the tool result is ingested.
4. During prompt assembly, delegate summaries are rendered under the `ToolSummaryBytes` budget (default: 1024 bytes).
5. When compaction occurs, delegate summary messages in the retained recent turns survive implicitly — the compacting model sees them as part of the conversation it summarises.

Child agents use the same prompt assembly and compaction path as the parent for their own (ephemeral) contexts, but those child transcripts are discarded when the child exits. Nothing leaks back to the parent beyond the final structured result.

---

## Prompt assembly

Each turn, steiner assembles the full context through a 7-step ordered plan. The order is intentional — static sources come first to maximize KV-cache reuse in local inference servers:

| Step | Source | Budget | Bypasses budget? |
|------|--------|--------|-------------------|
| 1 | System preamble | — | Yes |
| 2 | Agent definitions (global + project `AGENTS.md`) | — | Yes |
| 3 | Project context files | 8000 bytes | No |
| 4 | Skills | 16384 bytes | No |
| 5 | Oneshot phase prompt (if applicable) | — | Yes |
| 6 | Conversation history | — | No (pass-through) |
| 7 | Tool summaries (including delegate summaries) | 1024 bytes | No |

Each step with a budget is tracked by a `budgetTracker`. When a source exceeds its allocation, content is truncated and a `Truncated` flag is set on the resulting `ContextBlock`. The system preamble, phase prompt, and AGENTS.md are never truncated.

`ContextSource` constants distinguish where each block originated: `preamble`, `phase_prompt`, `global_agents_md`, `project_agents_md`, `project_context`, `skill`, `tool_result`, `tool_summary`, and `delegation_result`.

---

## Compaction

Compaction is the last line of defense. When the estimated prompt tokens exceed **70%** of the context window, the agent loop pauses to compact.

### Two-stage strategy

| Stage | Turns retained | When used |
|-------|---------------|-----------|
| Normal | 3 most recent turns | First attempt |
| Emergency | 1 most recent turn | Normal didn't fit or still over budget |

Both stages work the same way:

1. Older turns are split from the retained recent turns.
2. The older turns are assembled through the normal prompt assembly path, then a final `user` message is appended asking the model to summarise them. The compaction request carries the same tool definitions and generation params as a normal turn, so the call replays the identical cached prefix (system + tools + conversation) and hits the prompt cache. This holds for manual `/compact` too: the interactive session reuses the runner's `PromptAssembly` options rather than building its own, so both paths assemble from the same skills, project context, and preamble settings. Normal mode uses a detailed 8-section handoff (task, repo state, work completed, decisions, problems, remaining work, verification, preferences). Emergency mode appends extra guidance to be shorter and more lossy. When `cave_human` is enabled, the compaction prompt uses a purpose-built compaction voice block in place of the standard instruction, producing denser summaries (dropped articles, `key=value` shorthand, semicolons over sentences) instead of the verbose default.
3. The model's summary becomes a `MessageRoleSummary` prefix on a new `ConversationGeneration`.
4. The summary is appended to `ContextState.RetainedSummaries` with source `compaction:{generationID}/{view}`.
5. A new `ConversationGeneration` is created containing just the summary prefix + retained turns. The old generation is preserved in `ConversationLineage`.

`ConversationLineage` keeps all generations — nothing is ever pruned. This means the full conversation history is theoretically recoverable, but subsequent turns only see the latest generation.

### Durable context

During compaction, `RetainedSummaries` from previous compactions are included by the same prompt assembly path used for normal turns. This ensures the compacting model has awareness of earlier work even though the original transcript is gone from the active generation.

### Escalation policy

Compaction is tracked per-session. After multiple compactions the session becomes increasingly fragile:

| Compactions | Budget health | Severity | Session state | Guidance |
|-------------|---------------|----------|---------------|----------|
| 0–1 | Stable | info | stable | Continue |
| 1 | Fragile (20%+ over threshold) | warning | fragile | Restart soon |
| 2 | Stable | warning | fragile | Restart soon |
| 2 | Fragile | critical | likely_lossy | Restart now |
| 3+ | Any | critical | likely_lossy | Restart now |

A restart resets the compaction counter and the context window — it's the ultimate escape hatch when compactions can't keep up.

Context diagnostics for these states are emitted as typed sub-events: budget, compaction, session-health, and file-annotation payloads all share the same top-level event type while keeping their serialized fields specific to the diagnostic being reported.

---

## Durable context state

`ContextState` carries information that survives compaction across the session:

| Field | Purpose |
|-------|---------|
| `RetainedSummaries` | Accumulated compaction summaries; fed into subsequent compaction prompts |
| `FileTrackerSummary` | Names of files the agent has read or modified |
| `RecentToolCalls` | Tool names used in recent turns; refreshed each turn from lineage |
| `TurnCount` | Total turns in the session |
| `CompactionCount` | How many times compaction has run |

`ContextState` is cloned on each turn. Compactions append to `RetainedSummaries` and increment `CompactionCount`. `RecentToolCalls` is rebuilt each turn from the current lineage generation's messages.

---

## Byte budget reference

| Budget | Default |
|--------|---------|
| System preamble | 4096 (never truncated) |
| Project context | 8000 |
| Skills | 16384 |
| Tool results | 2048 |
| Tool summaries (incl. delegate) | 1024 |
| Compaction summary | 1024 |

Budgets are configurable via `AssemblyPolicy` in the prompt package. Zero values fall back to these defaults.

---

## Invariants

- **Lineage never pruned**: all `ConversationGeneration`s are preserved; the active generation is the latest.
- **Preamble never truncated**: the system prompt is always delivered in full and bypasses the budget tracker.
- **Delegate transcripts never leak**: child agent conversation is discarded on exit; only the structured result and bounded summary persist.
- **Children use the same compaction path**: sub-agents have the same context manager and compaction logic as the parent.
- **Compaction is lossy but bounded**: summaries are capped.
- **70% threshold**: compaction triggers when estimated prompt tokens reach 70% of the context window, reserving headroom for the model response.
