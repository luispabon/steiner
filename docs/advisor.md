# Advisor sub-agent

`advisor` is Steiner's optional stronger-model steering tool. It lets the main agent ask a configured advisor model for planning and risk guidance without handing work to a child agent.

Despite the filename, the advisor is not a sub-agent in the delegation sense. It does not run a child loop, does not receive a tool registry, does not mutate files, and does not maintain its own session. It is a single read-only model call over the parent conversation.

## Purpose

Use the advisor when a stronger model can improve the main loop's judgment:

- before committing to an implementation or review approach
- when requirements are ambiguous or tradeoffs matter
- after verification failures when the next fix path is unclear
- before declaring a plan, implementation, or review complete

The advisor is for strategic steering. It is not a code executor, verifier, reviewer of hidden child-agent work, or replacement for local tests.

## How it works

1. The main model decides to call `advisor`.
2. The tool handler snapshots the live parent conversation from the current run context.
3. Steiner builds an advisor request with an advisor system prompt, the parent conversation snapshot, and a short user prompt asking for guidance.
4. The configured advisor model receives no tools and runs one non-streaming chat completion. Any `tool_use`/`tool_result` messages in the snapshot are flattened to plain text before sending, so the advisor request contains no structured tool content and requires no tool definitions.
5. The advisor's text is returned as the tool result and added to the parent conversation.

The conversation snapshot is sent after Steiner's normal context management with one transformation applied: `tool_use` blocks in assistant messages are rendered as `[tool_call: <name> <args-json>]` lines, and `tool_result` messages are converted to user messages prefixed with `[tool_result: <name>]`. All original content is preserved in text form. This keeps the advisor request provider-agnostic and eliminates the need for a matching `toolConfig`.

## Payload shaping

Before the advisor receives the conversation, Steiner applies several transformations to reduce token usage and preserve prompt caching:

**Reasoning and metadata stripping**: `ReasoningContent` and `ProviderMetadata` (including `ThinkingSignature`) are unconditionally cleared from every message reaching the advisor. This is a per-message transform with no position dependence, so it does not break cache reusability.

**Oversized tool-call arguments capping**: When tool calls in the snapshot contain large arguments (e.g. whole-file content from a `mutate` call), any string in the `Arguments` object longer than 1000 characters is truncated to a 1000-character prefix plus a size-preserving elision marker like `"...[elided, N bytes total]"`. This is a compile-time constant, not derived from conversation state, so it stays cache-safe. The transformation applies generically to any tool's arguments, not hardcoded to a specific tool.

**Prior advisor notes reframing**: When a tool-result message's `Name` equals `advisor`, it is rendered with the framing "Your earlier note (update if circumstances have changed):" instead of the generic `[tool_result: advisor]` prefix. This presents the advisor's own revisable prior opinion rather than an authoritative observation. *(This is scope creep beyond issue #380, a quality fix not a token or cache optimization.)*

**Stable per-run prompt cache key**: Each advisor handler generates one stable prompt cache key via `provider.NewPromptCacheKey()` in `NewHandler`, reused across every advisor call in that run. This lets call N+1 read call N's cached prefix. On Codex, this triggers session-id/thread-id shard-affinity headers that route requests to the same cache shard, improving cache hit rates. Empty key on entropy error disables caching without failing the call. On Anthropic providers, the same stable key may improve hit rates through the provider's ephemeral cache window (5 minutes), though this benefit is not yet measured as of this writing.

## Configuration

The advisor is disabled by default.

```yaml
advisor:
  enabled: true
  model: advisor-model
  max_uses_per_run: 2
  max_tokens: 256
  timeout: 5m
```

Fields:

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enables the advisor tool and advisor prompt steering. |
| `model` | `""` | Model alias used for advisor calls. Required when enabled, and must exist in `models`. |
| `max_uses_per_run` | `3` | Per-run call cap. Required to be at least `1` when enabled. |
| `max_tokens` | `nil` | Optional output-token limit forwarded to the advisor provider request. |
| `timeout` | `180s` | Optional HTTP timeout override applied only to advisor calls. Overrides `providers.<name>.timeout` for the advisor's provider only, leaving the main chat model and other models unaffected. The default `180s` is higher than the typical 30s provider default because advisor calls send a large parent-conversation prompt. |

The advisor model is resolved through the same model configuration and provider discovery path as other runtime models.

## Tool behavior

The model-facing tool is named `advisor`. Its schema is an empty object; the call is a timing signal rather than a request with meaningful parameters.

The handler keeps a per-run use counter. Calls within `max_uses_per_run` invoke the advisor model. Calls after the cap return:

```text
advisor budget exhausted for this run (N/N); proceed on your own judgment
```

Steiner deliberately keeps the advisor tool definition registered for the whole run instead of removing it when the cap is reached. The cap is enforced inside the handler so provider-visible tool schemas stay stable and prompt-cache prefixes can remain reusable.

## Relationship to delegation

Advisor and delegation solve different problems.

| Capability | Advisor | Delegation |
|------------|---------|------------|
| Execution shape | One model call | Child agent loop |
| Context | Live parent conversation | Explicitly passed task and context |
| Tools | None | Per-agent allowlist |
| Mutation | Never | Only for mutation-capable agents such as `code` |
| Parent transcript impact | Advisor result is appended | Only bounded child result summary is appended |
| Best use | Steering, tradeoffs, residual risk | Exploration, implementation, verification, research |

The advisor cannot see a delegate's private internal transcript unless the delegate result summary is already present in the parent conversation.

## Events and UI

Advisor calls emit structured lifecycle events:

| Event | Meaning |
|-------|---------|
| `advisor_started` | Advisor model call is starting. |
| `advisor_complete` | Advisor model call completed or failed. |
| `advisor_budget_exhausted` | The per-run cap was reached and no provider call was made. |

The TUI renders advisor activity as a collapsible advisor panel with model and use-count metadata.

## Implementation map

| Area | Responsibility |
|------|----------------|
| `internal/advisor` | Advisor prompt construction, provider call, tool definition, per-run cap handler |
| `internal/agent` | Per-tool-call parent conversation snapshot in context |
| `cmd/steiner` | Advisor model resolution, provider wiring, tool registration |
| `internal/prompt` | Base advisor steering preamble when enabled |
| `internal/output` | Advisor lifecycle event types and rendering |
| `internal/tui` | Advisor lifecycle display |
| `internal/config` | `advisor` config loading, patching, defaults, validation |

## Safety and limits

- Advisor is opt-in and disabled by default.
- Advisor has no tool access and cannot mutate local state.
- Advisor guidance is advisory only; the main agent must adapt it to local evidence.
- The per-run cap limits cost and latency.
- Provider credentials are still those configured for the selected advisor model; do not hardcode secrets in config.
