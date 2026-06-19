# Unslop L4 — Aggressive structural backlog (das-plan hand-off)

> **Feed this file to `/das-plan`.** It is a scoped brief for the **med-risk, opinionated
> re-architecture** work that was deliberately held out of the behavior-preserving loops.
> Derived from a 5-slice audit on 2026-06-19. Nothing here is implemented. This is **Loop
> 4 of 4** in the "unslop" program: L1 = non-TUI semantic dedup, L2 = TUI semantic dedup,
> L3 = file splits, **L4 = this (aggressive backlog)**.
>
> **Run order:** L4 runs **last** — after L1–L3 leave a clean, deduped, well-split base.
> Each item below is independent; the planner may pick a subset. Unlike L1–L3, several of
> these **can change observable behavior** and demand characterization tests first.

## Why these are separated

Each item needs one or more of: coordinated multi-file change, behavioral re-verification,
a new package, or rework of an observable contract (event ordering, wire serialization,
TUI state machine). They are individually med-risk. Treat each as its own planning step
with a **characterization test written first** (capture current behavior, then refactor
under green).

## Constraints

- Behavior must remain equivalent **unless the item explicitly removes a redundant
  behavior** (e.g. B4 removes duplicate events) — in which case all consumers must be
  migrated in the same change and re-verified.
- Honor CLAUDE.md boundaries: `internal/agent` must not bypass provider abstractions;
  `internal/prompt` owns context assembly; no `util`/`helper`/`common` packages; exported
  symbols justified + Godoc; update docs in-commit (README built-in-tools / sub-agent
  tables, docs/CONFIGURATION.md, docs/CONTEXT_MANAGEMENT.md) where a touched surface is
  documented.
- Verify per step: `gofmt`/`goimports`, targeted `go test ./internal/<pkg>/...`,
  `go build ./...`. Loop end: full `make check` (incl. `test-race`, `vuln`).

## Backlog items (full detail)

**B1 (A-008) — Theme `Styles` parallel fields → maps** · `internal/tui/theme/theme.go:60-96`
+ `content_tool.go` · risk med
28 parallel fields (`ToolTagX`/`ToolBorderX` ×8, `DelegateTagX`/`DelegateBorderX` ×6) plus
two parallel `switch` statements (`toolTagStyle`, `toolBorderStyle`). Adding a tool kind
requires 4 lockstep edits; already drifted (`ToolTagGrep` vs `ToolTagSearch`/`ToolTagGlob`).
**Change:** replace with `ToolTags map[string]lipgloss.Style` + `ToolBorders
map[string]lipgloss.Style` (and delegate equivalents) keyed by normalized tool name;
dispatch becomes a map lookup with Default fallback; registration in one place
(`buildStylesInternal`). Coordinated across `theme.go` + `content_tool.go` + the steiner
theme impl. Tests: `theme/style_test.go`, `theme/steiner_test.go`. Value L.

**B2 (B-008) — `decode.go` reflection decoder → json round-trip** ·
`internal/tool/builtin/decode.go` (283) · risk med
~250 LOC custom reflection JSON decoder (`decodeReflect`, `setField`, `setPointerField`,
`setStructField`, `setSliceField`, `setScalarField`) exists to coerce LLM-sent `float64`
into string/int (~2 real cases). **Change:** replace with `encoding/json`
marshal→unmarshal round-trip after a pre-pass that coerces `float64` where the target
struct expects `int`/`string`. **Risk:** nested struct/slice fields (`MutateOperation`'s
`[]MutateOperation`). Build a **parity test** that runs all `decode_test.go` (272-line)
cases against both old and new before deleting the old. Value L (~220 LOC removed).

**B3 (D-008) — `ContextDiagnosticsEvent` kitchen-sink → typed sub-events** ·
`internal/output/debug.go:9-51` · risk med
One 45-field struct serves 5 distinct diagnostic kinds (`budget`, `file_annotation`,
`compaction`, `session_health`, `session_loaded`) dispatched on a `Kind` string; most
fields zero for any given kind. **Change:** introduce concrete sub-events
(`ContextBudgetEvent`, `ContextCompactionEvent`, `ContextSessionHealthEvent`,
`ContextFileAnnotationEvent`) carrying only their fields; register each in
`eventRenderers`; decompose `formatContextDiagnosticsEvent` into per-type render funcs.
**Blast radius:** TUI and `file_log.go` consume `ContextDiagnosticsEvent` by type
assertion — migrate all sites. Tests: `debug_test.go`, `stream_test.go`. Update
docs/CONTEXT_MANAGEMENT.md if the diagnostics surface is documented. Value L.

**B4 (D-009) — Remove redundant Turn vs ModelCall events** ·
`internal/output/event_{constructors,types}.go` + `internal/tui/model_events.go` · risk med
**(behavioral)**
`TurnStarted`/`TurnFinished` largely duplicate `ModelCallStarted`/`ModelCallFinished`; TUI
treats Turn events as no-ops except for state-machine transitions (`model_events.go:46,194,
226,240`) and delegation grouping. **Change:** verify Turn events carry no unique signal,
then remove the pair + their constructors + render funcs, and rewire the TUI state machine
to transition on ModelCall events. **Event ordering/identity is observable** — characterize
first. Tests: `event_scope_test.go`, `model_test.go`. Value M.

**B5 (A-014) — Overlay-handler interface registration** ·
`internal/tui/model_update_keys.go:47-88` + `model_init.go` · risk med
12-case overlay switch; each new overlay needs 2 edits (Model field + case). **Change:**
define `overlayHandler` interface `{ IsOpen() bool; HandleKey(tea.KeyMsg) (tea.Model,
tea.Cmd) }`, register overlays in a priority-ordered slice in `model_init.go`;
`handleOverlayKeyMsg` becomes a loop. **Caveat:** some overlays return `tea.Model`, others
mutate `m` directly — the interface must accommodate both, which is the hard part. Touches
the critical key-dispatch path. Tests: `model_test.go`. Value M.

**B6 (C-001) — Anthropic provider strategy-hook refactor** ·
`internal/provider/anthropic.go:34-160` vs `openai_compat.go:127-264` · risk med
`Anthropic.ChatCompletion`/`StreamChatCompletion` duplicate ~120 lines of nil-guard /
`acquire`-`defer release` / `withRetry` / HTTP boilerplate from the embedded
`*OpenAICompat`; only `buildRequestPayload` (serialization) and
`normalizeAnthropicChatResponse` differ. **Change:** lift serialization + response-
normalization into injectable hooks on `OpenAICompat` (unexported func fields set at
construction); `Anthropic` overrides only those two and delegates the rest. **Risk:**
nil-safety (`p == nil || p.OpenAICompat == nil` vs `p == nil`) and wire correctness are
load-bearing. Tests: `anthropic_test.go`, `openai_compat_test.go`. Value L (~120 LOC).

**B7 (E-008 deep half) — Move `buildActiveRegistry` into `internal/delegation`** ·
`cmd/steiner/runner.go:168` · risk med
`buildActiveRegistry` takes 15 params and assembles sub-agent + advisor registries — too
much business logic in the composition root. **Change (this loop):** extract
`BuildDelegateRegistry(deps DelegateDeps) (*tool.Registry, error)` into
`internal/delegation`, leaving `cmd/steiner` as thin wiring. *(The lighter
`RegistryBuilderDeps` struct-bundling is the moderate alternative and may already be done
in L1; this item is the full re-home.)* Tests: `delegation/integration_test.go`,
`cmd/steiner/main_test.go`. Update docs/SUBAGENT_DELEGATION.md if tool allowlists move.
Value M.

**B8 (A-011) — Fix TUI copy of the delegation agent-type set** ·
`internal/tui/content_tool.go:14-25` · risk med
`specializedDelegateTools` is an out-of-band copy of data owned by `internal/delegation`
(comment admits it). **Change:** either (a) export `delegation.SpecializedAgentNames()
[]string` and import it from `internal/tui`, or (b) move the canonical set to a new shared
sub-package (e.g. `internal/agenttype`) that neither imports the other. **Risk:** option
(a) may create an import cycle — verify; option (b) adds a package (allowed: it's a domain
package, not `util`/`common`). Value M (kills silent drift).

**B9 (C-010 + E-011) — Decouple `turnProgressor`; fix follow-up double-count** ·
`internal/agent/turn_progression.go:257-263` + `internal/delegation/session.go:51-64` ·
risk med **(behavioral)**
(C-010) `turnProgressor` holds only `*Runner`; most methods never touch it. **Change:**
pass a compaction-fn parameter to `handleCompaction`, fully decoupling the struct and
enabling isolated unit tests; rewire `Runner.Run`. (E-011) `SessionStore.Update` does
`FollowUpCount++` while `follow_up.go` independently computes `FollowUpCount + 1` → latent
double-count. **Change:** make the counter authoritative in one place — **trace the
follow_up → store → result cycle first** to determine which path is canonical; this can
change observed counts, so characterize first. Tests: `runner_test.go`, delegation
follow-up tests. Value M.

### Minor / debatable (include only if convenient)

- **C-003** — `internal/provider` wire role-dispatch symmetry (`toOpenAIMessage` vs
  `toAnthropicMessage` scaffolding + image-block sub-loop). Extract a shared image-block
  converter parameterized by a block-builder, or just document the symmetry. Low value;
  expected given different wire formats.
- **E-015** — `internal/delegation` `limits.go` (50) / `result.go` (76) could merge into
  `task.go` (sole consumer). Debatable whether the current split is wrong; skip unless
  doing other delegation work.

## Recommended step decomposition for the planner

One step per backlog item, each gated on a characterization test. Suggested priority
(value-per-risk, safest first): **B1 → B6 → B2 → B8 → B7 → B3 → B5 → B4 → B9**. B4 and B9
are behavioral and should go last with the most scrutiny. The planner may legitimately
ship only a subset and re-defer the rest.

## Provenance

Audit slices A–E (read-only sub-agents, 2026-06-19) applying slop-detector + go-code-audit
+ improve-codebase-architecture methodology. Line numbers evidence-cited; re-verify at
edit time. Companion: `.project_planning/2026-06-19_complexity-reduction/research.md`.
