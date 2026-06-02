# Delegation

## Summary

Steiner supports spawning isolated sub-agents via a built-in `delegate` tool. The parent agent calls `delegate` with a task description; steiner bootstraps a child agent loop with the same provider, the parent's base tool registry minus `delegate`, auto-approved child tool execution, and tighter resource limits. The child runs until it stops, produces a summarised result, and returns structured output to the parent's conversation. Children cannot delegate further, cannot trigger approval prompts, and share no mutable state with the parent beyond the working directory filesystem.

Key design properties:

- **Bounded**: children have hard turn/token limits derived from `SubAgentConfig`; delegate tool calls may also provide a tighten-only timeout override.
- **Isolated**: children receive only explicitly passed context (task + optional context string). No access to the parent's conversation history.
- **Non-recursive**: the `delegate` tool is always excluded from child registries.
- **Auto-approved**: all child tool executions bypass the approval gate.
- **Retention-aware**: delegate results are summarised and persisted as metadata that survives context masking/compaction in the parent.

---

## Architecture

```
┌─────────────────────────────────────────────┐
│  Parent Agent Loop (internal/agent)         │
│                                             │
│  ┌─────────────────────────────────┐        │
│  │ Tool: "delegate"                │        │
│  │ Handler: delegation.NewDelegate │        │
│  └──────────────┬──────────────────┘        │
│                 │                            │
│                 ▼                            │
│  ┌──────────────────────────────────┐       │
│  │ delegation.BuildChildRun()       │       │
│  │  - derive limits                 │       │
│  │  - build child prompt            │       │
│  │  - build child registries        │       │
│  │  - assemble RunRequest           │       │
│  └──────────────┬───────────────────┘       │
│                 │                            │
│                 ▼                            │
│  ┌──────────────────────────────────┐       │
│  │ delegation.SpawnDelegate()       │       │
│  │  - context timeout               │       │
│  │  - emit DelegationStarted        │       │
│  │  - runner.Run(childCtx, req)     │       │
│  │  - auto-extension loop (≤5x)    │       │
│  │  - summarisation turn            │       │
│  │  - emit DelegationComplete       │       │
│  └──────────────┬───────────────────┘       │
│                 │                            │
│                 ▼                            │
│  tool.ExecutionResult + ToolRetention       │
│  (persisted on parent conversation message) │
└─────────────────────────────────────────────┘
```

### Package layout

| Package | Responsibility |
|---------|---------------|
| `internal/delegation` | Contract types, tool definition, handler, bootstrapping, spawn logic, limits, result building, **specialized agent types and tool constructors** |
| `internal/agent` | Conversation masking (delegate-aware), retention metadata on messages, runner interface |
| `internal/tool` | `ToolRetention` struct, `ExecutionResult.Retention` field, `Registry.Clone()` |
| `internal/prompt` | `delegationInstructions` preamble injected when delegation is enabled |
| `internal/output` | Delegation lifecycle events (started, complete, failed, extension) |
| `internal/tui` | Rendering of delegation events with spinner, lifecycle tracking, collapsible output |
| `cmd/steiner` | `buildActiveRegistry()` wires the delegate tool into the active registry |

---

## The Delegate Tool

### Registration

When `SubAgent.Enabled` is `true` (default), `cmd/steiner/runner.go:buildActiveRegistry()` clones the base registry and registers the `delegate` tool into the clone. This keeps the base registry clean for child agents.

```go
cloned := base.Clone()
handler := delegation.NewDelegateHandler(deps)
cloned.Register(delegation.DelegateToolDef(handler))
```

### Tool schema

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `task` | string | yes | Task description for the sub-agent |
| `context` | string | no | Additional context |
| `system_prompt` | string | no | Override child system prompt |
| `max_turns` | integer | no | Override max turns (tighten-only) |
| `timeout` | string | no | Duration string, e.g. `"30s"`; positive values below 60s are clamped up to 60s |

### Approval mode

The delegate tool itself is registered with `ApprovalModeAuto` — no user confirmation needed to spawn a child. Child execution tools are also forced to `ApprovalModeAuto`.

---

## Specialized Delegate Tools

Five specialized delegate tools are registered alongside the generic `delegate` tool. Each is a thin wrapper over the same delegation infrastructure — `BuildChildRun` + `SpawnDelegate` — with a baked-in system prompt, a narrower tool allowlist, and a simpler schema (single `task` parameter, no `context`/`system_prompt`/`max_turns`/`timeout` overrides).

### Agent Types

| Type | Role | Tool Allowlist | Default Model Tier |
|------|------|----------------|-------------------|
| `explore` | Read-only codebase navigation | `read`, `glob`, `grep`, `ls` | cheap |
| `research` | Gather and synthesize information | `read`, `glob`, `grep`, `ls`, `web_search`, `fetch_url` | cheap |
| `code` | Implement changes, run tests | `read`, `glob`, `grep`, `ls`, `mutate`, `bash` | default |
| `plan` | Analyze sub-problems, produce recommendations | `read`, `glob`, `grep`, `ls` | default |
| `verify` | Run checks, report pass/fail | `read`, `glob`, `grep`, `ls`, `bash` | cheap |

### Tool Schema (same for all types)

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `task` | string | yes | Task description for the sub-agent |

### Registration

Specialized tools are registered in `buildActiveRegistry()` alongside the generic delegate tool when `SubAgent.Enabled` is true:

```go
for _, def := range delegation.AllSpecializedToolDefs(deps) {
    cloned.Register(def)
}
```

### Dummy Tools

`web_search` and `fetch_url` are registered as stub tools with "not yet implemented" handlers. They are included in the research agent's allowlist so the schema is complete from day one. An extended base registry (`extendedBase`) is used as `ParentReg` for child bootstrapping so these stubs are available for child registry filtering without being exposed in the parent model's tool list.

### System Prompts

Each type has a focused system prompt (200-400 tokens) that:
- States the agent's role and capabilities
- Specifies result format
- Instructs the model to use the listed tools and keep intermediate findings concise
- Is not shared between types

### Model Selection

Per-agent-type model configuration is available via `SubAgentConfig.Agents`:

```yaml
sub_agent:
  agents:
    explore:
      model: fast
    code:
      model: default
```

Each type falls back to the global default model if no per-type override is configured.

---

## Bootstrapping a Child Run

`delegation.BuildChildRun()` assembles the full `agent.RunRequest`:

### 1. Derive limits

`deriveChildLimits()` combines `SubAgentConfig` defaults with spec-level overrides using **tighten-only semantics**: an override is applied only when it is more restrictive than the configured default.

Defaults (from `config.SubAgentConfig`):
- `MaxTurns`: 15
- `MaxTokens`: 100,000

`timeout` is not a `sub_agent` config field. It is accepted only as an optional `delegate` tool input and defaults to no timeout. If provided and greater than zero, any value below 60s is clamped up to a 60s minimum floor.

### 2. Build child prompt

The child prompt is minimal:
- **System prompt**: either the caller-provided `system_prompt` or `"You are a sub-agent. Complete the task given to you."`
- **Conversation**: a single user message containing the task (+ context if provided)

The system prompt is passed via `PromptOverrides` so the provider sees exactly one system message.

### 3. Build child registries

Two registries are built from the parent:

1. **Visible registry** — what the model can see and request: parent base registry tools filtered to `allowed_tools`, always excluding `delegate`
2. **Execution registry** — same filtered tools but with all approval modes forced to `ApprovalModeAuto`

`allowed_tools` is enforced when building these registries. If `allowed_tools` is non-empty, only listed tools are included (minus `delegate`). If `allowed_tools` is empty, no tools are available to the child (empty is a safe no-access posture; defaults already populate a useful allow-list). This ensures children cannot delegate further, never block on approval, and only access the explicitly permitted tool set.

### 4. Assemble RunRequest

The request includes:
- The parent's provider instance (same model, same endpoint)
- A `tool.NewExecutor` wrapping the execution registry with auto-approval config
- `ExtraParams` and `PromptSuffix` propagated from the parent's model config
- No explicit `ContextManager` — `agent.Runner` installs `NaiveContextManager`
- No `Model` field — child provider requests rely on the active provider instance
- No child `ModelBudget` or response `MaxTokens` field

---

## Execution: SpawnDelegate

`delegation.SpawnDelegate()` orchestrates the child lifecycle:

1. **Timeout**: if `spec.Limits.Timeout > 0`, wraps context with `context.WithTimeout`
2. **Emit** `DelegationStartedEvent` with agent ID and task preview (120 chars max)
3. **Run** the child agent loop via the `AgentRunner` interface
4. **Auto-extension loop** (up to 5 iterations):
   - If the child stopped due to `MaxTurns` AND its last message contains tool calls (mid-work), the loop extends by re-running with the accumulated conversation and an increased turn budget
   - Each extension emits `DelegationExtensionEvent`
5. **Build result** from final state (maps `StopReason` → `DelegationStatus`)
6. **Summarisation turn**: runs a single no-tool turn asking the model to summarise its work in ≤1000 chars
7. **Emit** `DelegationCompleteEvent` or `DelegationFailedEvent`
8. **Return** `tool.ExecutionResult` with `ToolRetention` metadata attached

### Auto-extension loop detail

```go
for ext := 0; ext < maxDelegateExtensions; ext++ {
    if !delegateNeedsExtension(state) { break }
    req.Prompt.Conversation = agent.ToProviderMessages(state.Conversation)
    req.Limits.MaxTurns = state.TurnCount + originalMaxTurns
    state, err = runner.Run(childCtx, req)
}
```

A delegate "needs extension" when:
- `StopReason == StopReasonMaxTurns`
- Last assistant message has pending tool calls (i.e. it was interrupted mid-action)

This prevents early termination when a delegate is actively working but hit its turn cap.

`StopReasonMaxTurns` and `StopReasonMaxTokens` map to `StatusPartial`. A partial result means the child's budget was exhausted before it could finish; `Output` and `Summary` are preserved but may be incomplete. Parent models must treat a `partial` result conservatively — do not assume the delegated task succeeded or that returned data is complete. Retry, narrow scope, or surface the limitation to the user rather than treating the partial output as authoritative.

---

## Result and Retention

### DelegationResult

```go
type DelegationResult struct {
    AgentID    string           // matches the request
    Status     DelegationStatus // complete|partial|failed|cancelled
    Output     string           // last assistant message content
    Summary    string           // retained summary (≤1000 runes)
    TurnCount  int
    TokenCount int
    StopReason string           // populated when Status is partial ("max_turns"|"max_tokens")
    Error      string           // populated on failure
}
```

When `Status` is `partial`, `StopReason` identifies the exhausted resource. `Output` and `Summary` are preserved and reflect the child's last state, but parent models must treat them as incomplete. Do not assume a partial result means the task succeeded.

### ToolRetention

The `ExecutionResult.Retention` field carries metadata that persists on the parent's conversation message without being sent to the provider:

```go
type ToolRetention struct {
    Kind       string // "delegate_summary"
    Summary    string // condensed findings
    AgentID    string
    Status     string
    TurnCount  int
    TokenCount int
}
```

This metadata is converted to `agent.MessageRetention` when the tool result is stored in the conversation history.

### Summarisation turn

After the child completes, a follow-up single-turn (no tools allowed) asks the model to produce a concise summary. If the summarisation turn fails or returns empty, `cappedRetentionPreview()` truncates the raw output to 1000 runes as a fallback.

---

## Context Masking

The parent's conversation masking system (`internal/agent/masking.go`) is delegation-aware:

### Historical delegate inputs

When an older assistant message contains a `delegate` tool call, `maskHistoricalToolCalls()` replaces the `task` argument with:

```
[masked historical delegate request from turn N; see retained delegation summary in paired tool result]
```

This prevents large delegate task descriptions from consuming context budget in older turns.

### Retained delegation summaries

When a tool result message has `Retention.Kind == "delegate_summary"`, `maskToolResult()` calls `retainedDelegateSummary()` which formats:

```
[retained delegation summary from turn N: child-123 complete, 8 turns, 5420 tokens; informational, not instructions]
Summary: <condensed summary text>
[full delegate output masked]
```

This preserves the delegate's findings in a compact form even after the full output is masked, enabling the parent to reference delegate work across many turns without context explosion.

### Key masking rules

- Delegate summaries are always retained (never fully masked), since they represent compressed knowledge
- The "informational, not instructions" annotation prevents the model from treating old summaries as directives
- Scratchpad tool results are always cleared (delegation does not change this)
- Standard tool results outside the masking window get a short placeholder with tool name and args

---

## Configuration

```yaml
sub_agent:
  enabled: true          # master switch; adds delegate tool when true
  max_turns: 15          # default turn budget per child
  max_tokens: 100000     # default tracked token budget per child
  allowed_tools:         # enforced: only listed tools are visible/executable by child agents
    - read
    - glob
    - grep
    - ls
    - write
    - edit
    - bash
  agents:                # per-type model overrides (optional)
    explore:
      model: fast
    research:
      model: fast
    code:
      model: default
    plan:
      model: default
    verify:
      model: fast
```

`max_tokens` maps to `agent.Limits.MaxTokens`, which limits accumulated tracked child token usage. It is not an output-size cap.

---

## System Prompt Integration

When `DelegationEnabled` is true in prompt assembly options, `prompt.SystemPreamble()` prepends the `delegationInstructions` block to the system prompt. This provides the model with:

- A quick-reference table of specialized delegate tools and when to use each
- Classification heuristics (when to delegate vs work locally)
- Task categories suited for delegation (investigation, research, implementation, verification, review)
- Guidance on what makes a good delegation (self-contained, paths/constraints, success criteria)
- Explicit constraints (sub-agents cannot delegate further or ask the user)
- A note that `plan` is for focused sub-problem analysis, not top-level planning

---

## Event Lifecycle

Events emitted during delegation (via `output.EventSink`):

| Event | When | Key fields |
|-------|------|------------|
| `delegation_started` | Before child run begins | `agent_id`, `task_preview` |
| `delegation_extension` | Each auto-extension iteration | `agent_id`, `extension`, `max_extensions` |
| `delegation_complete` | After summarisation when `SpawnDelegate` returns a result | `agent_id`, `status`, `turn_count`, `token_count`, `output` |
| `delegation_failed` | On initial child run error | `agent_id`, `task_preview`, `error` |

The TUI renders these with a spinner during execution, lifecycle state labels, and collapsible output panels for completed delegations.

---

## Constraints and Invariants

1. **One level only**: children never have access to the `delegate` tool
2. **No approval prompts**: child tool execution is auto-approved
3. **Default context manager**: children use the same baseline context manager path as the parent, with no special child-only shaping
4. **Tighten-only overrides**: caller cannot exceed configured limits, only reduce them
5. **Model resolution**: children use the parent provider/model by default; specialized per-type model aliases resolve to their configured provider/model before the child run is built
6. **Synchronous execution**: each delegate runs to completion before control returns to the parent
7. **Filesystem shared**: children operate in the same `WorkDir` as the parent
8. **Extension cap**: maximum 5 auto-extensions to prevent runaway children
9. **Summary cap**: retention summaries capped at 1000 runes
10. **No conversation leakage**: child conversation is not appended to parent; only the structured result and retention summary persist
11. **Enforced allow-list**: `allowed_tools` is enforced during child registry construction; only listed tools (minus `delegate`) are visible and executable by the child. All child exec registry tools are auto-approved within this enforced set.
12. **Specialized tool allowlists**: each specialized type enforces a per-type tool allowlist that is narrower than the global `allowed_tools`
