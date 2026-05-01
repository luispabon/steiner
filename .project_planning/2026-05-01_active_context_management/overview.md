## Request

Implement an active context management system for steiner with two layers: a deterministic ingestion/assembly pipeline that controls what enters the prompt, and a lightweight scratchpad that tracks task state across turns. The system must work for small local models (7B-35B, 8k-64k context) with zero model tokens spent on context management decisions. Expose as two modes: naive (current behavior) and smart (full pipeline). Replace the existing model-based compaction with zero-cost structured compaction.

## Overview

### Architecture

Two-phase deterministic pipeline plus scratchpad:

**Phase 1 — Ingestion** (when tool output arrives, destructive):
- Tool output truncation per tool type (tail-priority for bash/test, count-cap for grep/search, configurable limits)
- Noise stripping (ANSI codes, duplicate blanks, repeated warnings, progress bars)
- Extends existing `boundedCapture` in `internal/tool/output_shaping.go`

**Phase 2 — Prompt Assembly** (when building next model request, non-destructive):
- Observation masking: tool result bodies older than M turns replaced with placeholder; tool call metadata preserved. Research confirms this matches LLM summarization at ~50% cost (arXiv:2508.21433). Default M=5 for steiner (smaller windows than SWE-agent's M=10).
- Assistant prose masking: old assistant messages trimmed to first line or dropped.
- File read annotation: unchanged re-reads get short annotation instead of full content. File metadata tracked in Go (path, turn, byte range, mtime).
- Hooks into existing `Assembler` in `internal/prompt/` via a new assembly phase between conversation loading and message building.

**Scratchpad — Hybrid approach** (research-informed design adjustment):

Research (arXiv:2408.11061, arXiv:2510.03847) shows model-written structured output is unreliable for 7B-32B models: 82% average compliance, format drift over long sessions, field omission, verbosity creep. The SWE-agent and Springdrift patterns (scaffold-injected, model reads but doesn't write) are more reliable.

steiner already has `ContextState` in `internal/agent/context_state.go` — a scaffold-maintained state block with `ActiveConstraints`, `UnresolvedWork`, `ActiveFocus`, `RetainedSummaries`. This is the right foundation.

Design:
1. **Scaffold-maintained state** (reliable, always present): Extend `ContextState` with file tracking metadata, turn count, compaction count, tool call history summary. Injected at fixed prompt position after system instructions. Updated by Go code, never by the model.
2. **Model-written scratchpad** (optional, best-effort): Simple tagged-text block with 3-5 flat string fields (`goal`, `plan`, `step`, `next`, `open`). Model instructed to include in every response. Parsed leniently — extract what's present. On parse failure, carry forward previous scratchpad unchanged. On 3+ consecutive failures, log warning.
3. Both combined form the "where am I" anchor. Scaffold state provides reliability; model scratchpad provides the model's own task understanding.

Target budget: scaffold state ~200 tokens, model scratchpad ~200-400 tokens. ~400-600 tokens total fixed overhead.

**Compaction** (three configurable strategies):

When context exceeds threshold (configurable, default 60% for large windows, lower for small), compaction fires using one of three strategies:

| Strategy | What happens | Cost | Best for |
|---|---|---|---|
| `drop` | Keep scratchpad + recent N turns, drop rest | Zero (no model call) | Local models, small windows |
| `summarize` | Keep scratchpad, feed conversation to model for summary | One full-context model call | Frontier API models where inference is cheap |
| `hybrid` | Masking hollows out old turns first, delayed model summary at second threshold | One model call, but on smaller input | Best empirical results (arXiv:2508.21433: 43% raw cost, +2.6pp solve rate) |

All three strategies share these invariants:
1. Preserve scaffold state + model scratchpad verbatim
2. Preserve most recent N turns verbatim (configurable, default 2-3)
3. Insert discontinuity marker referencing the scratchpad
4. File metadata persists in Go across compaction

```yaml
context_mode: smart
compaction:
  strategy: drop          # or: summarize, hybrid
  threshold: 0.6
  retain_turns: 3
```

The compaction step is behind a `Compactor` interface — one method, three implementations. The existing `compactConversationForBudget` / `buildCompactionRequest` machinery becomes the `summarize` implementation. `drop` is ~20 lines. `hybrid` adds a second threshold check before invoking the summarize path on already-masked input.

**Naive vs Smart mode**:

```
steiner --context-mode naive    # current behavior (default)
steiner --context-mode smart    # full pipeline
```

Both share provider, tool, runner infrastructure. The context manager is an interface that the agent loop calls at two points:
1. After tool execution (ingestion)
2. Before prompt assembly (masking/annotation)

Naive implementation: pass-through (no-op at both points). Smart implementation: full pipeline.

### Key code areas

| Area | What changes |
|---|---|
| `internal/agent/context_state.go` | Extend with file metadata, turn tracking |
| `internal/agent/compaction.go` | `Compactor` interface, three strategy implementations (drop, summarize, hybrid) |
| `internal/agent/runner.go` | Context manager interface, scratchpad lifecycle |
| `internal/agent/turn_progression.go` | Ingestion hook after tool execution |
| `internal/prompt/` | Assembly masking phase, scratchpad injection |
| `internal/tool/output_shaping.go` | Extend truncation strategies, noise stripping |
| `internal/config/config.go` | Context mode config, masking/compaction thresholds |
| `cmd/steiner/` | `--context-mode` CLI flag |

### Staging approach

1. **Foundation**: Context manager interface, naive/smart mode selection, config
2. **Ingestion**: Tool output truncation strategies, noise stripping pipeline
3. **Assembly masking**: Observation masking, assistant prose masking, file read annotation
4. **Scratchpad**: Scaffold state extension, model-written scratchpad parsing, prompt injection
5. **Compaction**: `Compactor` interface, drop/summarize/hybrid strategies, discontinuity markers
6. **Observability**: Logging for masking decisions, token budgets, scratchpad content
7. **Integration**: End-to-end wiring, CLI flag, config validation

### Risks

- **Masking threshold M is scaffold-specific**: M=5 is a starting guess. Wrong M degrades performance "drastically" per research. Needs instrumentation and tuning.
- **Model scratchpad compliance**: Even with lenient parsing, some models may never produce usable scratchpad output. The scaffold-maintained state provides the safety net, but the model loses its self-reported task understanding.
- **File read annotation aggressiveness**: Serving "unchanged" annotations instead of content may cause excessive re-reads. Need to measure re-read rate and provide config to tune.
- **Compaction at small windows**: At 8k, even 600 tokens of fixed overhead is 7.5%. Masking threshold may need to be 1-2 turns. May need a "small window" config profile.
- **Test coverage for masking correctness**: Masking must never separate a tool call from its result. Needs thorough test coverage for edge cases (multi-tool turns, partial masking boundaries).

## Verification Strategy

### Sources
- `Makefile`
- `CLAUDE.md`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
- check:
  - `gofmt -d <changed files>`
- use_check_only_when:
  - reviewer verifying no remaining drift

#### static-analysis
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go vet ./...`
- use_check_only_when:
  - always check-only (no auto-fix)

#### unit-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test ./path/to/pkg -run TestName` (targeted)
  - `go test ./...` (broad)
- use_check_only_when:
  - always check-only

#### build
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go build ./...`
  - `make build-binaries`
- use_check_only_when:
  - always check-only

### Tiers
- cheap:
  - formatting
  - static-analysis
- medium:
  - unit-tests
- expensive:
  - build

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - static-analysis
  - unit-tests
  - build
- reviewer_after_fix:
  - run targeted tests for changed packages
  - run `go vet` on changed packages

### Assumptions
- No CI pipeline exists; all verification is local
- No linter beyond `go vet` and `gofmt`
- Go 1.25

### Uncertainties
- Whether `go test ./...` runtime is acceptable as a broad check (currently assumed medium cost)

## Decision Log

| # | Decision | Rationale |
|---|---|---|
| 1 | Hybrid scratchpad: scaffold-maintained + optional model-written | Research shows 82% structured output compliance for 8B models with format drift over long sessions. Scaffold state provides reliability; model scratchpad adds optional task understanding. Existing `ContextState` is the right foundation. |
| 2 | Observation masking as primary strategy, not LLM summarization | arXiv:2508.21433 confirms masking matches summarization at ~50% cost. Summarization causes 4-15% trajectory elongation. Aligns with "no meta-reasoning tax" principle. |
| 3 | Default M=5 for masking window | Smaller than SWE-agent's M=10 because steiner targets 8k-64k windows vs frontier model windows. Conservative starting point; configurable and instrumentable. |
| 4 | Token estimation: no changes needed | steiner already uses tiktoken-go with cl100k fallback. Research confirms this is the best practical approach for Go. cl100k overcounts for Qwen (conservative, safe). |
| 5 | Three compaction strategies behind `Compactor` interface | `drop` (zero-cost, best for local models), `summarize` (existing behavior, best for cheap API), `hybrid` (masking + delayed summary, best empirical results). User picks via config for experimentation. |
| 6 | Context manager as interface, not mode flag in every function | Clean separation. Naive = pass-through. Smart = full pipeline. Both called at same points in agent loop. |
| 7 | Ingestion is destructive, assembly masking is non-destructive | Ingestion rules permanently reduce stored content (truncation, noise strip). Assembly rules operate on a view (masking, annotation). Full history retained for potential "rewind". |
| 8 | File read annotation defaults to "unchanged" placeholder | Most aggressive option per proposal. SWE-Pruner confirms 67-76% of tokens are file reads. Configurable if re-read rate is too high. |
