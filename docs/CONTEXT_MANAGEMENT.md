# Context Management

steiner's context management system keeps the model focused and the prompt small without spending model tokens on meta-reasoning about what to keep or drop. The system is deterministic, configurable, and designed for small local models (7B-35B) with 8k-64k context windows.

## Modes

steiner supports two context management modes:

```
steiner --context-mode naive    # default
steiner --context-mode smart
```

**Naive mode** is the current behavior: full conversation history, model-based compaction at the hard limit, no gating. It exists as a baseline and safe fallback.

**Smart mode** activates the full context management pipeline: ingestion rules, observation masking, file read annotation, scratchpad, and structured compaction.

Both modes share the same provider, tool, and runner infrastructure. The context manager is an interface called at three points in the agent loop:

1. **PostIngestion** — runs once at session start on the initial loaded conversation. Per-tool-result shaping during a run is handled by `IngestToolResult` on `SmartContextManager`.
2. **PreAssembly** — before building the next model request, to filter the conversation view
3. **OnTurnComplete** — after each model response, to track whether the scratchpad tool was called (hybrid scratchpad mode only; no-op in scaffold_only mode)

In naive mode, all hooks are pass-through.

## Pipeline Architecture

Smart mode has two phases. The distinction matters: ingestion rules are destructive (they permanently reduce what is stored), assembly rules are non-destructive (the full history is retained, only the prompt view is filtered).

```
Phase 1: Ingestion (when tool output arrives)

    tool produces output
          │
          ▼
    truncate to size limit (strategy per tool type)
    strip noise (ANSI, duplicate blanks, repeated warnings, progress bars)
          │
          ▼
    processed result stored in conversation history


Phase 2: Prompt Assembly (when building the next model request)

    conversation history
          │
          ▼
    epoch-based masking (mask turns older than epoch boundary)
    mask old assistant prose (trim to first line)
    annotate unchanged file re-reads
          │
          ▼
    assemble prompt:
      system instructions (stable zone — cached per session)
      project context (skills, repo instructions)
      older turns (masked, token-stable between epoch advances)
      recent turns (verbatim)
      synthetic scratchpad user message (scaffold state; + model scratchpad in hybrid mode)
          │
          ▼
    prompt sent to model
          │
          ▼
    model responds (action; + scratchpad tool call in hybrid mode)
          │
          ▼
    scaffold state updated from tool call outcomes
    [hybrid mode] scratchpad tool result processed, decisions appended
    [scaffold_only mode] intent extracted via cheap second-pass on pivot turns
          │
          ▼
    OnTurnComplete fires
```

## Ingestion Rules

These apply once, when a tool result is received. They run in Go with no model calls.

### Tool output truncation

Bash and grep tool results are subject to a maximum size at ingestion. When output exceeds the limit, it is truncated with a marker so the model knows content was removed and can re-run with different parameters. Other tool types (read, ls, glob, edit, write) bypass ingestion truncation — their output is managed at assembly time.

Different tool types use different truncation strategies and limits:

| Tool type | Strategy | Limit | Rationale |
|-----------|----------|-------|-----------|
| bash | Tail-priority | 4096 bytes | Errors and failures appear at the end; cap avoids context flooding from large stdout |
| grep | Count cap | 200 results | Limit number of results, not bytes; preserves signal-to-noise ratio |
| default (read, ls, glob, edit, write) | None | — | Output size managed at assembly time (observation masking, file annotation) |

Truncation marker example:
```
<truncated output shown=4521 total=12830>
```

The marker is prepended for tail-priority strategies (bash) and appended for head-based strategies (grep count cap). `shown` is the byte-size of the truncated output after noise stripping, not the raw limit.

### Noise stripping

Applied after truncation. Removes content that is never useful to the model:

- ANSI escape codes and terminal color sequences
- Duplicate consecutive blank lines (collapsed to single)
- Repeated identical warning/info lines (replaced with count: `[previous line repeated 47 times]`)
- Progress bars, spinners, download progress output

## Prompt Assembly Rules

These apply every turn when building the prompt. They do not modify stored history.

### Observation masking (epoch-based)

Observation masking replaces old tool results and assistant prose with compact placeholders. Rather than advancing the masking boundary by one turn every turn (which invalidates the KV cache at the mutation point on every turn), steiner uses **epoch-based masking** to keep the masked section token-stable between epoch boundaries.

**Epoch mechanics:**

steiner tracks two values on `SmartContextManager`:
- `epochMaskBoundary` — the turn index below which all turns are masked
- `epochStartTurn` — the turn at which the current epoch began

**Epoch initialization** (on first call to PostIngestion or PreAssembly, in `initializeEpochFromTurnCount()` at line 429):
- Only initializes if `turnCount > 0` and both `epochStartTurn` and `epochMaskBoundary` are still zero (first run guard)
- Sets `epochStartTurn = turnCount` (current conversation turn count)
- Sets `epochMaskBoundary = turnCount - maskingWindow` (clamped to minimum of 0)
- Example: loading a conversation with 12 prior turns and `maskingWindow=5` sets boundary to turn 7, meaning turns 1-6 are already masked at session start

Between epoch advances, the masking boundary is frozen. The masked prefix of the conversation is byte-identical across turns, producing full KV cache hits from the system prompt through the entire masked section. The only cache miss each turn is the new content appended at the tail (new verbatim turns, updated scratchpad, current user message).

**Epoch advance triggers** (either condition fires):

1. **Turn count** (primary, always active):
   - Fires when: `currentTurn - epochStartTurn >= maskingWindow`
   - Example: if `maskingWindow=5` and epoch started at turn 5, the epoch advances when currentTurn reaches 10 (after 5 turns have elapsed)
   - Condition checked in `shouldAdvanceEpoch()` (line 446 in context_manager.go)
   - Trigger reason reported as `"turn_count"` in masking diagnostics

2. **Context pressure** (secondary, currently unimplemented):
   - Intended to trigger early epoch advances when estimated token usage approaches the context window limit
   - Placeholder mechanism: `contextPressureTrigger` function field on `SmartContextManager` is checked but never assigned (always nil)
   - When implemented, would fire when: estimated prompt tokens exceed a soft threshold (proposed: 80% of context window minus safety margin)
   - Would trigger `advanceEpoch()` even if turn count threshold hasn't been reached
   - Would report as `"context_pressure"` in masking diagnostics (see line 459 in context_manager.go)

**On epoch advance** (executed in `advanceEpoch()` at line 455):
1. Calculate new masking window: `newBoundary = currentTurn - maskingWindow` (clamped to minimum of 0)
2. Set `epochMaskBoundary = newBoundary` (all turns before this index become masked)
3. Set `epochStartTurn = currentTurn` (epoch clock resets)
4. All newly eligible turns (those between previous and new boundary) are masked in one batch
5. `PreAssembly()` emits a masking diagnostic event with:
   - `epochStatus = "advance"` 
   - `trigger` = either `"turn_count"` or `"context_pressure"` (whichever condition fired)
   - Count of newly masked turns in this batch

This produces one cache-invalidating mutation per epoch instead of one per turn.

**Masking behavior:**

Turns older than `epochMaskBoundary` have their tool result bodies replaced with a placeholder that includes the absolute turn index. The tool call metadata is preserved so the model knows what happened.

```
Turn 3: [tool_call] read internal/agent/runner.go
        [result] [tool result from turn 3 masked: read path=internal/agent/runner.go - re-read if needed]

Turn 3: [assistant]
        [turn 3] package agent...   ← first line only, with turn prefix

Turn 10: [tool_call] read internal/agent/runner.go
         [result] package agent...  (full content, recent turn)
```

M is configurable. Default is 5 (conservative starting point for steiner's target window sizes, smaller than the M=10 that worked for SWE-agent on frontier models). The minimum effective window is 2 — a configured value of 1 is silently raised to 2 to ensure at least enough recent context for coherent tool call sequences.

Scratchpad tool results are special-cased during masking: their content is always cleared (set to empty string) regardless of turn. The scratchpad state survives masking because it is injected from Go state at assembly time, not from conversation history.

**Trade-off:** Between epoch boundaries, context runs slightly larger than with rolling masking because masking of newly eligible turns is deferred. On a 32k window this is a modest overshoot — typically 3-5 extra unmasked turns for a few turns before the epoch catches up. The context pressure trigger (trigger 2) acts as a safety valve: if turns are unusually heavy, the epoch advances early rather than letting context grow unchecked.

**Interaction with retain_turns:** After compaction (drop strategy), 3 turns are retained. With M=5, the oldest retained turn begins masking 2 turns after compaction. This is intentional — the scratchpad carries orientation across the transition. If the model typically needs more turns to complete a sub-task post-compaction, increase `masking_window_turns` rather than hardcoding a higher `retain_turns`.

**Masking state tracking:**
- `previousBoundary` is captured before epoch advance so `PreAssembly()` can detect "newly masked" vs "previously masked" turns
- A turn is "newly masked" if: `trigger != "" AND turn > 0 AND turn >= previousBoundary AND turn < epochMaskBoundary`
- This distinction is reported in masking diagnostics (`epochStatus = "newly_masked"` vs `"previously_masked"`)
- Newly masked turns are counted in the advance event to show how many turns transitioned from visible to masked in this batch

Invariants:
- Tool calls and their results are atomic. If a tool call is present, either its full result or its masked placeholder is also present. They are never separated.
- Only the tool result body is replaced. The tool name and a summary of arguments are preserved in the placeholder so the model retains orientation.
- Assistant prose older than `epochMaskBoundary` is trimmed to its first line (with a `[turn N]` prefix). It is never dropped entirely — at minimum the first line is preserved.
- Masking operates on a copy. The stored conversation history is never modified.
- The masked section is byte-stable between epoch advances. No mid-epoch mutations.
- Epoch boundary only moves forward (never backward). `epochMaskBoundary` and `epochStartTurn` are monotonically increasing.

### File read annotation

File reads are the dominant source of context consumption (67-76% of total tokens in coding agent benchmarks). steiner tracks file metadata in Go via `FileTracker`:

- File path
- Turn number when last read
- Byte/line range (offset + limit)
- Modification timestamp at time of read
- Write generation counter (incremented on every steiner-initiated write or edit to this path)

When the model requests a file that was recently read and is unmodified since, steiner replaces the tool result with a short annotation:

```
[file unchanged since turn 5: lines 1-247 of 247 in /path/to/file]
```

This is the most aggressive option. The model can always re-read if it needs the content.

**Modification detection** uses three checks (all must pass for the annotation to be served):

1. **Filesystem mtime unchanged** — the file's modification time has not changed since the last read.
2. **Write generation unchanged** — the in-memory generation counter for this path has not been incremented by a steiner-initiated write or edit.
3. **Original read still visible** — the turn containing the original read has not been masked by epoch-based masking or dropped by compaction. If masked or dropped, full content is returned instead.

The generation counter is only bumped when a write or edit tool **actually modifies a file**. If the edit fails (e.g., old_string not found), the generation is not bumped and the read cache remains valid. The generation counter is bumped synchronously in the write/edit tool handlers before the tool result is returned, so it is always current.

Invalidation:
- External modifications (user editing outside steiner) change the file's mtime, which invalidates the tracking. The next read serves full content.
- File writes and edits by steiner's own tools increment the generation counter and invalidate tracking for that path.
- File tracking state (including generation counters) persists across compaction (it lives in Go, not in conversation history).

### Prompt zone stability

steiner splits every prompt into two zones:

- **Stable zone**: the system preamble (identity, scratchpad instructions, core rules), global agents file, and project AGENTS.md. Built once per session from session-constant inputs (`override` and `scratchpadEnabled` from config) and cached on `SmartContextManager`. The same byte string is used on every turn, enabling KV cache hits on the system prompt prefix across all providers that support prefix caching. Also included are compaction summary blocks when they exist. Project context and skill files are **not** cached — they are loaded fresh each turn from disk.

- **Volatile zone** (messages array): older masked turns (token-stable between epoch advances), recent turns verbatim, actual user message, project context blocks, skill blocks, synthetic scratchpad user message (appended as last message).

The epoch-based masking design ensures that the masked turns sub-section of the volatile zone is also effectively stable between epoch advances, extending the KV cache hit region beyond the system prompt into the conversation prefix.

A per-turn debug log (`slog.Debug("prompt zones", "turn", N, "system_bytes", X, "conversation_bytes", Z)`) records the byte sizes of each zone. The system count includes preamble, agents, and conversation summary blocks. Project context, skills, durable context, tool results, and the conversation messages are counted as conversation bytes. Enable with `--log-level debug`. See `docs/providers.md` for provider-specific KV cache behaviour.

## The Scratchpad

The scratchpad is a small block of text injected as a user-role message appended at the end of each prompt, after all conversation messages. It provides the model's "where am I" anchor, especially important after observation masking has removed old context or compaction has dropped conversation history.

steiner supports two scratchpad modes, configured via `scratchpad_mode`:

### scaffold_only mode (default)

The scratchpad consists entirely of scaffold-managed state. The model is not asked to call any scratchpad tool and the scratchpad tool is not registered. No scratchpad instructions are included in the system prompt.

**Scaffold state (~200-300 tokens)** — maintained by Go code, always accurate:
- Files the model has read (path, turn, modification status) — from FileTracker
- Current turn number and compaction count
- Recent tool call summary (what was run, which turn)
- Last action: the tool call the model just made, plus a truncated summary of the result (success/fail, line count, error snippet)
- Working file: the most recently read or edited path (from FileTracker)
- Momentum signal: iterating (same file, similar tool calls) or pivoting (new file, different tool type) — simple state machine over the last 2-3 tool calls

**Intent fields (~100-200 tokens)** — populated by a cheap second-pass inference on pivot turns only:

| Field | Description |
|-------|-------------|
| `intent` | What the model is doing and why (merges goal/plan/step) |
| `next` | Planned next action |

A **pivot turn** is detected heuristically when any of:
- The model switches to a different file (different path from the previous turn's primary file)
- The model changes tool type category (e.g. read -> edit, grep -> bash)
- The turn immediately follows compaction

On non-pivot turns, the previous intent fields are carried forward unchanged.

The second-pass inference uses a minimal context: scaffold state (~200 tokens) + the model's last response truncated to ~200 tokens + a focused prompt requesting JSON output. Max tokens capped at 150. Total input is ~600-800 tokens — sub-second on local inference. The prompt:

```
Given the current scaffold state and the model's last action, respond with ONLY a JSON object:
{"intent": "what is being done and why", "next": "planned next action"}
```

**Decisions field** — fully scaffold-managed via heuristic extraction from tool call outcomes:
- File edits: "edited {path}: {summary}" (from edit tool result)
- Test outcomes: "tests {passed|failed}: {summary}" (from bash tool result when command contains test/go test/pytest etc.)
- File switches: "switched from {old} to {new}" (from FileTracker)
- Compaction: "compaction occurred at turn {N}" (from compaction event)

Stored with a 2000-byte cap, oldest-first eviction. Appended by steiner, never written by the model.

**Total scratchpad size in scaffold_only mode: ~300-500 tokens.**

### hybrid mode (for 30B+ models)

Adds the model-written scratchpad layer on top of scaffold state. Use when model compliance with tool calls is reliable enough to justify the additional prompt space and system prompt complexity.

The scaffold state is identical to scaffold_only mode. The model is additionally instructed to call the `scratchpad` tool on every turn. The tool takes four fields:

| Field | Owner | Description |
|-------|-------|-------------|
| `intent` | model | What is being done and why (replaces the second-pass inference) |
| `decisions` | steiner-managed | Key decisions made so far; model writes only this turn's new decisions; steiner appends to history (2000-byte cap, oldest-first eviction). Never overwritten by the model. |
| `open` | model | Unresolved questions or risks |
| `next` | model | Planned next action |

steiner processes the tool result via `IngestToolResult`, stores state separately from conversation history, and injects the latest version as a synthetic user-role message on the next turn.

**Total scratchpad size in hybrid mode: ~400-600 tokens.**

### Failure handling (hybrid mode only)

Failure is defined as: the model did not call the `scratchpad` tool this turn. `OnTurnComplete(turnIndex int, scratchpadCalled bool)` is invoked after each model response. If `scratchpadCalled == false`, `SmartContextManager` increments a consecutive-miss counter; the counter resets to 0 on any successful call.

1. Miss: carry forward the previous scratchpad state unchanged (no event emitted; miss counter incremented silently)
2. After 3+ consecutive misses: emit a ScratchpadEvent with note "scratchpad tool not called in 3+ consecutive turns"

The system never crashes or loses state because of a missing scratchpad call. The scaffold state provides the factual safety net regardless of model cooperation.

In scaffold_only mode, there is no miss tracking because the model is never asked to call the tool.

## Compaction

Compaction is the fallback when masking alone is insufficient to keep context within limits.

### Trigger

Compaction fires when the estimated total token usage (prompt tokens + reserved completion tokens + safety margin) exceeds the model's context window size. There is no configurable fill-ratio threshold — the trigger is purely the hard capacity check after applying `safety_margin_tokens`.

```yaml
compaction:
  strategy: drop
```

### Strategies

Three compaction strategies are available, all behind a common interface:

#### drop (default for local models)

Zero-cost compaction. No model call.

1. Keep the last 3 turns verbatim (hardcoded)
2. Drop all older turns
3. Insert a discontinuity marker: `[context compacted - see scratchpad for task state; re-read files if needed]`
4. Reset epoch state: `epochMaskBoundary` and `epochStartTurn` reset to current turn

The scratchpad (scaffold state; and model-written state in hybrid mode) survives verbatim — it is injected at assembly time from Go state, not stored in conversation history. File tracking metadata (including generation counters) also survives.

#### summarize

Model-based compaction. The existing behavior from naive mode, enhanced with scratchpad awareness.

1. Feed the conversation to the model with a compaction system prompt
2. Model produces a summary that replaces the old conversation
3. Scratchpad is preserved alongside the summary

This costs one full-context model call. For local models, this is the most expensive possible operation. For frontier API models where inference is cheap relative to context cost, it may produce better continuity than `drop`.

**Fallback layers:** When the full conversation does not fit the compaction budget, the implementation escalates through three progressive fallback attempts before falling through to `drop`:

1. Mask the conversation with window=1 (heavily compressed) and retry
2. If still too large, replace the compaction system prompt with a short (`"Write a concise handoff summary for the next turn."`) version and retry
3. If still too large, fall through to `drop` strategy (zero-cost, guaranteed to succeed)

Layer 2 from the previous design (truncate each message body to 80 characters) has been removed. Truncating to 80 characters destroys content to fit a budget for a model call that will produce a low-quality summary from destroyed input — the `drop` strategy achieves the same information loss at zero model cost.

#### hybrid

Observation masking first, then conditional model summary. Best empirical results from research (43% of raw agent cost, +2.6pp solve rate improvement over pure masking).

1. Apply observation masking to the full conversation (reuses the same masking logic from assembly)
2. Check if the masked conversation fits within threshold
3. If yes, use the masked conversation (no model call)
4. If no, invoke the summarize strategy on the already-masked (smaller) input

The model call, when needed, processes a much smaller input than raw summarization because masking has already hollowed out old tool results.

### Invariants across all strategies

- Tool call + result pairs are never separated. Both kept or both dropped.
- Discontinuity marker is always inserted after compaction so the model is not confused by context gaps.
- File metadata tracking (including generation counters) persists across compaction (lives in Go, not conversation).
- Scaffold state and model scratchpad (hybrid mode) survive all strategies.
- Epoch state is reset on compaction (fresh epoch starts from retained turns).
- Escalation system tracks compaction count: info (1st), warning (2nd), critical (3rd+). At critical, steiner advises restarting in a fresh session.
- **Fragility bump:** When the post-compaction budget overage exceeds 20% of context size, the severity is bumped one level (info -> warning, warning -> critical) because the retained context is too fragile to be reliable.

## Interaction Between Components

The components are complementary and layer progressively:

1. **Ingestion** prevents the biggest offenders from entering history at full size. A 50k grep result is capped to 200 results at ingestion. This is the cheapest, most impactful reduction.

2. **Epoch-based masking** progressively hollows out turns as they age, advancing in batches to preserve KV cache stability. By the time a turn is past the epoch boundary, its tool results are one-line placeholders. Context grows slowly, and the masked prefix is byte-stable between epoch advances.

3. **File annotation** prevents the single largest token source (file reads, 67-76% of total) from accumulating. Unchanged re-reads cost ~20 tokens instead of hundreds or thousands. The write generation counter ensures annotation correctness even for sub-second re-reads after steiner-initiated writes.

4. **The scratchpad** ensures task continuity regardless of what has been masked or dropped. In scaffold_only mode, this is entirely deterministic. In hybrid mode, the scaffold state provides a factual safety net while the model adds intent.

5. **Compaction** is the hard reset when everything else is insufficient. By the time it fires, masking has already reduced most old turns to lightweight skeletons. Compaction drops these skeletons, preserves the scratchpad, resets epoch state, and the model continues with a clean slate plus full orientation.

## Configuration

Context management settings are configured at the top level under `context_management`:

```yaml
context_management:
  mode: smart                    # naive or smart (default: naive)
  compaction_strategy: drop      # drop, summarize, or hybrid (default: drop)
  masking_window_turns: 5        # M: turns before masking (default: 5)
  read_annotations: true         # annotate unchanged re-reads (default: true)
  scratchpad_mode: scaffold_only # scaffold_only or hybrid (default: scaffold_only)
```

Additional budget parameters live under the model's `compaction` block:

```yaml
models:
  default:
    model: qwen3-35b-a3b
    context_size: 32768
    max_completion_tokens: 8192
    compaction:
      safety_margin_tokens: 8192  # headroom before compaction trigger
      summary_max_tokens: 4096    # max tokens for summarization
```

Notes on configuration fields:

- **`scratchpad_mode`**: `scaffold_only` (default) uses only scaffold-managed state with cheap second-pass inference on pivot turns. `hybrid` adds model-written scratchpad fields. Use `hybrid` for 30B+ models with reliable tool-call compliance.
- **No `threshold`**: compaction fires when the prompt exceeds the context window (after applying safety margin); there is no configurable fill-ratio.
- **No `retain_turns`**: the drop strategy keeps the last 3 turns (hardcoded).
- **`safety_margin_tokens`** lives under `compaction`, not under `context_management`, and defaults to 8192 (not 2048).

The CLI flag `--context-mode naive|smart` overrides the config file setting.

## Observability

Smart mode emits structured events through the existing EventSink:

- **ContextMaskingEvent**: which turn and tool call was masked, why; includes epoch advance notifications
- **FileAnnotationEvent**: file path, whether annotation or full content was served, why (including generation counter mismatches)
- **CompactionEvent**: strategy used, token count before/after, turns dropped, epoch state reset
- **ScratchpadEvent**: scratchpad content after each model response; in hybrid mode, whether the scratchpad tool was called this turn (fired via `OnTurnComplete`); in scaffold_only mode, whether a pivot-turn second-pass inference was triggered
- **TokenBudgetEvent**: aggregate prompt token counts each turn (estimated prompt, reserved completion, safety margin, context size, total). No per-category breakdown is emitted.
- **EpochEvent**: epoch advance trigger (turn count or context pressure), new boundary, turns masked in this batch

Debug mode (`--log-level debug`) logs a one-line summary of per-zone byte sizes (`prompt zones`). Masking decisions and file annotation outcomes are emitted as structured events (ContextMaskingEvent, FileAnnotationEvent) regardless of log level. The full assembled prompt content and unmasked conversation are not logged at any log level.

## Design Rationale

### Why not LLM summarization as the primary strategy?

Empirical research (arXiv:2508.21433) tested observation masking against LLM-based summarization on SWE-bench across five model configurations. In four of five settings, masking matched or beat summarization while being up to 52% cheaper. Summarization also causes 4-15% trajectory elongation — the model re-explores because summaries introduce ambiguity.

For small local models, summarization has additional problems: it fills the entire context window for the summary call (the most expensive operation possible), and small models produce lower-quality summaries than frontier models.

### Why scaffold_only as the default scratchpad mode?

Research on structured output from small models (arXiv:2408.11061, arXiv:2510.03847) shows 82% average compliance for 8B models with high variance, format drift over long sessions, and field omission under token pressure. In practice, small models (7B-14B) frequently skip the scratchpad tool call — they focus on the task and omit the housekeeping step.

The scaffold_only approach accepts this reality: small models are good at using tools to do work, bad at metacognition. Instead of asking the model to introspect and self-report, steiner observes the model's actions and infers orientation from behaviour. The scaffold state is deterministic, always correct, and costs zero model tokens to maintain. Intent fields are populated by a cheap second-pass inference only on pivot turns (~15-20% of turns), keeping amortised cost negligible.

The hybrid mode is available for 30B+ models where tool-call compliance is reliable enough that model-written intent adds genuine value over scaffold-inferred intent.

### Why epoch-based masking instead of rolling?

Rolling masking (advance the boundary by one turn every turn) invalidates the KV cache at the mutation point on every turn. Everything downstream of the mutation — the entire recent window, scratchpad, and user message — must be reprocessed. The system prompt prefix stays cached, but that's a small fraction of a 16-32k prompt.

Epoch-based masking freezes the boundary and advances in batches. Between epochs, the masked section is byte-identical, producing full KV cache hits from the system prompt through the entire masked zone. The cost is slightly larger context between epoch boundaries (a few extra unmasked turns), offset by the context pressure trigger which forces early epoch advances when turns are unusually heavy.

For local inference (llama-server, llama.cpp) where KV cache behaviour is directly observable and controllable, this is a meaningful performance improvement. For API providers with server-side prefix caching (e.g. Anthropic), the epoch approach is unnecessary but harmless — the provider handles cache stability transparently.

### Why is ingestion destructive but assembly non-destructive?

Ingestion rules (truncation, noise stripping) remove content that is never useful at any future point. ANSI codes, duplicate blanks, and progress bars have zero value regardless of when they're viewed. Removing them once saves storage and speeds all future operations.

Assembly masking operates on a view because the full history might be needed later — a "rewind" mechanism could restore masked content if the model needs to revisit old context. The full history also supports the hybrid compaction strategy, which re-masks the conversation before summarizing.
