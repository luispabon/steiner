# Advisor sub-agent internals

User guide: [Advisor sub-agent](../user/advisor.md).

## Request flow

The `advisor` handler (`internal/advisor/tool.go`) loads the optional `question` and `files` arguments, snapshots the live parent conversation from the run context, and sends one request. The model receives no tools. The request holds an advisor system prompt, the flattened snapshot, and one trailing user message with the files, the question, and a request for guidance.

Without reasoning, the call is a non-streaming chat completion. When the request sets reasoning, it streams. A non-streaming call that fails with a stream-required error is retried as a stream ([stream-required retry](architecture.md#stream-required-retry)).

Tool calls and tool results in the snapshot are flattened to text. A tool call becomes `[tool_call: <name> <args-json>]`, and a tool result becomes a user message prefixed `[tool_result: <name>]`. Results from the advisor tool use a separate note prefix that marks the opinion as revisable.

## Payload shaping

- Reasoning content is cleared from every message.
- Tool-call argument strings over 1000 bytes (`maxToolArgStringLen`) are cut to a 1000-byte prefix followed by `…[elided, N bytes total]`.
- Caller files are capped at 8 files, 32 KB each, and 96 KB in total. The question is capped at 4000 bytes. A ninth file is rejected before the call counts against the budget.
- `dedupeAgainstSnapshot` (`internal/advisor/files.go`) drops a file's content when the snapshot already holds a full `read` of it with a matching `file_hash`. Files over `maxAdvisorFileBytes` are never deduped.
- Files and question go in one trailing user message after the snapshot, so the system prompt and snapshot prefix do not change between calls.

## Budget

The use counter is an `advisor.SharedState` allocated once per runtime (`cliRuntime.advisorState` in `cmd/steiner/runtime_build.go`). `BuildDelegateRegistry` builds a new handler each turn, so the shared counter makes `Config.MaxUsesPerRun` a runtime-lifetime cap across turns and conversations in the process. Calls past the cap return the budget-exhausted result, and the tool stays registered. Child agents get their own counts from `AdvisorBudgetStore`, keyed by agent ID.

## Transport and cache

Advisor calls use transport session `<session>-advisor` with the parent session set to `<session>` (`internal/delegation/registry.go`). The cache key comes from the process-lifetime `CacheKeyStore`, as described in [cache identity](prompt-caching.md#cache-identity). If the store is nil or gives no key, the handler mints a fresh key. On Codex, a stable key sends affinity headers. Anthropic ignores the key and relies on breakpoints.

Advisor requests set `ChatRequest.AdvisorCacheProfile`. The Anthropic wire gives these breakpoints a one-hour TTL (`anthropicAdvisorCacheTTL`). It marks the last system block, then places breakpoints every 15 content blocks (`anthropicAdvisorBreakpointSpacing`) walking backward through the reusable tail, excluding the final message, within the four-breakpoint limit (`anthropicMaxCacheBreakpoints`). Main-agent and delegation profiles are unchanged.
