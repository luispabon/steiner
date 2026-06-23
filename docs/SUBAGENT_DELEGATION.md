# Sub-agent delegation

`steiner` exposes six sub-agent-as-tool operations that delegate bounded tasks to isolated child agents. This document covers both user-facing usage (tools, configuration, safety) and the internal delegation machinery.

`advisor` is separate from delegation: it is a stronger-model steering pass over the live parent conversation, with no tools and no child loop. The advisor lives alongside the delegation tools in the main loop, but it is not a child agent.

---

## Part 1 — User guide

### Available tools

Sub-agent delegation is **enabled by default**. When it is, the model sees six additional tools alongside the built-in ones:

| Tool        | What it does                                                                     | Extra params                                               | Can mutate?            |
|-------------|----------------------------------------------------------------------------------|------------------------------------------------------------|------------------------|
| `explore`   | Navigate the codebase to find files, symbols, call sites, and patterns           | `task` only                                                | No                     |
| `research`  | Gather and synthesise information from the codebase or web                       | `task` only                                                | No                     |
| `code`      | Implement a scoped change — read relevant files, write changes, run tests        | `task` only                                                | Yes (`mutate`, `bash`) |
| `plan`      | Analyse a sub-problem, evaluate options, and produce a structured recommendation | `task` only                                                | No                     |
| `verify`    | Run tests, linters, builds, or other checks and report pass or fail              | `task` only                                                | No                     |
| `follow_up` | Resume an existing sub-agent session by agent ID with a new user message         | `agent_id`, `message`                                      | No (resumes existing)  |

The five specialised tools (`explore`, `research`, `code`, `plan`, `verify`) are hardcoded with purpose-built system prompts and tool allowlists. The `follow_up` tool resumes a previously delegated child agent while preserving its conversation history. The parent-only `workflow_handoff` tool creates a handoff request for the current session; it is not exposed to child agents yet.

### Advisor

The `advisor` tool is a pure reasoning pass for the parent agent. It reads the live parent conversation, calls a stronger model, and returns concise strategic guidance. It does **not** expose any tools, does **not** start a child loop, and does **not** mutate state. Its per-run cap is enforced in handler state so the tool definition stays stable for prompt-cache integrity.

### When to use each

| Situation                                              | Tool                                                           |
|--------------------------------------------------------|----------------------------------------------------------------|
| Find DRY/refactoring opportunities across the codebase | `explore` — report files, repeated patterns, risks, next steps |
| Fix a bug but location is unknown                      | `explore` — search likely areas and report exact files/code    |
| Need to understand an external API or library          | `research` — gather docs, usage examples, and constraints      |
| Implement a small known change in one package          | `code` — implement if ownership and tests are clear            |
| Understand how a feature works across multiple files   | `explore` — trace the call chain and report                    |
| Evaluate two approaches to a design problem            | `plan` — analyse tradeoffs and recommend                       |
| Run broad verification while continuing local work     | `verify` — run checks and summarise exact failures             |

`plan` is for focused sub-problem analysis, **not** overall task planning.

### `follow_up`

The `follow_up` tool lets the parent model send a new user message to an existing child session identified by `agent_id`. This is useful when a sub-agent's initial response leads to follow-up questions or iterative refinement.

Key behaviours:

- **Preserves conversation** — the child's prior message history is retained and the new message is appended.
- **Resets budget** — each follow-up resets the child's turn and token budgets to the configured defaults (not the remaining budget from the prior run).
- **Tracks follow-ups** — the returned result includes a `follow_up_count` field so the parent can see how many follow-ups have occurred.
- **Auto-approved** — the `follow_up` tool is approval mode `auto` (no user gate).
- **No nesting** — `follow_up` is stripped from child agent registries, so sub-agents cannot follow-up on other sub-agents.

### Safety

- A sub-agent **cannot delegate further** — `delegate` and `follow_up` tools are always stripped from child registries.
- The parent-only `workflow_handoff` tool is not included in child allowlists yet.
- Only the `code` sub-agent has access to file-mutation tools (`mutate`) or `bash`.
- `explore`, `research`, and `plan` are read-only.
- `verify` can run commands via `bash` but must not modify files.
- All sub-agent tools are automatically approval-gated as `auto` — no manual prompt is needed to use them.
- The child's full conversation transcript is not copied into the parent session; only a structured result and bounded summary persist.

### Default tool allowlists

| Agent      | Tools available                                             |
|------------|-------------------------------------------------------------|
| `explore`  | `read`, `glob`, `grep`, `ls`                                |
| `research` | `read`, `glob`, `grep`, `ls`, `web_search`\*, `fetch_url`\* |
| `code`     | `read`, `glob`, `grep`, `ls`, `mutate`, `bash`              |
| `plan`     | `read`, `glob`, `grep`, `ls`                                |
| `verify`   | `read`, `glob`, `grep`, `ls`, `bash`                        |

\* `web_search` and `fetch_url` are not yet implemented. The `research` agent won't be fully available until a `web_search` backend is configured — see the README for details.

### Configuration

Sub-agents are configured under the `sub_agent` key in `config.yaml`:

```yaml
sub_agent:
  # Master switch — set to false to remove all sub-agent tools from the model.
  enabled: true

  # Default limits for all sub-agents (the code applies a floor of 15 turns).
  max_turns: 30
  max_tokens: 100000

  # Per-agent-type model overrides. When set, sub-agents of that type use
  # a different model than the parent agent.
  agents:
    code:
      model: gpt-4o
    research:
      model: claude-sonnet-4
```

Each entry under `agents` keyed by agent type name can set `model` to any model alias defined in your `models` configuration. If no override is set, the sub-agent uses the same model as the parent.

---

## Part 2 — Internals

### Architecture

```
┌─────────────────────────────────────────────┐
│  Parent Agent Loop (internal/agent)         │
│                                             │
│  Specialized sub-agent tools (explore,      │
│  research, code, plan, verify) call into    │
│  BuildChildRun() directly.                  │
│                                             │
│                 ▼                           │
│  ┌──────────────────────────────────┐       │
│  │ delegation.BuildChildRun()       │       │
│  │  - derive limits                 │       │
│  │  - build child prompt            │       │
│  │  - build child registries        │       │
│  │  - assemble RunRequest           │       │
│  └──────────────┬───────────────────┘       │
│                 │                           │
│                 ▼                           │
│  ┌──────────────────────────────────┐       │
│  │ delegation.SpawnDelegate()       │       │
│  │  - context timeout               │       │
│  │  - emit DelegationStarted        │       │
│  │  - runner.Run(childCtx, req)     │       │
│  │  - auto-extension loop (≤5x)     │       │
│  │  - summarisation turn            │       │
│  │  - emit DelegationComplete       │       │
│  └──────────────┬───────────────────┘       │
│                 │                           │
│                 ▼                           │
│  tool.ExecutionResult + ToolRetention       │
│  (persisted on parent conversation message) │
└─────────────────────────────────────────────┘
```

### Package layout

| Package               | Responsibility                                                                                                                               |
|-----------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `internal/delegation` | Contract types, tool definition, handler, bootstrapping, spawn logic, limits, result building, specialised agent types and tool constructors |
| `internal/agent`      | Retention metadata on messages, runner interface                                                                                             |
| `internal/tool`       | `ToolRetention` struct, `ExecutionResult.Retention` field, `Registry.Clone()`                                                                |
| `internal/prompt`     | Delegation instructions preamble injected when delegation is enabled                                                                         |
| `internal/output`     | Delegation lifecycle events (started, complete, failed, extension)                                                                           |
| `internal/tui`        | Rendering of delegation events with spinner, lifecycle tracking, collapsible output                                                          |
| `cmd/steiner`         | `buildActiveRegistry()` wires delegate tools into the active registry                                                                    |

### Tool registration

When `SubAgent.Enabled` is `true`, `cmd/steiner` clones the base registry and registers all five specialised tools. Specialised tools are thin wrappers over the same delegation infrastructure (`BuildChildRun` + `SpawnDelegate`) with a baked-in system prompt, a narrower tool allowlist, and a simpler schema (single `task` parameter).

`web_search` and `fetch_url` are registered as stub tools with "not yet implemented" handlers. They are included in the research agent's allowlist so the schema is complete from day one. An extended base registry is used as the parent reference for child bootstrapping so these stubs are available for child registry filtering without being exposed in the parent model's tool list.

### Bootstrapping a child run

`BuildChildRun()` assembles the full `agent.RunRequest`:

**1. Derive limits.** `deriveChildLimits()` combines `SubAgentConfig` defaults with spec-level overrides using tighten-only semantics — an override is applied only when it is more restrictive than the configured default. Defaults: `MaxTurns` 15, `MaxTokens` 100,000. `timeout` is accepted as an optional parameter and defaults to no timeout.

**2. Build child prompt.** The child prompt is minimal: either the caller-provided `system_prompt` or a default, plus a single user message containing the task (and optional `context`). The system prompt is passed via `PromptOverrides` so the provider sees exactly one system message.

**3. Build child registries.** Two registries are built from the parent:
- **Visible registry** — what the model can see and request: parent base registry tools filtered to `allowed_tools`, always excluding `delegate` and `follow_up`.
- **Execution registry** — same filtered tools but with all approval modes forced to `ApprovalModeAuto`.

If `allowed_tools` is empty, no tools are available to the child. This ensures children cannot delegate further, never block on approval, and only access the explicitly permitted tool set.

**4. Assemble RunRequest.** Includes the parent's provider instance, a tool executor wrapping the execution registry, `ExtraParams` and `PromptSuffix` propagated from the parent's model config, and no explicit model override (child uses the parent's provider/model by default, unless a per-type model alias is configured).

### Execution: SpawnDelegate

`SpawnDelegate()` orchestrates the child lifecycle:

1. **Timeout**: if `spec.Limits.Timeout > 0`, wraps context with `context.WithTimeout`.
2. **Emit** `DelegationStartedEvent` with agent ID and task preview (120 chars max).
3. **Run** the child agent loop via the `AgentRunner` interface.
4. **Auto-extension loop** (up to 5 iterations): if the child stopped due to `MaxTurns` AND its last message contains pending tool calls (mid-work), the loop extends by re-running with the accumulated conversation and an increased turn budget.
5. **Build result** from final state (maps `StopReason` → `DelegationStatus`).
6. **Summarisation turn**: runs a single no-tool turn asking the model to summarise its work in ≤1000 chars.
7. **Emit** `DelegationCompleteEvent` or `DelegationFailedEvent`.
8. **Return** `tool.ExecutionResult` with `ToolRetention` metadata attached.

A child "needs extension" when `StopReason == StopReasonMaxTurns` AND the last assistant message has pending tool calls (interrupted mid-action). This prevents early termination when a delegate is actively working but hit its turn cap.

`StopReasonMaxTurns` and `StopReasonMaxTokens` map to `StatusPartial`. A partial result means the child's budget was exhausted before it could finish. Parent models must treat partial results conservatively — do not assume the delegated task succeeded, and retry or narrow scope rather than treating partial output as authoritative.

### Result and retention

**DelegationResult** (returned to the parent model):

| Field        | Description                                           |
|--------------|-------------------------------------------------------|
| `AgentID`    | Matches the request                                   |
| `Status`     | `complete`, `partial`, `failed`, or `cancelled`       |
| `Output`     | Last assistant message content                        |
| `Summary`    | Retained summary (≤1000 runes)                        |
| `TurnCount`  | Turns consumed by the child                           |
| `TokenCount` | Tokens consumed by the child                          |
| `StopReason` | Populated on partial: `"max_turns"` or `"max_tokens"` |
| `Error`      | Populated on failure                                  |

**ToolRetention** persists on the parent conversation message as metadata that is not sent to the provider:

| Field        | Description          |
|--------------|----------------------|
| `Kind`       | `"delegate_summary"` |
| `Summary`    | Condensed findings   |
| `AgentID`    | Child agent ID       |
| `Status`     | Result status        |
| `TurnCount`  | Turns consumed       |
| `TokenCount` | Tokens consumed      |

**Summarisation turn.** After the child completes, a follow-up single-turn (no tools allowed) asks the model to produce a concise summary. If the summarisation turn fails or returns empty, the raw output is truncated to 1000 runes as a fallback.

**Retention path.** The child agent's full transcript is not copied into the parent session. The parent keeps the delegate result plus a bounded summary. Compaction may later summarise older parent conversation state, including delegated work, through the normal baseline path.

### Event lifecycle

Events emitted during delegation (via `output.EventSink`):

| Event                  | When                            | Key fields                                                  |
|------------------------|---------------------------------|-------------------------------------------------------------|
| `delegation_started`   | Before child run begins         | `agent_id`, `task_preview`                                  |
| `delegation_extension` | Each auto-extension iteration   | `agent_id`, `extension`, `max_extensions`                   |
| `delegation_complete`  | After summarisation, on success | `agent_id`, `status`, `turn_count`, `token_count`, `output` |
| `delegation_failed`    | On initial child run error      | `agent_id`, `task_preview`, `error`                         |

The TUI renders delegation lifecycle events with a spinner during execution, lifecycle state labels, and collapsible output panels for completed delegations. Extension events update an always-visible counter in the status bar.

### System prompt integration

When delegation is enabled, the system prompt preamble includes a delegation instructions block: a quick-reference table of tools, classification heuristics (when to delegate vs work locally), task categories suited for delegation, guidance on what makes a good delegation, and explicit constraints (no nesting, no user questions from sub-agents).

### Constraints and invariants

1. **One level only**: children never have access to `delegate` or `follow_up`.
2. **No approval prompts**: child tool execution is auto-approved.
3. **Default context manager**: children use the same baseline context manager path as the parent.
4. **Tighten-only overrides**: caller cannot exceed configured limits, only reduce them.
5. **Model resolution**: children use the parent provider/model by default; specialised per-type model aliases resolve before the child run is built.
6. **Synchronous execution**: each delegate runs to completion before control returns to the parent.
7. **Filesystem shared**: children operate in the same workdir as the parent.
8. **Extension cap**: maximum 5 auto-extensions to prevent runaway children.
9. **Summary cap**: retention summaries capped at 1000 runes.
10. **No conversation leakage**: child conversation is not appended to parent; only the structured result and retention summary persist.
11. **Enforced allowlist**: `allowed_tools` is enforced during child registry construction; only listed tools (minus `follow_up`) are visible and executable.
12. **Specialised tool allowlists**: each specialised type enforces a per-type tool allowlist that is narrower than the global `allowed_tools`.
