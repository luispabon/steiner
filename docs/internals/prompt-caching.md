# Prompt caching internals

User guide: [Cache Hit Rate Tracking](../user/cache-stats.md).

## Cache identity

`provider.NewPromptCacheKey` returns 16 random bytes, hex encoded. If generation fails, the key stays empty and no key or affinity header is sent.

Parent runs take their key from the runner (`cliRunner.promptCacheKey` in `cmd/steiner`), and interactive sessions persist theirs. Delegated children get a key from `cacheKeyOrMint` in `internal/delegation`, keyed by agent type. A nil store or a failed mint gives a fresh key on each call. The advisor uses the same store under the slot `advisor`, which is not an agent type, so it stays out of `AllAgentTypes()`. Compaction escalation and turn progression reuse the run's `PromptCacheKey` and transport session fields. A compaction fallback target uses derived values instead ([compaction fallback identities](#compaction-fallback-identities)).

Per provider:

- Codex Responses sends `prompt_cache_key` in the body and the affinity headers below.
- The OpenAI chat wire sends `prompt_cache_key` only for provider type `openai`, and `prompt_cache_retention: "24h"` only for models that support extended retention.
- Anthropic does not read `PromptCacheKey`. `assignCacheBreakpoints` (`internal/provider/anthropic_wire.go`) places at most four `cache_control` breakpoints: the last system block (or the last tool when there is no system block), the final non-volatile message block, and the last block of the second-to-last user message. Blocks at or after the first volatile block, which holds image data stripped after one request, are never marked. Advisor-shaped requests use a different placement ([advisor internals](advisor.md#transport-and-cache)).

OpenCode-compatible routing applies only when the wire configures an `X-Opencode-Session` header. `prepareTransportSessionHeaders` (`internal/provider/openai_compat.go`) sends a non-empty `TransportSession` in that header and `ParentTransportSession` as `X-Parent-Session-Id`. The OpenAI chat and Anthropic wires call it. Codex Responses does not. Child agents use `<session>-<agentID>`, and advisor calls use `<session>-advisor` with the session as parent (`internal/delegation/registry.go`).

### Compaction fallback identities

`compactionFallbackRequest` (`internal/agent/compaction_fallback.go`) gives a fallback summary identities derived from the run's values. `compactionFallbackIdentity` hashes length-prefixed main value, `compaction-fallback` label, provider alias, and backend model ID fields with SHA-256, and keeps the first 16 bytes as 32 hex characters. The result becomes the request's `PromptCacheKey`. It becomes the request's `TransportSession` when the main transport session is set. An empty main `PromptCacheKey` stays empty. An empty main transport session gets a new `provider.NewPromptCacheKey` value once per fallback compaction operation, and that operation's summary calls share it. When the main `PromptCacheKey` is non-empty, each target's key is separate from the main run's and from other targets'.

The fallback request sets `CacheBaseline` to nil. Its call neither compares against nor advances the main prefix baseline (`computeRequestCacheStats`, `promoteRequestCacheStats`).

## Codex affinity headers

When the key is non-empty, Codex Responses requests set `session-id` and `thread-id` to the key and `originator` to `codex_cli_rs`. `(*responsesWire).HTTPRequest` sets them in `wire_responses.go`, and the WebSocket dial in `codex_ws_conn.go` sets the same three headers. The headers pin one conversation to one backend shard.

Codex rejects `prompt_cache_retention`, so `responsesWire` never sends it. A Codex replica also rejects that field with an OpenAI-shaped error. `isCodexPromptCacheRetentionRejection` matches that error, and the retry drops the cache key, so the retried request sends no affinity headers.

## Request pacing

`codex.min_request_interval` sets the minimum gap between Codex request starts. The default is `0s`, which turns pacing off. `Client.pace` returns at once when the interval is zero or less. Otherwise it holds the client mutex while it waits until the interval has passed since the last request start, then records the new start. Concurrent calls on one client queue behind that wait. Pacing changes timing only. It does not change keys or headers.

## Codex transport

Transport selection and fallback are in [Codex transport and pacing](../user/cache-stats.md#codex-transport-and-pacing). The WebSocket endpoint and beta header are marked unconfirmed in `codex_ws_protocol.go`, so WebSocket stays opt-in. Validation rejects `auto` and other unknown values.

## Codex conversation replay

Codex replay preserves assistant message boundaries and per-message `phase` (`commentary` or `final_answer`), along with message and function-call order, in provider-native metadata on session messages. Distinct non-empty message items are joined by at least one blank line in flattened content. Streamed text is reconciled with completed output to recover missing text without duplication. Phase preservation does not guarantee narration before tool calls.

## Usage fields

`openAIUsage.toUsageStats` in `openai_wire.go` reads cache hits by presence. `prompt_tokens_details.cached_tokens` wins over `prompt_cache_hit_tokens`, which wins over top-level `cached_tokens`, and a present zero counts. `cache_write_tokens` maps to cache creation tokens. Providers that omit it report zero.

`prompt_tokens` is the raw total including cached input. Diagnostics derive the non-cached count as `prompt_tokens - cache_read_tokens - cache_create_tokens`, floored at zero (`scripts/diagnostics.mjs`).

## Usage statistics storage

The global store is `$XDG_STATE_HOME/steiner/cache-stats.json`, or `~/.local/state/steiner/cache-stats.json` when `XDG_STATE_HOME` is unset. Schema version 2 stores hourly entries keyed by provider alias, provider type, model, hour, and source (`parent`, `sub_agent`, or `advisor`). Entries are retained for eight days (`retentionDays`). Version 1 entries decode with source `SourceUnknown`, not parent. Missing, corrupt, or unknown-version files load as empty.

Persistence is asynchronous. `Recorder.Record` adds a delta to a pending map keyed by `bucketKey`; every delta field is additive (request count and token sums), so entries for one key are summed rather than replaced. A `persistWriter` goroutine (`internal/usagestats/persist.go`) swaps the pending map for a fresh one and calls `store.writeBatch` once per batch, which takes the exclusive `flock` on the sibling `cache-stats.json.lock` (mode `0600`), rereads the file, merges every key as an additive delta, and rewrites it with `fsatomic.WriteFile`. The lock is a separate file because the data file is replaced by rename, which changes its inode. Platforms without `flock` skip persistence rather than write unlocked.

`Drain(ctx)` waits for the pending and in-flight batch; `Close()` stops the writer, drops anything still pending, and wakes a waiting `Drain`. Both are idempotent. After `Close` no batch is claimed, so no new write starts; a batch already claimed may still reach the store. `persistWriter.abandon` logs the count of dropped buckets, and `persistWriter.add` drops and logs any delta that arrives after `Close`, so a post-Close observation never reaches the store. A write blocked on the lock cannot be cancelled: an expired `Drain` reports an error while the writer stays blocked until the lock is released or the process exits, and the queued deltas are lost. While another process holds the `flock`, every new delta queues in the pending map instead of being written, so the file lags the session and the window of data a crash could lose grows until the lock is released. Write and drop failures are logged outside the writer mutex. `cmd/steiner` `closeRuntime` drains with its own 5 second deadline (`usageStatsDrainTimeout`, independent of the quota drain's budget) after delegation shutdown, then closes the recorder.

In-session readers (`Recorder.Window`, `SessionReport`, `SessionReportFor`) read the in-memory buckets that `Record` updates, so they never wait on persistence and always include the current session's observations. A separate process, or a new `Recorder`, sees only flushed data. The `STEINER_USAGE_TELEMETRY` JSONL writer is separate and still synchronous.

## Telemetry

`STEINER_USAGE_TELEMETRY` names a JSONL file for usage records, and `STEINER_USAGE_TELEMETRY_RUN` sets the run ID. Each line carries `seq`, a process-wide counter incremented under the writer mutex, so file order matches `seq` order.

## Cache diagnostics

Each usage-bearing model response writes one `cache` record (`internal/agent/cache_diagnostics.go`) with model identity and token counts, plus:

- `cold_start`: true on the first usage-bearing record a process writes. A process has one run ID, so this is once per run. Fallback compaction calls write `false` and leave the latch unchanged.
- `prefix_comparison_enabled` and `prefix_predecessor_known`: whether prefix comparison ran and whether a predecessor existed. The predecessor is the last accepted request under the same `CacheBaselineKey` (`internal/agent/cache_baseline.go`), which holds the cache key, agent ID, configured and effective provider type, effective transport, provider alias, and backend model ID. An empty cache key disables baselines.
- `shared_prefix_messages`: leading messages shared with the last accepted request. It separates a pure append from a prefix rewrite.
- `prefix_hash`: a cumulative 8-hex digest folded over one 8-hex hash per message. Each per-message digest is over the `provider.AppendMessageHashInput` bytes, appended into one reused buffer per request: role, content, reasoning content, tool-call names and arguments, and image media types and data. It hashes logical messages, not wire JSON.
- `cache_key_hash`: an 8-hex hash of the cache key.

No record contains message bodies, tool arguments, images, or the key itself. The baseline advances only in `promoteRequestCacheStats`, after the provider accepts an attempt. Compaction requests carry `call_kind` on their records.

## Cold-turn analysis

`node scripts/diagnostics.mjs coldturns` reports cold turns (script mechanics in [Diagnostics](diagnostics.md#analyzing-diagnostics)). A cold turn has `cache_read_tokens == 0`, which is different from `cold_start`. Models that never report a nonzero `cache_read_tokens` are listed as excluded, not counted as cold. Records are ordered by `seq`, not `ts`, because wall clocks can step backwards.

Each parent-turn gap is attributed to `no_prior_baseline`, `prefix_rewrite`, `delegation`, `idle`, or `unexplained`. `no_prior_baseline` is tested first and needs `prefix_comparison_enabled: true` with `prefix_predecessor_known: false`. The delegation threshold is four minutes (`LONG_DELEGATION_MS`).

## Session reset

`/clear` calls `Recorder.ResetSession`, which clears in-memory latest-request state per source. Persisted hourly buckets are kept.
