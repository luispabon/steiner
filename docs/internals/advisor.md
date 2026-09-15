# Advisor sub-agent internals

User-facing documentation: [Advisor sub-agent](../user/advisor.md).

## Request flow

The main model calls `advisor` with optional `question` and `files` fields. The handler resolves and loads files under the same path policy as `read`, snapshots the live parent conversation from the current run context, and builds one advisor request containing:

1. an advisor system prompt;
2. the parent conversation snapshot; and
3. a trailing user message with loaded files, the caller's question, and a closing request for guidance.

The configured model receives no tools and runs one non-streaming chat completion. `tool_use` and `tool_result` messages in the snapshot are flattened to text before sending. Assistant tool calls render as `[tool_call: <name> <args-json>]`; tool results become user messages prefixed with `[tool_result: <name>]`. Original content is preserved in text form. This keeps the request provider-agnostic and removes the need for a matching `toolConfig`.

When a tool-result message has `Name == advisor`, it is rendered as `Your earlier note (update if circumstances have changed):` rather than the generic tool-result prefix. This presents an earlier opinion as revisable.

## Payload shaping

Before the request is sent, Steiner applies deterministic transformations:

- `ReasoningContent` and `ProviderMetadata` (including `ThinkingSignature`) are cleared from every message.
- Strings in tool-call `Arguments` longer than 1000 bytes are truncated to a 1000-byte prefix plus a marker such as `…[elided, N bytes total]`. The cap is generic, compile-time, and cache-safe. A cut inside a UTF-8 character is encoded as U+FFFD deterministically.
- At most 8 caller files are accepted, with a 32KB cap per file, a 96KB aggregate cap, and a 4000-byte question cap. Each uses the same elision marker. More than 8 files is rejected before the call counts against the budget.
- Files are read through a reader limited to 32KB. Their true on-disk size comes from a stat call so an oversized file cannot force an unbounded read. Resolution uses the `read` tool's blocked-path and special-file policy and does not add workspace containment.
- Files and question are placed in one trailing user message after the flattened snapshot. The system prompt and snapshot prefix therefore do not move between calls.

A bare call with neither field remains the pre-file-context behavior.

## Cache profile and keys

`BuildDelegateRegistry` resolves the advisor prompt cache key from the process-lifetime `CacheKeyStore` under synthetic slot `"advisor"`. The slot is deliberately not a delegation `AgentType`, so it does not appear in `AllAgentTypes()` or the delegation allowlist and cannot enter parallel-tool dispatch. `advisor.HandlerDeps.CacheKey` carries the key into `NewHandler`.

If the store is nil or produces no key, `NewHandler` mints a fresh key. The advisor still works, without cross-call reuse. On Codex, a stable key triggers session-id/thread-id shard-affinity headers. Anthropic does not read `PromptCacheKey`; its caching is driven by wire-level `cache_control` breakpoints.

Advisor requests set `ChatRequest.AdvisorCacheProfile`. The Anthropic wire uses this profile to give breakpoints a one-hour TTL instead of the default five-minute ephemeral window. It marks the last system block, then walks backward through the reusable conversation tail, excluding the unique final message, placing breakpoints every 15 content blocks, with up to four total. Anthropic searches back at most 20 content blocks from a breakpoint, so this spacing keeps consecutive breakpoints reachable across calls. The main-agent and delegation profiles are unchanged.

## Per-session state

`advisor.SharedState` carries the use counter through `delegation.DelegateDeps.AdvisorState`. `BuildDelegateRegistry` creates a fresh handler each turn, so keeping the counter in shared state makes `max_uses_per_run` a session cap rather than a turn cap. The handler returns the budget-exhausted result after the cap and leaves the tool definition registered.

## Package map

| Area | Responsibility |
|------|----------------|
| `internal/advisor` | Prompt construction, provider call, tool definition, and cap handler |
| `internal/agent` | Parent conversation snapshot in context |
| `cmd/steiner` | Model resolution, provider wiring, and registration |
| `internal/prompt` | Base advisor steering preamble when enabled |
| `internal/output` | Advisor lifecycle event types and rendering |
| `internal/tui` | Advisor lifecycle display |
| `internal/config` | Config loading, patching, defaults, and validation |
