User-facing documentation: [Sub-agent Delegation](sub-agent-delegation.md).

## Part 2 — Internals

### Architecture

```
┌─────────────────────────────────────────────┐
│  Parent Agent Loop (internal/agent)         │
│                                             │
│  Specialized sub-agent tools (explore,      │
│  research, code, evaluate, sanity_check, review, vision) call into    │
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
| `cmd/steiner`         | `buildActiveRegistry()` wires delegation tools into the active registry                                                                    |

### Tool registration

When `SubAgent.Enabled` is `true`, `delegation.BuildDelegateRegistry` clones the base registry and registers the `follow_up` tool plus a specialised tool for each agent type (`explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, and conditionally `vision`). Specialised tools are thin wrappers over the same delegation infrastructure (`BuildChildRun` + `SpawnDelegate`) with a baked-in system prompt, a per-type tool allowlist (`AgentAllowedTools`), and a task-oriented schema. The `vision` tool additionally accepts an `image_id` parameter and is only registered when `sub_agent.agents.vision.model` is configured.

`fetch_url` is registered unconditionally in the base built-in tool set. `web_search` is registered conditionally — it is added to both the parent registry and the extended base registry (used for child bootstrapping) only when a `web.Searcher` backend is configured. When no search backend is configured, the `research` sub-agent type is excluded from delegation entirely, so no stub or unavailable tool is ever exposed.

### Bootstrapping a child run

`BuildChildRun()` assembles the full `agent.RunRequest`:

**1. Derive limits.** `deriveChildLimits()` combines `SubAgentConfig` defaults with spec-level overrides using tighten-only semantics — an override is applied only when it is more restrictive than the configured default. Defaults: `MaxTurns` 15, `MaxTokens` 100,000. `timeout` is accepted as an optional parameter and defaults to no timeout.

**2. Build child prompt.** The child prompt is minimal: either the caller-provided `system_prompt` or a default, plus a single user message containing the task (and optional `context`). The system prompt is passed via `PromptOverrides` so the provider sees exactly one system message. When `DelegationSpec.Images` is non-empty, those images are attached to the first user message so the child model sees them immediately without spending a turn on a `read` call.

**3. Build child registries.** Two registries are built from the parent via `BootstrapDeps.AllowedTools` (populated per agent type from `AgentAllowedTools(agentType)`):
- **Visible registry** — what the model can see and request: parent base registry tools filtered to `AllowedTools`, always excluding `follow_up` and `workflow_handoff`.
- **Execution registry** — same filtered tools but with all approval modes forced to `ApprovalModeAuto`.

If `AllowedTools` is empty, no tools are available to the child. This ensures children cannot delegate further, never block on approval, and only access the explicitly permitted tool set for their agent type.

**4. Assemble RunRequest.** Includes the parent's provider instance, a tool executor wrapping the execution registry, `ExtraParams` and `PromptSuffix` propagated from the parent's model config, and no explicit model override (child uses the parent's provider/model by default, unless a per-type model alias is configured).

**5. Skip project context for selected types.** Certain agent types skip the
project context injection to keep the child prompt focused and cheap:

- **Skip project context:** `explore`, `research`, `sanity_check`, `vision` — these
  agents navigate the codebase, search, run checks, or analyze images; project
  context would add noise.
- **Keep project context:** `code`, `review`, `evaluate` — these agents need full
  project awareness to implement changes, review code, or evaluate design approaches.

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

### Vision handler

The `vision` tool uses a dedicated handler (`newVisionHandler`) rather than the generic `newSpecializedHandler`. When invoked:

1. Validates `task` and `image_id` inputs.
2. Looks up the `ImageRef` from `ImageStore` (registered when the image was pasted).
3. Reads the image file from `.steiner/tmp/images/` and base64-encodes it.
4. Builds a `DelegationSpec` with `Images` populated — the image is attached to the sub-agent's first conversation message via `buildChildPrompt`.
5. Resolves the per-type model from `sub_agent.agents.vision.model`.
6. Calls `BuildChildRun` and `SpawnDelegate` with the vision allowlist (`["read"]`) and vision system prompt.
7. Saves the child session so the parent model can use `follow_up` for additional questions about the same image without re-uploading it.
8. Appends a `follow_up` reminder (with `agent_id`) to the returned result.

**ImageStore** is a goroutine-safe session-scoped registry in `internal/agent/image_store.go`. It maps auto-assigned IDs (`img-1`, `img-2`, …) to `ImageRef` values (file path, media type, dimensions, size). Images are saved to `.steiner/tmp/images/` on paste with `YYYYMMDD_HHMMSS_<hex>.ext` filenames and deleted on agent exit (`imageStore.Cleanup()`). `ImageStore` is wired from the composition root through `DelegateDeps.ImageStore` → `SpecializedToolDeps.ImageStore` → vision handler.

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

1. **One level only**: children never have access to `follow_up` or `workflow_handoff`.
2. **No approval prompts**: child tool execution is auto-approved.
3. **Default context manager**: children use the same baseline context manager path as the parent.
4. **Tighten-only overrides**: caller cannot exceed configured limits, only reduce them.
5. **Model resolution**: children use the parent provider/model by default; specialised per-type model aliases resolve before the child run is built.
6. **Synchronous execution**: each delegate runs to completion before control returns to the parent.
7. **Filesystem shared**: children operate in the same workdir as the parent.
8. **Extension cap**: maximum 5 auto-extensions to prevent runaway children.
9. **Summary cap**: retention summaries capped at 1000 runes.
10. **No conversation leakage**: child conversation is not appended to parent; only the structured result and retention summary persist.
11. **Enforced allowlist**: `BootstrapDeps.AllowedTools` is enforced during child registry construction; only listed tools (minus `follow_up` and `workflow_handoff`) are visible and executable.
12. **Per-type allowlists**: each specialised agent type has its own tool allowlist, resolved via `AgentAllowedTools(agentType)` and passed as `BootstrapDeps.AllowedTools` — there is no user-configurable global allowlist.
