## Request

Remove "smart" context management from steiner per GitHub issue #108:

> After lots of experimentation I didn't find it to be very good, models would often get confused about disappearing tool calls and the toll on cache hits was immense.
>
> We must remove the code, relevant config and documentation.

The work should remove the experimental smart context-management path, its configuration and CLI surface, and docs that describe or recommend it.

## Overview

The repo currently exposes two context-management modes:

- `naive`: the baseline and default path. It preserves full conversation history, read-result annotation, cached system preamble, and budget-triggered model compaction behavior.
- `smart`: the experimental path. It layers epoch-based masking, scaffold/hybrid scratchpad behavior, smart compaction strategies (`drop`, `summarize`, `hybrid`), context masking/reset logic, additional diagnostics, and mode-specific config/CLI documentation.

The implementation should remove `smart` as a selectable mode and simplify the context-management surface around the baseline behavior. The likely code areas are:

- `internal/config`: remove `ContextModeSmart`, `CompactionStrategy`, `ScratchpadMode`, and smart-only fields from `ContextManagementConfig`; update defaults, validation, patching, and tests.
- `cmd/steiner`: remove `--context-mode`, CLI/config override plumbing, and context-mode runtime wiring. Runtime should construct the single remaining baseline context state directly, not via a mode factory.
- `internal/agent`: remove the pluggable context-manager abstraction along with `SmartContextManager`. This includes `ContextManager`, `NewContextManager`, `NaiveContextManager` as a mode implementation, and role interfaces that exist only to type-assert optional behavior (`CompactionRecorder`, `EpochResetter`, `AssistantResponseIngestor`, `ScaffoldInferrer`, `CompactionStrategyProvider`, `MaskingWindowProvider`). Keep the actual baseline state/behavior, likely as a concrete unexported runner helper: post-ingestion read normalization, fresh tool-result shaping, mutation tracking for read invalidation, file-annotation diagnostics, and cached system preamble.
- `internal/agent/compaction.go`: keep the model-based summarize compactor as the baseline hard-limit fallback if still needed by naive mode, but remove smart-only `drop` and `hybrid` compactors, compaction strategy selection, epoch reset, compaction recorder hooks, and scratchpad-oriented discontinuity wording.
- `internal/prompt`: remove prompt scratchpad/scaffold machinery and durable context fields that become unused after smart removal. Preserve prompt assembly, bounded source budgets, tool-result shaping, conversation-summary support needed by baseline summarize compaction, and model-budget fit checks.
- `internal/output`: remove smart-only scratchpad, masking, epoch, and session-health diagnostics when no longer emitted; preserve budget/file-annotation/compaction diagnostics that the remaining runner still uses.
- `internal/tool/builtin`: remove the `scratchpad` built-in tool unless a deliberate non-smart delegation feature keeps it. Current code registers the built-in in `builtin.Builtins`, but `cmd/steiner/tools.go` filters it out unless `context_management.scratchpad_mode == hybrid`; after that config goes away, the tool and tests are likely dead.
- `internal/delegation`: remove `scratchpad` from default sub-agent allowed tools, agent-type allowlists, bootstrap prompts, and docs unless the implementation deliberately keeps a separate delegation scratchpad capability. Today delegation prompts instruct scratchpad use, but the parent runtime usually filters the tool out in the default `scaffold_only` config, so this looks like cleanup rather than a behavior loss.
- `testdata/stage3` and package tests: delete or rewrite tests and fixtures that assert smart masking, scratchpad, and smart compaction behavior; retain tests for baseline read annotations, prompt assembly, model calls, tool execution, and hard-limit behavior.
- `docs/CONTEXT_MANAGEMENT.md`: delete or rewrite because it primarily documents the smart pipeline and its rationale.
- Other docs: update references in `README.md`, `docs/DELEGATION.md`, `docs/PRD.md`, `docs/ROADMAP.md`, and `docs/adr/0001-defer-context-manager-interface-consolidation.md`.

Important scope boundary: issue #108 targets "smart" context management, not every bounded-context mechanism in steiner. File read annotation is documented as shared by naive and smart modes, and the baseline path still uses cached preamble and model-budget checks. Those should remain unless implementation proves they are inseparable from smart-only code.

Expected end state:

- There is no user-facing `smart` context mode.
- There is no internal context-mode plugin/factory abstraction kept solely for a single remaining implementation.
- Config files no longer accept or advertise smart-only context settings.
- The code no longer contains smart-only masking, epoch, scaffold scratchpad, drop/hybrid compaction, or related diagnostics.
- The model-facing `scratchpad` tool and scratchpad prompt instructions are gone unless explicitly retained as a separate delegation feature.
- Remaining context behavior is simpler and predictable: baseline conversation assembly plus bounded tool output/read annotation and existing provider/model budget checks.
- Docs and tests reflect the simplified behavior.

## Verification Strategy

Repo instructions require `make check` before finalizing Go changes.

Use targeted checks during implementation:

- Cheap: `gofmt -w <changed go files>` and `goimports -w <changed go files>` after Go edits.
- Cheap/medium: targeted package tests around changed areas, likely `go test ./internal/config`, `go test ./cmd/steiner`, `go test ./internal/agent`, `go test ./internal/prompt`, and `go test ./internal/output`.
- Medium: `go test ./...` after major deletion passes to catch cross-package fallout.
- Required final check: `make check`.

`make check` currently expands to:

- `go mod tidy` plus `git diff --exit-code go.mod go.sum`
- `gofmt` check
- `goimports` check
- `go build` for `./cmd/steiner`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `golangci-lint run ./...`
- `govulncheck ./...`

CI mirrors these checks in `.github/workflows/checks.yml`, with `test-race` after lint/vuln.

## Decision Log

- 2026-06-02: Planning branch is `cl/2026-06-02_remove-smart-context-management`.
- 2026-06-02: External research skipped. The issue is repo-local, and `docs/CONTEXT_MANAGEMENT.md` plus nearby code and tests explain the smart pipeline sufficiently for planning.
- 2026-06-02: Preserve baseline shared behavior unless implementation proves it is smart-only: read annotations, cached preamble, bounded prompt assembly, tool output shaping, and model-budget fit checks.
- 2026-06-02: Treat smart-only scratchpad, epoch masking, drop/hybrid compaction, and context-mode config/CLI as removal targets.
- 2026-06-02: Second pass found scratchpad references outside `SmartContextManager` in built-in tools, sub-agent defaults/allowlists, delegation prompts, system preamble, TUI event state, and tests. Because the registry only exposes `scratchpad` when `scratchpad_mode == hybrid`, removing smart/hybrid likely makes the model-facing scratchpad tool dead. Plan should remove those references unless the executor intentionally preserves scratchpad as a separate delegation feature.
- 2026-06-02: Keep summarize compaction as the likely baseline hard-limit fallback, but strip its smart-only strategy plumbing and scratchpad/epoch/recorder hooks.
- 2026-06-02: Third pass found the context-mode pluggability layer itself should be removed. The current `ContextManager` interface has lifecycle hooks for two modes (`PostIngestion`, `PreAssembly`, `OnTurnComplete`) plus many optional role interfaces reached by type assertion. With only baseline behavior remaining, preserving this as a one-implementation plugin layer would keep the failed experiment's complexity. Collapse it into concrete runner/context state and direct helper calls.
