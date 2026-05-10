# Delegation

## Summary

Steiner supports spawning isolated sub-agents via a built-in `delegate` tool. The parent agent calls `delegate` with a task description; steiner bootstraps a child agent loop with the same provider, an auto-approved subset of tools (minus `delegate` itself), and tighter resource limits. The child runs to completion, produces a summarised result, and returns structured output to the parent's conversation. Children cannot nest further, cannot prompt the user, and share no mutable state with the parent beyond the working directory filesystem.

Key design properties:

- **Bounded**: children have hard turn/token/timeout limits derived from `SubAgentConfig` with tighten-only overrides.
- **Isolated**: children receive only explicitly passed context (task + optional context string). No access to the parent's conversation history.
- **Non-recursive**: the `delegate` tool is excluded from child registries; `AllowNesting` defaults to `false`.
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
| `internal/delegation` | Contract types, tool definition, handler, bootstrapping, spawn logic, limits, result building |
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
| `timeout` | string | no | Duration string, e.g. `"30s"` |

### Approval mode

The delegate tool itself is registered with `ApprovalModeAuto` — no user confirmation needed to spawn a child.

---

## Bootstrapping a Child Run

`delegation.BuildChildRun()` assembles the full `agent.RunRequest`:

### 1. Derive limits

`deriveChildLimits()` combines `SubAgentConfig` defaults with spec-level overrides using **tighten-only semantics**: an override is applied only when it is more restrictive than the configured default.

Defaults (from `config.SubAgentConfig`):
- `MaxTurns`: 15
- `MaxTokens`: 100,000
- `Timeout`: 0 (no timeout)

### 2. Build child prompt

The child prompt is minimal:
- **System prompt**: either the caller-provided `system_prompt` or `"You are a sub-agent. Complete the task given to you."`
- **Conversation**: a single user message containing the task (+ context if provided)

The system prompt is passed via `PromptOverrides` so the provider sees exactly one system message.

### 3. Build child registries

Two registries are built from the parent:

1. **Visible registry** — what the model can see and request (parent tools minus `delegate`)
2. **Execution registry** — same tools but with all approval modes forced to `ApprovalModeAuto`

This ensures children cannot delegate further and never block on approval.

### 4. Assemble RunRequest

The request includes:
- The parent's provider instance (same model, same endpoint)
- A `tool.NewExecutor` wrapping the execution registry with auto-approval config
- `ExtraParams` and `Thinking` config propagated from the parent's model config
- No `ContextManager` — children don't compact
- No `Model` field — inherits from the active provider

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

---

## Result and Retention

### DelegationResult

```go
type DelegationResult struct {
    AgentID    string           // matches the request
    Status     DelegationStatus // complete|failed|cancelled
    Output     string           // last assistant message content
    Summary    string           // retained summary (≤1000 runes)
    TurnCount  int
    TokenCount int
    Error      string           // populated on failure
}
```

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
  max_tokens: 100000     # default token budget per child
  allowed_tools:         # which tools children can use
    - read
    - glob
    - grep
    - ls
    - write
    - edit
    - bash
  allow_nesting: false   # children cannot delegate further
  max_concurrent: 1      # concurrency limit (for future use)
```

---

## System Prompt Integration

When `DelegationEnabled` is true in prompt assembly options, `prompt.SystemPreamble()` prepends the `delegationInstructions` block to the system prompt. This provides the model with:

- Classification heuristics (when to delegate vs work locally)
- Task categories suited for delegation (investigation, research, implementation, verification, review)
- Guidance on what makes a good delegation (self-contained, paths/constraints, success criteria)
- Explicit constraints (sub-agents cannot delegate or ask the user)

---

## Event Lifecycle

Events emitted during delegation (via `output.EventSink`):

| Event | When | Key fields |
|-------|------|------------|
| `delegation_started` | Before child run begins | `agent_id`, `task_preview` |
| `delegation_extension` | Each auto-extension iteration | `agent_id`, `extension`, `max_extensions` |
| `delegation_complete` | After summarisation, on success | `agent_id`, `status`, `turn_count`, `token_count`, `output` |
| `delegation_failed` | On child error | `agent_id`, `task_preview`, `error` |

The TUI renders these with a spinner during execution, lifecycle state labels, and collapsible output panels for completed delegations.

---

## Constraints and Invariants

1. **No nesting**: children never have access to the `delegate` tool
2. **No user interaction**: children cannot prompt the user; all tools are auto-approved
3. **No context manager**: children don't perform compaction or masking internally
4. **Tighten-only overrides**: caller cannot exceed configured limits, only reduce them
5. **Single provider**: children use the same provider/model instance as the parent
6. **Filesystem shared**: children operate in the same `WorkDir` — concurrent filesystem mutation is the caller's responsibility
7. **Extension cap**: maximum 5 auto-extensions to prevent runaway children
8. **Summary cap**: retention summaries capped at 1000 runes
9. **No conversation leakage**: child conversation is not appended to parent; only the structured result and retention summary persist
