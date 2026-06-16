# Overview — Advisor pattern (issue #192)

## Request

Issue #192 ("Advisor pattern"): add advisor agents for planning and steering, where a
**smarter / more capable model advises the cheaper model driving the main agent loop**.
References: Anthropic advisor tool docs, pi.dev/packages/pi-advisor.
Deliverable: research-grounded design + full implementation plan.

## Overview

Add a client-side **advisor** capability to steiner: a model-invoked tool the main
(executor) agent calls when it wants planning/steering guidance from a stronger model.
The advisor is a **pure reasoning pass** — it receives the current conversation, returns
free-form guidance as a tool result, and **calls no tools, mutates nothing, and produces
no user-facing output**. This is distinct from the existing `delegate`/`plan` sub-agents,
which run a child agent loop with their own tool access.

Confirmed by research: both Anthropic's advisor tool (server-side, beta, Anthropic-only)
and pi-advisor (client-side, third-party) implement exactly this "stronger advises cheaper"
pattern, with published benchmark gains. Because steiner is provider-agnostic (Ollama
default), we implement the **client-side** variant: a new `internal/advisor` package, a
new `advisor` built-in tool wired in `cmd/steiner` with access to the live conversation
and provider, a new `advisor` config block, and a system-prompt steering preamble in
`internal/prompt` that tells the executor when to consult.

Mechanism in steiner terms:
1. Executor decides to call `advisor` (empty/minimal input — timing signal only).
2. The tool handler snapshots the current conversation, builds an advisor request
   (advisor system prompt + transcript), and calls the configured advisor model via the
   existing provider/scheduler abstraction.
3. The advisor's free-form guidance is returned as the tool result and injected back into
   the executor's conversation, bounded/summarised per steiner's tool-output rules.
4. A per-run usage cap prevents runaway advisor calls.

## Key Decisions

- **Client-side implementation.** Anthropic's server-side advisor tool is beta and
  Anthropic-only; steiner must support any provider (Ollama default), so we orchestrate
  the advisor call ourselves. Rationale: provider-agnostic invariant.
- **Model-invoked tool, not a loop hook (v1).** Matches steiner's explicit-tool-use
  philosophy and reuses the tool registry. Auto-trigger hooks are deferred.
- **Pure reasoning pass, separate from `delegate`/`plan`.** Advisor gets no tool registry,
  runs no child loop, cannot mutate. New `internal/advisor` package rather than extending
  `internal/delegation`, because delegation's contract (pass-only context, child loop,
  tools) is the wrong shape — the advisor needs the *live parent conversation* and has *no*
  tools.
- **Advisor sees the live parent conversation exactly as the executor currently holds it
  (post-compaction), with no extra cap or trimming.** Decisive reason (see research_002.md):
  steiner delegates aggressively and a delegate's internal tool calls/transcript are NOT
  copied to the parent — only the task + a bounded result summary persist
  (`docs/SUBAGENT_DELEGATION.md`). Compaction summarises old turns on top. So the parent
  transcript is *already* a bounded, summarised-at-the-boundaries context: we get
  curated-context benefits for free, with no new selector, no second budgeting path, and no
  context-rot blowup. **Rejected: a dedicated per-consult summarisation pass** — the
  transcript is already summarised, so re-summarising compounds fidelity loss (critic/
  self-correction literature: critics need raw signal, not a digest) and adds latency.
  Explicit pi-advisor-style curation/stage detection is deferred until cost demands it.
- **Disabled by default** (`advisor.enabled: false`). Opt-in feature.
- **`max_uses_per_run` enforced in the handler, never by mutating the tool definition.**
  steiner never adds/removes/alters tool definitions mid-conversation (prompt-cache
  integrity). The advisor tool stays statically registered for the whole run; a per-run
  counter in the handler short-circuits at the cap and returns a plain tool result
  ("advisor budget exhausted; proceed on your own judgment") instead of calling the advisor
  model. This deliberately diverges from Anthropic's docs (which tell clients to remove the
  tool at the cap) — a code comment must cite the cache reason.
- **Availability = statically registered whenever enabled; "restriction" to the coding loop
  is realized via guidance placement, not tool gating.** Hard per-command gating would
  require mid-conversation registry swaps (cache bust) — rejected. The tool is always
  callable when enabled; strong situated nudges live only in the plan/implement/review
  skills.
- **Two-layer steering.** Layer A: a concise base preamble in `internal/prompt` injected
  only when advisor enabled (high-level steering only; cannot see inside delegations; when
  to call; give advice weight but adapt; budget is limited) — adapted from Anthropic's
  published coding-task prompt minus its remove-at-cap guidance. Layer B: situated nudges in
  the plan/implement/review SKILL.md files.
- **Advisor's role is high-level steering/planning, not line-level review.** By construction
  it cannot see inside delegations, which matches issue #192 ("planning and steering") and
  keeps it out of `verify`/`code`'s lane.
- **Free-form text guidance in v1.** Matches Anthropic; structured verdict + action items
  (pi-advisor style) deferred.
- **Explicit `advisor_model` config, distinct from executor model**, resolved against the
  existing `models` config. Plus `enabled`, `max_uses_per_run` (cost cap), `max_tokens`.
- **System-prompt steering is the trigger lever.** Add an advisor preamble in
  `internal/prompt` (like the delegation preamble) telling the executor when to consult
  (e.g. before substantive work, after test failures). Research shows executors under-call
  without this.
- **Approval policy: auto** (like other sub-agent/read-only tools) — advisor is read-only
  and produces no side effects.

## Tradeoffs

- **Full transcript vs curated vs summarised context.** Resolved by steiner's architecture:
  delegation + compaction already curate/summarise the parent transcript, so passing it live
  gives bounded context for free. Dedicated summarisation rejected (compounds fidelity loss +
  latency); explicit stage-aware curation deferred. See research_002.md.
- **Free-form vs structured output.** Free-form = simplest, matches Anthropic. Structured
  verdict = more actionable, machine-checkable, but adds schema/parsing. Chose free-form
  v1. *(Open for confirmation.)*
- **Reuse `internal/delegation` vs new `internal/advisor`.** Reuse would save scaffolding
  but forces the advisor through a child-loop/pass-only-context contract that fights the
  pattern (advisor needs live conversation, no tools). Chose a focused new package.
- **Model-invoked tool vs automatic loop hook.** Hook guarantees consultation but is
  invasive to the loop and risks over-calling. Tool defers timing to the executor (steered
  by prompt). Chose tool for v1; hook deferred.
- **Manual slash command (`/advisor ask`) like pi-advisor.** Useful but UI surface; deferred
  to keep v1 scope tight.

## Scope Boundaries

**In scope:**
- `internal/advisor` package: build advisor request from conversation + run advisor model
  call via provider/scheduler, return bounded guidance.
- New `advisor` built-in tool + registration/wiring in `cmd/steiner`.
- New `advisor` config block in `internal/config` (enabled [default false], model,
  max_uses_per_run, max_tokens) with defaults, merging, validation.
- Per-run usage cap enforced in the handler (counter short-circuit), never by mutating the
  tool definition.
- Layer A advisor steering preamble in `internal/prompt` (injected only when advisor enabled).
- Layer B situated advisor nudges in plan/implement/review SKILL.md.
- Output/event surfacing in `internal/output` + `internal/tui` (visible "consulting
  advisor" status; bounded/collapsible result).
- Tests (table-driven, next to source) for config, advisor request build, cap enforcement
  (incl. exhausted-budget result), tool execution, prompt preamble.
- Docs: README built-in tools + new feature section; CONFIGURATION.md advisor block;
  per CLAUDE.md maintenance rules. SUBAGENT_DELEGATION.md cross-ref since advisor sits
  alongside delegation tools (and explicitly differs: no tools, no child loop).

**Out of scope:**
- Automatic loop-hook / turn-N auto-trigger of the advisor.
- Curated / stage-aware context selection (initial/recovery/final-check).
- Structured verdict + action-item output schema.
- Manual `/advisor ask` slash command / TUI command.
- Relying on Anthropic's server-side advisor tool.
- Persistent advisor session / cross-call advisor memory beyond shared transcript.
- Changes to existing `delegate`/`plan`/other sub-agent tools.

## Verification Strategy

Discovered from `Makefile` + `CLAUDE.md` (Go 1.25):

- **Formatter (cheap):** `gofmt -w <files>`, `goimports -w <files>`. Prefer fix mode.
- **Targeted test (cheap/medium):** `go test ./internal/<pkg> -run TestName`.
- **Build (medium):** `go build ./...`.
- **Vet (cheap):** `go vet ./...`.
- **Full suite (expensive):** `make check` = `tidy-check fmt-check imports-check
  build-binaries test test-race vet lint vuln`. Run before finalizing.
- Per CLAUDE.md: run targeted tests during steps; run `make check` before finishing.
  If a check can't run, report exact command + reason.

## Decision Log

- 2026-06-16: Deliverable = design + full implementation plan (user).
- 2026-06-16: Issue's "cheaper models" was a typo for "smarter models"; corrected premise =
  stronger model advises cheaper driving agent (user).
- 2026-06-16: Research required and approved; delegated to sub-agent; premise confirmed
  against Anthropic advisor tool + pi-advisor (see research.md).
- 2026-06-16: Engagement model + relation to `plan` deferred to research; research →
  model-invoked client-side tool, separate from delegation.
- 2026-06-16: Output = free-form text (user); trigger = model-invoked tool only,
  prompt-steered, no auto-hook in v1 (user).
- 2026-06-16: Second research round on context strategy (research_002.md). User raised that
  delegation hides sub-agent tool calls from the parent transcript — confirmed against
  SUBAGENT_DELEGATION.md. Decided: pass live (compaction-budgeted) parent conversation +
  head/tail cap; reject dedicated summarisation; defer explicit curation. Advisor scoped to
  high-level steering.
- 2026-06-16: Advisor steering preamble will adapt Anthropic's published coding-task system
  prompt (timing guidance: consult before substantive work / when stuck / before declaring
  done), captured in research_002.md.
- 2026-06-16: User notes resolved — (1) disabled by default; (3) send live conversation
  as-is, drop the head/tail cap; (4) tool statically registered when enabled, steering via
  guidance placement not tool gating; (6) cap enforced in handler, never by mutating tool
  defs (cache integrity); (2/5) Layer A base preamble approved as written + Layer B nudges
  added to plan/implement/review skills.
