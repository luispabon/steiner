# Research — Codebase Complexity Audit

## Question

Where has Steiner accreted avoidable complexity — repetition, overengineering, dead
code, oversized/over-responsible files, and boundary violations — and which of those
findings form the highest value-per-risk **first tranche** of behavior-preserving
cleanup for one implementation loop (moderate aggressiveness: reshaping allowed,
opinionated re-architecture deferred)?

Method: five parallel read-only audit sub-agents, sliced by package group, each
applying the combined methodology of `slop-detector`, `go-code-audit`, and
`improve-codebase-architecture`. 93 findings returned. This document clusters and
ranks them and proposes the tranche/backlog split.

## Findings

### Scale baseline

| Metric | Value |
|---|---|
| Non-test Go LOC (excl. worktrees/vendor) | ~48.8k |
| Test Go LOC | ~68.9k |
| Source files | 501 |
| Largest package | `internal/tui` — 13.2k LOC (27%) |
| Files >400 LOC | 20+; worst: `content_render_chrome.go` (842) |

### Cross-cutting themes (ranked by value-per-risk)

| # | Theme | Findings | Est. impact | Risk |
|---|---|---|---|---|
| T1 | **Cross-package duplicated wrappers** — `cloneProviderMessages`/`cloneProviderTools`/`cloneOptionalInt` copied across agent, output, interactive (+ provider already exports them); `isTTY`≡`supportsANSI`; delegate-arg-text triplicate | C-005, D-004, E-006, A-001 | ~110 LOC, 3+ files deleted, divergence risk killed | low |
| T2 | **`internal/output` event boilerplate** — ~30 constructors repeat the `Event{Type,Timestamp,Payload}` skeleton; ~25 render funcs repeat the `parts/append/Join` idiom; approval & workflow-handoff N-way constructor dup; advisor header dup | D-001, D-002, D-003, D-017 | ~200 LOC, mechanical, table-test-covered | low |
| T3 | **`internal/config` patch/validate fragmentation** — 31 `applyXxxConfigPatch`→`applyXxxPatch` two-level wrappers across 5 files; tiny one-function validate files; one extra apply-chain hop | E-001, E-002, E-021 | ~130 LOC + 3-5 fewer files | low |
| T4 | **Dead code & impossible branches** — huh stub files; `update.Update` alias; `runtimeRegistryWithSink` never-nil error; dropped `spec`/`ctx`/`[]Message` params; unused `Result.Error`; dead `stopReasonSummary` branch; magic `22`; `SupportsUsageStats` nil-guard | E-018, E-005, E-007, E-003, E-004, B-014, D-005, B-006, C-020 | ~80 LOC removed, surface shrunk | low |
| T5 | **`internal/tool/builtin` local dedup** — double path-resolution per read-only tool; `planInsertBefore`≡`planInsertAfter`; doubled bash-denial block; inline pagination vs existing `pageResults`; double file read; schema literals vs named consts; commit two-pass; field-validate re-sort; rune-map → `ContainsAny` | B-001, B-002, B-003, B-005, B-009, B-011, B-012, B-004, B-015 | ~150 LOC, clearer hot paths | low |
| T6 | **`internal/agent` thin-wrapper / test-seam collapse** — `performModelCall`/`emitModelCallStarted`/`buildTurnChatRequest`/`emitAssistantMessage`/`appendAssistantMessage` single-use wrappers; `handleModelCallError`≡`handleError` cancel path; compaction candidate dup; turn-completion emit-pair scattered; compaction function-type test seam | C-006, C-007, C-008, C-018, C-009, C-016, C-019, C-004 | ~70 LOC, less indirection | low–med |
| T7 | **`internal/provider` wire dedup** — `MarshalJSON` Params/ExtraParams merge copied; fragile retry classification via error-string prefixes (no sentinels); hand-walked cache-breakpoint index logic | C-002, C-012, C-014 | ~60 LOC + robustness | low |
| T8 | **`internal/tui` local dedup** — approval-button strip implemented twice; `summarizeArgs` normalize-once; layout `contentWidth`/input-rows dup; single-use forwarding render methods; styled-box dup; tool-event guard dup; nanoNow inconsistency; `renderInputView` flattening | A-003, A-002, A-004, A-010, A-013, A-015, A-009, A-012, A-017, A-005 | ~190 LOC, readability | low–med |
| T9 | **Oversized-file splits (pure mechanical, no logic change)** — chrome (842), model_update_keys (751), model_input (685), mutate (821), compaction (729), session (649), debug (399), event_constructors/render, runtime_build (331), noise_strip (323) | A-006, A-007, A-016, B-007, C-011, D-010, D-007, D-013, E-020, B-013 | brings files under target; navigability | low |
| T10 | **`internal/delegation` file consolidation** — `bootstrap_support.go`/`event_sink.go`/`trace.go` over-split into tiny helper files (the "support" naming the project forbids); `model.go`/`advisor.go` thin accessors | E-013, E-014, E-012, E-017, E-016 | ~5 fewer files, anti-pattern removed | low |

### Deferred (aggressive — out of first tranche, parked as backlog)

| # | Theme | Findings | Why deferred |
|---|---|---|---|
| B1 | Theme `Styles` parallel `ToolTagX/ToolBorderX` fields → maps | A-008 | Coordinated change across theme + 2 content files + theme impls; med risk |
| B2 | `decode.go` reflection engine → json round-trip | B-008 | ~220 LOC rewrite; nested-struct edge cases; needs parity suite; med risk |
| B3 | `ContextDiagnosticsEvent` kitchen-sink → typed sub-events | D-008 | Blast radius across TUI + file_log type assertions; med risk |
| B4 | Remove redundant Turn vs ModelCall event pair | D-009 | Requires reworking TUI state machine; observable ordering; med risk |
| B5 | Overlay-handler registration via interface | A-014 | Touches Model init + critical key path; med risk |
| B6 | `Anthropic` provider strategy-hook refactor (collapse ~120 LOC dup) | C-001 | nil-safety + serialization load-bearing; med risk |
| B7 | `buildActiveRegistry` 15-param → move builder into `internal/delegation` | E-008 (deep half) | Re-homes logic across package boundary; med risk |
| B8 | Cross-package agent-type list drift (`internal/tui` copy of delegation set) | A-011 | Import-cycle risk; may need new shared package |
| B9 | Latent follow-up double-count; `turnProgressor` decoupling | E-011, C-010 | Needs behavioral tracing before touching |

## Implications

- **The wins are real and mostly low-risk.** The first tranche (T1–T7 + the safe parts
  of T8–T10) plausibly removes well over **1,000 LOC** of pure boilerplate/dead code
  without changing behavior, all guarded by an existing ~69k-LOC test suite.
- **`internal/output` and `internal/config` are the two cleanest high-yield targets** —
  large, mechanical, table-test-covered repetition with negligible blast radius.
- **`internal/tui` is the biggest package but yields the most *moderate-risk* work** —
  plenty of safe local dedup and file-splits, but the highest-value structural wins
  (theme maps, overlay registration, diagnostics typing) are the deferred med-risk set.
  This validates the package-agnostic ranking: TUI is not where the cheapest wins are.
- **Several findings are "stop the drift" fixes** (schema literals→consts, isTTY dup,
  agent-type copy, sentinel errors) whose value is preventing future divergence, not LOC.
- The cleanup is naturally **parallel-safe across packages** but should stay serial to
  keep review and `make check` runs sane.

## Risks and Uncertainties

- **Behavior-preservation is the whole game.** Event-render and wire-serialization
  consolidations (T2, T7, D-006) must be byte-for-byte verified by the existing
  golden/round-trip tests; any "tidy" that changes output is out of scope.
- **File splits are low-risk but high-churn** — large diffs that are pure moves. They
  inflate review surface; worth confirming the user wants them inside this loop vs a
  follow-up mechanical pass.
- **A few "moderate" items shade toward behavioral** (C-019 turn-completion ordering,
  C-009 cancel path, D-006 MarshalJSON omitempty semantics, B-009 double read). These
  need careful test reading and may drop to backlog if verification is non-trivial.
- **`make check` is expensive** (build + test + race + lint + vuln). Per-step runs use
  targeted `go test ./internal/<pkg>/...`; the full gate runs once at tranche end.
- Audit was Sonnet-driven and evidence-cited but not exhaustive; line numbers should be
  re-verified at edit time (the executor reads the file before mutating regardless).

## Sources

- Five parallel read-only audit sub-agents (slices A–E), 2026-06-19, applying
  `slop-detector` + `go-code-audit` + `improve-codebase-architecture` methodology.
- `CLAUDE.md` invariants (file-size targets, no util/helper/common packages, package
  boundaries, doc-maintenance rules).
- `Makefile` (`check` = tidy-check fmt-check imports-check build-binaries test test-race
  vet lint vuln); `go.mod` (Go 1.26).
- Direct `wc`/`find` scale measurement of the real source tree.

## Open Questions

1. **Tranche size** — include the mechanical file-splits (T9) in this loop, or defer
   them to a separate low-risk "splitting" pass to keep this loop's diff reviewable?
2. **TUI depth** — include the moderate-risk TUI items (T8: approval-button merge,
   renderInputView) now, or keep this loop to the cheap-win packages and give TUI its
   own loop?
3. Any findings the user wants explicitly promoted from backlog or struck entirely?

## Appendix — full findings by slice

Slice A (tui): A-001…A-018. Slice B (tool): B-001…B-015. Slice C
(agent/prompt/provider): C-001…C-020. Slice D (output/oneshot/interactive):
D-001…D-018. Slice E (cmd/config/delegation/misc): E-001…E-022. Full per-finding
detail (location, suggested change, value, risk, aggressiveness) is preserved in the
audit transcripts; the theme table above is the canonical ranking for planning.
