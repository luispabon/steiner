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

1. **PostIngestion** — after a tool result arrives, before it enters conversation history
2. **PreAssembly** — before building the next model request, to filter the conversation view
3. **OnTurnComplete** — after each model response, to track whether the scratchpad tool was called

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
    mask old tool results (rolling window, turn-based)
    mask old assistant prose (trim to first line)
    annotate unchanged file re-reads
          │
          ▼
    assemble prompt:
      system instructions (stable zone — cached per session)
      project context (skills, repo instructions)
      older turns (masked)
      synthetic scratchpad user message (scaffold state + model scratchpad from previous turn)
      recent turns (verbatim)
          │
          ▼
    prompt sent to model
          │
          ▼
    model responds (action + scratchpad tool call)
          │
          ▼
    scratchpad tool result processed, decisions appended, stored for next turn
          │
          ▼
    OnTurnComplete fires (tracks whether scratchpad tool was called)
```

## Ingestion Rules

These apply once, when a tool result is received. They run in Go with no model calls.

### Tool output truncation

Every tool result is subject to a configurable maximum size. When output exceeds the limit, it is truncated with a marker so the model knows content was removed and can re-run with different parameters.

Different tool types use different truncation strategies:

| Tool type | Strategy | Rationale |
|-----------|----------|-----------|
| bash, test, build | Tail-priority | Errors and failures appear at the end |
| grep, search, glob | Count cap | Limit number of results, not bytes |
| read | No truncation | File size managed at assembly time |

Truncation marker example:
```
[output truncated: 4521 of 12830 bytes shown — re-run with narrower parameters if needed]
```

### Noise stripping

Applied after truncation. Removes content that is never useful to the model:

- ANSI escape codes and terminal color sequences
- Duplicate consecutive blank lines (collapsed to single)
- Repeated identical warning/info lines (replaced with count: `[previous line repeated 47 times]`)
- Progress bars, spinners, download progress output

## Prompt Assembly Rules

These apply every turn when building the prompt. They do not modify stored history.

### Observation masking

Tool results and assistant prose older than M turns have their body replaced with a placeholder that includes the absolute turn index. The tool call metadata is preserved so the model knows what happened.

```
Turn 3: [tool_call] read internal/agent/runner.go
        [result] [tool result from turn 3 masked: read path=internal/agent/runner.go - re-read if needed]

Turn 3: [assistant]
        [turn 3] package agent...   ← first line only, with turn prefix

Turn 10: [tool_call] read internal/agent/runner.go
         [result] package agent...  (full content, recent turn)
```

M is configurable. Default is 5 (conservative starting point for steiner's target window sizes, smaller than the M=10 that worked for SWE-agent on frontier models).

Invariants:
- Tool calls and their results are atomic. If a tool call is present, either its full result or its masked placeholder is also present. They are never separated.
- Only the tool result body is replaced. The tool name and a summary of arguments are preserved in the placeholder so the model retains orientation.
- Assistant prose older than M turns is trimmed to its first line or dropped entirely.
- Masking operates on a copy. The stored conversation history is never modified.

### File read annotation

File reads are the dominant source of context consumption (67-76% of total tokens in coding agent benchmarks). steiner tracks file metadata in Go:

- File path
- Turn number when last read
- Byte/line range (offset + limit)
- Modification timestamp at time of read

When the model requests a file that was recently read and is unmodified since (checked via filesystem stat), steiner replaces the tool result with a short annotation:

```
[file unchanged since turn 5, 247 lines — re-read with force=true if needed]
```

This is the most aggressive option. The model can always re-read if it needs the content.

Invalidation:
- External modifications (user editing outside steiner) change the file's mtime, which invalidates the tracking. The next read serves full content.
- File writes and edits by steiner's own tools invalidate tracking for that path.
- File tracking state persists across compaction (it lives in Go, not in conversation history).

### Prompt zone stability

steiner splits every prompt into two zones:

- **Stable zone** (system prompt only): role, tool rules, project context, scratchpad instructions. Built once per session from session-constant inputs (`override` and `scratchpadEnabled` from config) and cached on `SmartContextManager`. The same byte string is used on every turn, enabling KV cache hits on the system prompt prefix across all providers that support prefix caching.

- **Volatile zone** (messages array): older masked turns, recent turns verbatim, synthetic scratchpad user message, actual user message.

A per-turn debug log (`slog.Debug("prompt zones", "turn", N, "system_bytes", X, "conversation_bytes", Z)`) records the byte sizes of each zone. Enable with `--log-level debug`. See `docs/providers.md` for provider-specific KV cache behaviour.

## The Scratchpad

The scratchpad is a small block of text (~400-600 tokens) injected as a user-role message near the end of each prompt, just before the conversation history's recent turns. It provides the model's "where am I" anchor, especially important after observation masking has removed old context or compaction has dropped conversation history.

### Two parts

**Scaffold state (~200 tokens)** — maintained by Go code, always accurate:
- Files the model has read (path, turn, modification status)
- Current turn number and compaction count
- Active constraints and unresolved work items
- Recent tool call summary (what was run, which turn)

The model reads this for orientation but cannot modify it. steiner updates it automatically based on what happened during the session.

**Model scratchpad (~200-400 tokens)** — written by the model via a tool call, best-effort:

The system prompt instructs the model to call the `scratchpad` tool on every turn without exception. The tool takes seven fields:

| Field | Owner | Description |
|-------|-------|-------------|
| `goal` | model | Current overall objective |
| `plan` | model | High-level approach |
| `step` | model | What is being done this turn |
| `decisions` | steiner-managed | Key decisions made so far; model writes only this turn's new decisions; steiner appends to history (2000-byte cap, oldest-first eviction). Never overwritten by the model. |
| `files` | model | Files the model considers relevant to the current task (intent signal, distinct from FileTracker's ground-truth tracking) |
| `open` | model | Unresolved questions or risks |
| `next` | model | Planned next action |

steiner processes the tool result via `IngestToolResult`, stores state separately from conversation history, and injects the latest version as a synthetic user-role message on the next turn.

### Failure handling

Failure is defined as: the model did not call the `scratchpad` tool this turn. `OnTurnComplete(turnIndex int, scratchpadCalled bool)` is invoked after each model response. If `scratchpadCalled == false`, `SmartContextManager` increments a consecutive-miss counter; the counter resets to 0 on any successful call.

1. Miss: carry forward the previous scratchpad state unchanged and log a warning
2. After 3+ consecutive misses: emit a model compatibility warning to the user

The system never crashes or loses state because of a missing scratchpad call. The scaffold state provides the factual safety net regardless of model cooperation.

### Staleness detection

With the tool-call approach the model is required to call `scratchpad` each turn, making copy-forward less of a concern than with inline XML. If the same field values appear across consecutive turns, steiner logs it as a signal but does not treat it as a hard failure.

## Compaction

Compaction is the fallback when masking alone is insufficient to keep context within limits.

### Trigger

Compaction fires when context usage exceeds a configurable threshold. Default is 60% for large windows. For small windows (8k-32k), the threshold may need to be lower.

```yaml
compaction:
  threshold: 0.6
  retain_turns: 3
  strategy: drop
```

### Strategies

Three compaction strategies are available, all behind a common interface:

#### drop (default for local models)

Zero-cost compaction. No model call.

1. Keep the last N turns verbatim (configurable `retain_turns`, default 3)
2. Drop all older turns
3. Insert a discontinuity marker: `[context compacted — see scratchpad for task state, re-read files if needed]`

The scratchpad (both scaffold state and model-written) survives verbatim — it is injected at assembly time from Go state, not stored in conversation history. File tracking metadata also survives.

#### summarize

Model-based compaction. The existing behavior from naive mode, enhanced with scratchpad awareness.

1. Feed the conversation to the model with a compaction system prompt
2. Model produces a summary that replaces the old conversation
3. Scratchpad is preserved alongside the summary

This costs one full-context model call. For local models, this is the most expensive possible operation. For frontier API models where inference is cheap relative to context cost, it may produce better continuity than `drop`.

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
- File metadata tracking persists across compaction (lives in Go, not conversation).
- Scaffold state and model scratchpad survive all strategies.
- Escalation system tracks compaction count: info (1st), warning (2nd), critical (3rd+). At critical, steiner advises restarting in a fresh session.

## Interaction Between Components

The components are complementary and layer progressively:

1. **Ingestion** prevents the biggest offenders from entering history at full size. A 50k grep result is capped to 200 results at ingestion. This is the cheapest, most impactful reduction.

2. **Observation masking** progressively hollows out turns as they age. By the time a turn is 5+ turns old, its tool results are one-line placeholders. Context grows slowly.

3. **File annotation** prevents the single largest token source (file reads, 67-76% of total) from accumulating. Unchanged re-reads cost ~20 tokens instead of hundreds or thousands.

4. **The scratchpad** ensures task continuity regardless of what has been masked or dropped. The model always has its "where am I" anchor.

5. **Compaction** is the hard reset when everything else is insufficient. By the time it fires, masking has already reduced most old turns to lightweight skeletons. Compaction drops these skeletons, preserves the scratchpad, and the model continues with a clean slate plus full orientation.

## Configuration

All context management settings live under the model configuration:

```yaml
models:
  default:
    model: qwen3-35b-a3b
    context_size: 32768

    context_management:
      mode: smart                    # naive or smart
      masking_window: 5              # M: turns before masking
      file_annotation: true          # annotate unchanged re-reads
      scratchpad: true               # enable model-written scratchpad

    compaction:
      strategy: drop                 # drop, summarize, or hybrid
      threshold: 0.6                 # context fill ratio to trigger
      retain_turns: 3                # turns to keep after compaction
      safety_margin_tokens: 2048     # headroom before trigger
```

The CLI flag `--context-mode naive|smart` overrides the config file setting.

## Observability

Smart mode emits structured events through the existing EventSink:

- **ContextMaskingEvent**: which turn and tool call was masked, why
- **FileAnnotationEvent**: file path, whether annotation or full content was served, why
- **CompactionEvent**: strategy used, token count before/after, turns dropped
- **ScratchpadEvent**: scratchpad content after each model response; whether the scratchpad tool was called this turn (fired via `OnTurnComplete`)
- **TokenBudgetEvent**: per-category token counts each turn (system, scratchpad, project context, conversation)

Debug mode (`--log-level debug`) logs the full assembled prompt with masking decisions annotated, plus the full unmasked conversation history.

## Design Rationale

### Why not LLM summarization as the primary strategy?

Empirical research (arXiv:2508.21433) tested observation masking against LLM-based summarization on SWE-bench across five model configurations. In four of five settings, masking matched or beat summarization while being up to 52% cheaper. Summarization also causes 4-15% trajectory elongation — the model re-explores because summaries introduce ambiguity.

For small local models, summarization has additional problems: it fills the entire context window for the summary call (the most expensive operation possible), and small models produce lower-quality summaries than frontier models.

### Why a hybrid scratchpad instead of pure model-written?

Research on structured output from small models (arXiv:2408.11061, arXiv:2510.03847) shows 82% average compliance for 8B models with high variance, format drift over long sessions, and field omission under token pressure. The SWE-agent and Springdrift patterns (scaffold-injected state that the model reads but doesn't write) are more reliable.

The hybrid approach uses scaffold-maintained state for the facts (always correct) and a model-called scratchpad tool for intent (best-effort). Tool calls are more reliably structured than inline XML in assistant text, but models can still fail to call the tool. The `decisions` field mitigates drift: steiner appends to it rather than letting the model overwrite, so key decisions accumulate reliably regardless of what the model writes in a given turn. The model's scratchpad failing gracefully is better than depending on it.

### Why is ingestion destructive but assembly non-destructive?

Ingestion rules (truncation, noise stripping) remove content that is never useful at any future point. ANSI codes, duplicate blanks, and progress bars have zero value regardless of when they're viewed. Removing them once saves storage and speeds all future operations.

Assembly masking operates on a view because the full history might be needed later — a "rewind" mechanism could restore masked content if the model needs to revisit old context. The full history also supports the hybrid compaction strategy, which re-masks the conversation before summarizing.
