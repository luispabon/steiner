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

1. The main model decides to call `advisor`, optionally passing `question` (free text describing what to judge) and `files` (workspace paths to include verbatim).
2. The tool handler resolves and loads any `files` under the same path policy the `read` tool uses, then snapshots the live parent conversation from the current run context.
3. Steiner builds an advisor request with an advisor system prompt, the parent conversation snapshot, and a trailing user message containing the loaded files, the caller's question, and a short closing prompt asking for guidance.
4. The configured advisor model receives no tools and runs one non-streaming chat completion. Any `tool_use`/`tool_result` messages in the snapshot are flattened to plain text before sending, so the advisor request contains no structured tool content and requires no tool definitions.
5. The advisor's text is returned as the tool result and added to the parent conversation.

The conversation snapshot is sent after Steiner's normal context management with one transformation applied: `tool_use` blocks in assistant messages are rendered as `[tool_call: <name> <args-json>]` lines, and `tool_result` messages are converted to user messages prefixed with `[tool_result: <name>]`. All original content is preserved in text form. This keeps the advisor request provider-agnostic and eliminates the need for a matching `toolConfig`.

## Payload shaping

Before the advisor receives the conversation, Steiner applies several transformations to reduce token usage and preserve prompt caching:

**Reasoning and metadata stripping**: `ReasoningContent` and `ProviderMetadata` (including `ThinkingSignature`) are unconditionally cleared from every message reaching the advisor. This is a per-message transform with no position dependence, so it does not break cache reusability.

**Oversized tool-call arguments capping**: When tool calls in the snapshot contain large arguments (e.g. whole-file content from a `mutate` call), any string in the `Arguments` object longer than 1000 bytes is truncated to a 1000-byte prefix plus a size-preserving elision marker like `…[elided, N bytes total]`. This is a compile-time constant, not derived from conversation state, so it stays cache-safe. The transformation applies generically to any tool's arguments, not hardcoded to a specific tool. When the truncation point falls within a multi-byte UTF-8 character, JSON encoding will emit a U+FFFD replacement character at the cut point, which is deterministic and does not affect cache safety.

**Caller-supplied files and question**: `files` and `question` give a caller an explicit, bounded channel for artifacts the conversation snapshot doesn't reliably carry (e.g. tool-call arguments truncated by the 1000-byte cap above, or content dropped by a compaction that happened after it was written). They are rendered into a single trailing user message appended strictly *after* the flattened conversation snapshot, so the cached prefix (system prompt + snapshot) never shifts position regardless of what a caller passes — this mirrors the reasoning behind the 1000-byte tool-arg cap. Caps apply independently: up to 8 files, 32KB per file, 96KB aggregate across all files, and 4000 bytes for the question, each truncated with the same `…[elided, N bytes total]` marker when exceeded. Passing more than 8 files is a caller error and is rejected before the call is counted against the per-run budget. Each file is read through a reader limited to the 32KB cap rather than loaded in full first, so a path pointing at a very large file cannot force an unbounded read; the marker still reports the file's true on-disk size, obtained via a stat call rather than the capped read. Files are resolved with the same policy the `read` tool uses (a blocked-paths and special-file denylist — see the `read` tool's path policy for what "resolved" means; it does not add workspace containment beyond what `read` already allows). A bare `advisor` call with neither field behaves exactly as before this feature existed.

**Prior advisor notes reframing**: When a tool-result message's `Name` equals `advisor`, it is rendered with the framing "Your earlier note (update if circumstances have changed):" instead of the generic `[tool_result: advisor]` prefix. This presents the advisor's own revisable prior opinion rather than an authoritative observation. *(This is scope creep beyond issue #380, a quality fix not a token or cache optimization.)*

**Stable prompt cache key across turns**: `BuildDelegateRegistry` resolves the advisor's prompt cache key from the process-lifetime `CacheKeyStore` under a synthetic slot (`"advisor"`) that is deliberately not a delegation `AgentType` — it never appears in `AllAgentTypes()` or the delegation tool allowlist, so the advisor stays ineligible for the parallel-tool dispatch path. This makes the key stable across every advisor call in the process, not just within one run, matching how sub-agent delegations reuse a key per `AgentType`. `advisor.HandlerDeps.CacheKey` carries the resolved key into `NewHandler`; when the store is nil or produces no key (e.g. an entropy error, or the package is used standalone in tests), `NewHandler` mints a fresh key itself so the advisor still works, just without cross-call reuse. On Codex, a stable key triggers session-id/thread-id shard-affinity headers that route requests to the same cache shard, improving cache hit rates. Anthropic's wire mapping never reads `PromptCacheKey` — that field is consumed only by the Responses-shaped wires (OpenAI-compatible and Codex) — so on Anthropic providers the stable key has no effect; Anthropic caching is driven entirely by `cache_control` breakpoints, described next.

**Anthropic cache TTL and breakpoint redistribution**: Advisor requests set `ChatRequest.AdvisorCacheProfile`, which opts the Anthropic wire into a profile tuned for the advisor's call pattern instead of the default rolling-conversation placement. Every breakpoint carries an extended `ttl: "1h"` instead of the provider's default 5-minute ephemeral window, since advisor calls are typically spaced further apart than 5 minutes. Breakpoint placement is also redistributed: the default placement spends one of its four breakpoints on the final message, but for the advisor that final message is the per-call unique suffix (files + question + closing prompt) and can never be read back, so that breakpoint is wasted. Instead, after marking the last system block, the advisor profile walks backward from the end of the reusable conversation tail (excluding the final message) and places a breakpoint every 15 content blocks, spending the remaining budget of up to four breakpoints total. Anthropic's cache lookup walks backward at most 20 content blocks from a breakpoint to find a prior cached entry, so 15-block spacing keeps consecutive breakpoints within reach of each other across calls. With up to four breakpoints, this covers roughly 45–50 blocks of tail, i.e. on the order of 15 agent turns at the roughly `1 + tools_per_turn` blocks appended per turn. This lets a later advisor call chain its breakpoint back to an earlier call's cached entry when the two calls are within that range; wider gaps between advisor calls degrade gracefully back to a full cache write, matching today's behaviour. This profile is opt-in and does not change breakpoint placement or TTL for the main agent or sub-agent delegation requests.

## Configuration

The advisor is disabled by default.

```yaml
advisor:
  enabled: true
  max_uses_per_run: 2
  max_tokens: 256
  timeout: 5m

models:
  definitions:
    advisor-model:
      provider: local
      id: advisor-model
  profiles:
    default:
      default_model: advisor-model
      advisor: advisor-model
```

Fields:

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enables the advisor tool and advisor prompt steering. |
| `max_uses_per_run` | `3` | Per-session call cap, enforced across every turn in the process (see [Tool behavior](#tool-behavior)). Required to be at least `1` when enabled. |
| `max_tokens` | `nil` | Optional output-token limit forwarded to the advisor provider request. |
| `timeout` | `180s` | Optional HTTP timeout override applied only to advisor calls. Overrides `providers.<name>.timeout` for the advisor's provider only, leaving the main chat model and other models unaffected. The default `180s` is higher than the typical 30s provider default because advisor calls send a large parent-conversation prompt. |

The advisor model is assigned in the selected profile's `models.profiles.<name>.advisor` field. When omitted or empty, it falls back to that profile's `default_model`. Model references use the same model configuration and provider discovery path as other runtime models.

## Tool behavior

The model-facing tool is named `advisor`. Its schema accepts two optional properties: `question` (free text describing what to judge) and `files` (an array of workspace paths to include verbatim, since the advisor has no tool access and cannot read files itself). Both are optional; a bare call with neither behaves as a pure timing signal over the live conversation, same as before.

The handler keeps a use counter in `advisor.SharedState`, a process-lifetime singleton threaded through `delegation.DelegateDeps.AdvisorState` the same way `CacheKeyStore` is (see the cache-key paragraph above). Because `BuildDelegateRegistry` runs once per turn and builds a fresh advisor handler each time, the counter would reset every turn if it lived on the handler alone; sharing `AdvisorState` across those per-turn handlers is what makes `max_uses_per_run` a true per-session cap instead of a per-turn one. Calls within `max_uses_per_run` invoke the advisor model. Calls after the cap return:

```text
advisor budget exhausted for this session (N/N); proceed on your own judgment
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
