# Advisor sub-agent internals

User guide: [Advisor sub-agent](../user/advisor.md).

## Request flow

The `advisor` handler (`internal/advisor/tool.go`) loads the optional `question` and `files` arguments, snapshots the live parent conversation from the run context, and sends one request. The model receives no tools. The request holds an advisor system prompt, the flattened snapshot, and one trailing user message that carries the in-flight parent message, the files, the question, and a request for guidance.

Without reasoning, the call is a non-streaming chat completion. When the request sets reasoning, it streams. A non-streaming call that fails with a stream-required error is retried as a stream ([stream-required retry](architecture.md#stream-required-retry)).

Tool calls and tool results in the snapshot are flattened to text. A tool call becomes `[tool_call: <name> <args-json>]`, and a tool result becomes a user message prefixed `[tool_result: <name>]`. Results from the advisor tool use a separate note prefix that marks the opinion as revisable.

## Payload shaping

- Reasoning content is cleared from every message.
- Tool-call argument strings over 1000 bytes (`maxToolArgStringLen`) are cut to a 1000-byte prefix followed by `…[elided, N bytes total]`.
- Caller files are capped at 8 files, 32 KB each, and 96 KB in total. The question is capped at 4000 bytes. A ninth file is rejected before the call counts against the budget.
- `dedupeAgainstSnapshot` (`internal/advisor/files.go`) drops a file's content when the snapshot already holds a full `read` of it with a matching `file_hash`. Files over `maxAdvisorFileBytes` are never deduped.
- Files and question go in one trailing user message after the snapshot, so the system prompt and snapshot prefix do not change between calls.
- `buildMessages` (`internal/advisor/prompt.go`) takes a trailing assistant message out of the transcript and renders its text at the head of that final user message under `inFlightLabel`. After replay sanitising, a trailing assistant message can only be the message that issued this advisor call and is still waiting for its tool result. It carries no result while its own call runs but gains a `[tool_call: advisor ...]` line once that result exists, so it is kept out of the transcript to make the cached prefix end on a message whose rendering never changes between calls. A trailing assistant message with empty text is dropped without a label, and a trailing non-assistant message stays in the transcript.

## Context reduction

`reduceAdvisorMessages` (`internal/advisor/reduction.go`) runs before the provider call. Its trigger is `Config.ReductionTriggerTokens`, defaulted to `32000` by `internal/config` and disabled by `0`. The effective trigger is lowered to `floor(0.6 * (contextWindow - outputReserve - estimatorPad))` when the advisor model reports a context window and that budget is positive. The output reserve is the configured `advisor.max_tokens`, then the model's `MaxOutputTokens`, then `provider.DefaultMaxOutputTokens`. The target is `floor(0.75 * trigger)` and the protected recent tool tail is `ceil(0.25 * trigger)`.

The initial estimate is one `provider.EstimateChatRequestTokenEstimate` call over the complete flattened request, using the advisor model. Estimator failure falls back to `ceil(request bytes / 3)`, including image payload bytes, and never blocks the call. The same estimate supplies a token per content byte ratio used to convert planned byte savings and to size the protected tail, so candidates are not tokenized. A final estimate after a committed plan feeds diagnostics only.

Frozen replacements live in a per-conversation ledger on `SharedState`, keyed by cache key, transport and parent session, model identity, trigger, and output reserve, with an eight-scope LRU. Each entry stores the source message hash and, only for changed messages, the replacement text and stage. Replay matches the source transcript by hash and applies replacements up to the first mismatch. Appends therefore replay the exact earlier replacements. A mismatch truncates the ledger from that index, and index zero clears it, which resets the ledger after compaction. The same call can replay a frozen ledger below the trigger; it just does not plan a new batch.

The in-flight trailing assistant message is excluded from ledger identity because `buildMessages` moves it to the uncached suffix. The caller's question and files are also excluded because they are unique to each call.

Planning classifies tool results before flattening by resolving assistant tool-call IDs to names, falling back to the message Name. A result is protected when either source names `sub_agent` or `follow_up`, or when both sources are empty. User, system, assistant, and protected tool messages are never changed. Stages run oldest first and stop at the target:

1. Exact duplicate full reads: same path and byte-identical output, full range, no pagination, image, or line truncation. The earlier copy stays and the later eligible copy becomes a note. File hashes are not used because they normalize whitespace.
2. Head and tail clips of about 2 KB with a valid UTF-8 boundary and an omitted-byte marker. Structured `read` and `bash` results change only `Output` and are re-marshaled with every other field intact; other tool output is clipped as raw text.
3. Stubs for the oldest remaining results, naming the tool and call and telling the caller to re-read or re-run if the details matter.

A new batch is committed only when its proposed savings reach `ceil(0.1 * trigger)`. Otherwise the proposed changes are discarded and the ledger records exhaustion at the current estimated size; planning retries only after the request grows by another `ceil(0.1 * trigger)`. Ledger reads copy state, planning runs without a lock, and commit is a short compare-and-swap on the ledger epoch. Deterministic inputs produce identical plans.

Requested files are counted at their full bounded size while planning. After a plan is frozen, `dedupeAttachments` re-dedupes them only against untouched full reads in the reduced transcript, so a file reappears when the read that seeded its dedupe was reduced. The `advisor_complete` event carries optional `reduction` diagnostics: strategy, estimator, before and after token estimates, and the stages applied.

## Budget

The use counter is an `advisor.SharedState` allocated once per runtime (`cliRuntime.advisorState` in `cmd/steiner/runtime_build.go`). `BuildDelegateRegistry` builds a new handler each turn, so the shared counter makes `Config.MaxUsesPerRun` a runtime-lifetime cap across turns and conversations in the process. Calls past the cap return the budget-exhausted result, and the tool stays registered. Child agents get their own counts from `AdvisorBudgetStore`, keyed by agent ID.

## Transport and cache

Advisor calls use transport session `<session>-advisor` with the parent session set to `<session>` (`internal/delegation/registry.go`). Sub-agent advisors share `<session>-subagent-advisor`, with the parent session still `<session>`, so a sub-agent advisor call never shares or retires the parent advisor's pooled transport process. The cache key comes from the process-lifetime `CacheKeyStore`, as described in [cache identity](prompt-caching.md#cache-identity). If the store is nil or gives no key, the handler mints a fresh key. On Codex, a stable key sends affinity headers. Anthropic ignores the key and relies on breakpoints.

Advisor requests set `ChatRequest.AdvisorCacheProfile`. The Anthropic wire gives these breakpoints a one-hour TTL (`anthropicAdvisorCacheTTL`). It marks the last system block, then places breakpoints every 15 content blocks (`anthropicAdvisorBreakpointSpacing`) walking backward through the reusable tail, excluding the final message, within the four-breakpoint limit (`anthropicMaxCacheBreakpoints`). Main-agent and delegation profiles are unchanged.

The advisor provider is resolved once when the registry is built (`newAdvisorRuntime`) and shared by every advisor call that registry serves. `adviseWithMessages` releases that provider's transport connection when the call returns (`provider.ReleaseConnection`), so a factory-built Codex WebSocket provider does not keep a socket and keepalive between calls. Release is non-terminal: the next call redials with the same affinity headers ([Codex WebSocket connections](prompt-caching.md#uncached-codex-websocket-connections)).
