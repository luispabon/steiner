# Prompt caching internals

User-facing documentation: [Cache Hit Rate Tracking](../user/cache-stats.md). Config fields: [configuration](../user/configuration.md).

## Cache identity

`provider.NewPromptCacheKey` returns 16 random bytes, hex encoded. If generation fails, the key stays empty and no key or affinity header is sent.

Parent runs take their key from the runner (`cliRunner.promptCacheKey` in `cmd/steiner`), and interactive sessions persist theirs. Delegated children get a key from `cacheKeyOrMint` in `internal/delegation`, keyed by agent type. A nil store or a failed mint gives a fresh key on each call. The advisor uses the same store under the slot `advisor`, which is not an agent type, so it stays out of `AllAgentTypes()`. Compaction escalation and turn progression reuse the run's `PromptCacheKey` and transport session fields.

Per provider:

- Codex Responses sends `prompt_cache_key` in the body and the affinity headers below.
- The OpenAI chat wire sends `prompt_cache_key` only for provider type `openai`, and `prompt_cache_retention: "24h"` only for models that support extended retention.
- Anthropic does not read `PromptCacheKey`. Its caching comes from `cache_control` breakpoints in `anthropic_wire.go`, at most four per request.

OpenCode-compatible routing applies only when the wire configures an `X-Opencode-Session` header. A non-empty `TransportSession` is sent in that header, and `ParentTransportSession` is sent as `X-Parent-Session-Id`. Child agents use `<session>-<agentID>`, and advisor calls use `<session>-advisor` with the session as parent (`internal/delegation/registry.go`). Other wires ignore these fields.

## Codex affinity headers

When the key is non-empty, Codex Responses requests set `session-id` and `thread-id` to the key and `originator` to `codex_cli_rs`. `(*responsesWire).HTTPRequest` sets them in `wire_responses.go`, and the WebSocket dial in `codex_ws_conn.go` sets the same three headers. The headers pin one conversation to one backend shard.

Codex rejects `prompt_cache_retention`, so `responsesWire` never sends it. A Codex replica also rejects that field with an OpenAI-shaped error. `isCodexPromptCacheRetentionRejection` matches that error, and the retry drops the cache key, so the retried request sends no affinity headers.

## Request pacing

`codex.min_request_interval` sets the minimum gap between Codex request starts. The default is `0s`, which turns pacing off. `Client.pace` returns at once when the interval is zero or less. Otherwise it holds the client mutex while it waits until the interval has passed since the last request start, then records the new start. Concurrent calls on one client queue behind that wait. Pacing changes timing only. It does not change keys or headers.

## Codex transport

The Codex transport setting selects `http` (default) or `websocket`. The WebSocket endpoint and beta header are marked unconfirmed in `codex_ws_protocol.go`, so WebSocket stays opt-in. WebSocket errors return to the caller and do not fall back to HTTP. Validation rejects `auto` and other unknown values.

## Stream-required retry

A non-streaming request that fails with a 400 mentioning `stream` and `must` or `required` is matched by `agent.IsStreamRequiredError`. The turn streams, the run latches so later turns stream directly, and a `stream_required` provider diagnostic (warning) is emitted. Other 400 responses are unaffected. Advisor calls retry as a stream on the same error ([advisor internals](advisor.md#request-flow)).

## Codex conversation replay

Codex replay preserves assistant message boundaries and per-message `phase` (`commentary` or `final_answer`), along with message and function-call order, in provider-native metadata on session messages. Distinct non-empty message items are joined by at least one blank line in flattened content. Streamed text is reconciled with completed output to recover missing text without duplication. Phase preservation does not guarantee narration before tool calls.

## Usage fields

`openAIUsage.toUsageStats` in `openai_wire.go` reads cache hits by presence. `prompt_tokens_details.cached_tokens` wins over `prompt_cache_hit_tokens`, which wins over top-level `cached_tokens`, and a present zero counts. `cache_write_tokens` maps to cache creation tokens. Providers that omit it report zero.

`prompt_tokens` is the raw total including cached input. Diagnostics derive the non-cached count as `prompt_tokens - cache_read_tokens - cache_create_tokens`, floored at zero (`scripts/diagnostics.mjs`).

## Usage statistics storage

The global store is `$XDG_STATE_HOME/steiner/cache-stats.json`, or `~/.local/state/steiner/cache-stats.json` when `XDG_STATE_HOME` is unset. Schema version 2 stores hourly entries keyed by provider alias, provider type, model, hour, and source (`parent`, `sub_agent`, or `advisor`). Entries are retained for eight days (`retentionDays`). Version 1 entries decode with source `SourceUnknown`, not parent. Missing, corrupt, or unknown-version files load as empty.

Writes take an exclusive `flock` on the sibling `cache-stats.json.lock` (mode `0600`), reread the file, and merge the observation as an additive delta. The lock is a separate file because the data file is replaced by rename (`fsatomic.WriteFile`), which changes its inode. Platforms without `flock` skip persistence rather than write unlocked. In-session counts are unaffected.

## Telemetry

`STEINER_USAGE_TELEMETRY` names a JSONL file for usage records, and `STEINER_USAGE_TELEMETRY_RUN` sets the run ID. Each line carries `seq`, a process-wide counter incremented under the writer mutex, so file order matches `seq` order.

## Cache diagnostics

Each usage-bearing model response writes one `cache` record (`internal/agent/cache_diagnostics.go`) with model identity and token counts, plus:

- `cold_start`: true on the first usage-bearing record a process writes. A process has one run ID, so this is once per run.
- `prefix_comparison_enabled` and `prefix_predecessor_known`: whether prefix comparison ran and whether an in-process predecessor existed.
- `shared_prefix_messages`: leading messages shared with the last accepted request. It separates a pure append from a prefix rewrite.
- `prefix_hash`: a cumulative 8-hex digest folded over one 8-hex hash per message. Each message input is `provider.MessageHashInput`: role, content, reasoning content, tool-call names and arguments, and image media types and data. It hashes logical messages, not wire JSON.
- `cache_key_hash`: an 8-hex hash of the cache key.

No record contains message bodies, tool arguments, images, or the key itself. The baseline advances only in `promoteRequestCacheStats`, after the provider accepts an attempt. Compaction requests carry `call_kind` on their records.

## Cold-turn analysis

`node scripts/diagnostics.mjs coldturns` joins cache and tool records. A cold turn has `cache_read_tokens == 0`, which is different from `cold_start`. Models that never report a nonzero `cache_read_tokens` are listed as excluded, not counted as cold. Records are ordered by `seq`, not `ts`, because wall clocks can step backwards.

Each parent-turn gap is attributed to `no_prior_baseline`, `prefix_rewrite`, `delegation`, `idle`, or `unexplained`. `no_prior_baseline` is tested first and needs `prefix_comparison_enabled: true` with `prefix_predecessor_known: false`. The delegation threshold is four minutes (`LONG_DELEGATION_MS`).

## Session reset

`/clear` calls `Recorder.ResetSession`, which clears in-memory latest-request state per source. Persisted hourly buckets are kept.
