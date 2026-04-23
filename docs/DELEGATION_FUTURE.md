# Delegation — Deferred Features

This document holds delegation capabilities that are intentionally **not** part of the Stage 8 scaffolding or the Stage 9 first-execution stage, but are plausible follow-ups. It exists so that `ROADMAP.md`, `PRD.md`, and `INITIAL_IMPLEMENTATION_PLAN.md` can stay focused on in-flight work while longer-horizon delegation ideas remain captured and explorable.

Status of each item below: **deferred**. None of these are implemented or scheduled. They will be pulled into a real stage in `ROADMAP.md` before any implementation begins. Until then, treat this file as design-intent notes only.

Baseline for reference — the delegation surface that Stage 8 actually ships:

* a model-facing `delegate` tool on the parent agent
* one-shot, synchronous child execution (parent's tool call blocks until the child returns)
* fresh child context (no parent transcript, own system prompt, own tool registry)
* nested delegation blocked at the child tool-registry level (child has no `delegate` tool)
* scheduler-bounded concurrency via `provider.parallelism`, achievable by the parent emitting multiple `delegate` tool_use blocks in a single assistant turn
* result envelope returning final answer, status, agent id, turn and token counters; oversized child output is summarised via one extra child turn, not truncated

Everything below extends or replaces pieces of that baseline.

---

## 1. Background (non-blocking) delegation

### Idea

`delegate(..., background: true)` returns immediately with an `agent_id` instead of blocking the parent's tool call. The child continues running concurrently. The parent continues issuing tool calls or reasoning in the same assistant turn, and retrieves the child's result later.

### Why it is useful

* The parent can keep working while long-running children explore or refactor in the background.
* Fan-out beyond a single assistant turn's multi-tool-call emission becomes natural.
* Enables "status check" and "continue the work" patterns seen in Codex-style agents, where the parent periodically asks an in-flight child for progress.

### Why Stage 8 does not ship it

* Scaffolding scope. The synchronous path already exercises every seam Stage 8 must prove.
* Adds a poll/notify plumbing requirement, TUI state for in-flight children, orphan cleanup, and a cancellation path — none of which are load-bearing for proving the lifecycle works.
* Parallelism alone is already achievable synchronously via multi-tool-call emission gated by `provider.parallelism`.

### What the feature needs when it lands

* A session registry keyed by `agent_id` with lifecycle state, owned by `internal/delegation`.
* Result retrieval surface for the model. Options:
  * companion tool `delegate_wait(agent_id)` that blocks until the child finishes
  * companion tool `delegate_status(agent_id)` that returns current status + partial output without blocking
  * push-based completion event into the agent event stream that the parent loop observes
  * most likely a combination: event-driven completion plus an explicit `delegate_wait` for synchronous join
* Cancellation tool `delegate_cancel(agent_id)` with a clear semantics contract (graceful request vs hard abort).
* Orphan and timeout cleanup. A child whose parent session ended must not linger.
* Scheduler integration so background children still consume `provider.parallelism` slots and release them on completion.
* TUI rendering of in-flight children (distinct from synchronous delegation placeholders) so the operator can see what is running.
* Cost and budget reporting across concurrent children.

### Known gotchas

* The parent model must reason about `agent_id` values opaquely. The tool contract should make it impossible for the parent to confuse identifiers.
* Push events landing mid-assistant-turn must not corrupt streamed model output.
* Retrieving a partial-but-not-finished child state needs a clear contract; it is easy to leak intermediate reasoning this way if done carelessly.

### Reference implementations

* Claude Code `Agent(..., run_in_background: true)` — returns a handle, later harvested by the parent's agentic loop.
* Claude Code `agent-teams` — uses `SendMessage` to deliver follow-up prompts to a persistent teammate session.

---

## 2. Re-promptable child sessions

### Idea

Instead of every `delegate` call being a fresh one-shot child, the parent can open a session, send multiple tasks to the same child over time, and close it when done. The child's working history persists across calls.

### Why it is useful

* "Continue the previous investigation with this extra hint" without paying the context-setup cost of a fresh spawn.
* Natural pairing with background mode for long-running assistants the parent returns to.
* Matches the Codex workflow the user referenced.

### Why Stage 8 does not ship it

* In synchronous mode, re-prompting is indistinguishable from issuing another `delegate` call with a refined task. The only added value is skipping setup cost, which is not worth the lifecycle complexity.
* Re-promptable sessions only become meaningfully different from fresh spawns when background mode exists. They are most naturally planned together.
* Persistence plus context compaction plus cancellation across multiple prompts introduce several new failure modes.

### What the feature needs when it lands

* New contract surface next to `SpawnDelegate`:
  * `OpenDelegate(ctx, spec) (SessionHandle, error)`
  * `SendDelegate(ctx, handle, task string) (DelegationResult, error)`
  * `CloseDelegate(ctx, handle) error`
* Session state: child's conversation history, remaining budget, allowed tools snapshot, current status.
* Compaction strategy for the child's growing history; can likely reuse `internal/prompt` compaction primitives.
* Event types for `DelegationSessionOpened`, `DelegationSessionClosed`, plus per-message `DelegationStarted` / `DelegationCompleted` events that reference the session id.
* TUI rendering that shows sessions as persistent entities with per-message activity.
* Clear semantics when a session exceeds limits mid-send: does the session die or survive?

### Known gotchas

* Re-prompts are fertile ground for prompt injection carried over from prior child outputs if the parent feeds them back in.
* Long sessions with compacted history become hard to reproduce; debuggability drops.
* Tool-allowlist changes mid-session are ambiguous; the contract should forbid them.

### Reference implementations

* Claude Code `agent-teams` — persistent teammate sessions addressable by name via `SendMessage`.

---

## 3. `touched_files` in the delegation result envelope

### Idea

Add a structured `touched_files []string` (or a richer `[]FileChange`) field to `DelegationResult` so the parent can see exactly which paths the child mutated without reading the child's transcript.

### Why it is useful

* Audit and review: the operator can quickly see scope of child edits.
* Parent reasoning: the parent can reference specific paths in follow-up instructions without re-discovering them.
* UX: the TUI can render a compact diff summary of child work.

### Why Stage 8 does not ship it

* No established framework (Claude Code, OpenAI Agents SDK, smolagents) surfaces this field. There is no existing convention to mirror.
* Requires executor-level mutation tracking wired into every write/edit/bash tool used by the child. That couples the delegation contract to tool internals while the tool registry is still stabilising.
* Current result envelope stays minimal (final answer, status, counters), which keeps Stage 8 focused on the seam.

### What the feature needs when it lands

* Mutation-tracking middleware around the child's tool registry. Each write-effectful tool reports touched paths to a per-child recorder.
* `FileChange` shape decision: path only, or path plus action (`created`, `modified`, `deleted`), or path plus pre/post hashes.
* Size cap on the list; very large migrations could produce thousands of paths and bloat parent context.
* Result-envelope versioning so adding the field does not break any callers that materialise `DelegationResult`.
* TUI rendering of the list as a compact per-delegation summary.

### Known gotchas

* Bash tool usage is the easy blind spot. Without parsing the command, the recorder cannot know what paths were touched. Some `shell_edit` approaches or post-run git diff may be needed for completeness.
* If the recorder lies or lags, the parent's mental model diverges from reality. The field must be either accurate or absent — never partially filled without a flag.

---

## 4. Parallel sub-agents as an explicit capability

### Idea

A more developed story around many concurrent children: parent-driven fan-out with explicit joins, aggregate result handling, TUI fleet view, cost dashboards.

### Why it is useful

* Some tasks (e.g. "search this concept across N subtrees") are embarrassingly parallel.
* Explicit joins and aggregate rendering make high-concurrency delegation readable instead of noisy.

### Why Stage 8 does not ship it

* Stage 8 already allows concurrent children via `provider.parallelism` and multi-tool-call emission; that is a runtime property, not a feature. Promoting parallel delegation to a product capability (with join primitives, fleet UI, cost rollups) is a separate, larger chunk of work.
* `ROADMAP.md` already lists "parallel sub-agents" under Stage 11 as a candidate advanced capability.

### What the feature needs when it lands

* Explicit fan-out / join helpers for the parent model, or a contract for the model to reason about batches of delegations.
* TUI fleet view showing all in-flight children with status, budget, and elapsed time.
* Aggregate cost and token tracking across concurrent children.
* Deadline and budget guardrails that apply to the batch, not only per-child.

---

## Relation to current docs

* `docs/ROADMAP.md` Stage 8 ships the baseline above.
* `docs/ROADMAP.md` Stage 9 focuses on the delegation UX and observability polish once the baseline is in.
* `docs/ROADMAP.md` Stage 11 references "parallel sub-agents" as an advanced branch; the detail lives here.
* `docs/PRD.md` §14.5 lists architectural deferrals; this file is the detail companion for the delegation-specific ones.
* `docs/IDEAS.md` is a scratchpad and points here for delegation deferrals instead of duplicating them.
