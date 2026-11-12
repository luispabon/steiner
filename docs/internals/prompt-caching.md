# Prompt caching internals

User guide: [Usage and cache statistics](../user/usage.md#cache-hit-rate).

## Cache identity

`provider.NewPromptCacheKey` returns 16 random bytes, hex encoded. If generation fails, the key stays empty and no key or affinity header is sent.

Parent runs take their key from the runner (`cliRunner.promptCacheKey` in `cmd/steiner`), and interactive sessions persist theirs. Delegated children get a key from `cacheKeyOrMint` in `internal/delegation`, keyed by agent type. A nil store or a failed mint gives a fresh key on each call. The advisor uses the same store under the slot `advisor`, which is not an agent type, so it stays out of `AllAgentTypes()`. Compaction escalation and turn progression reuse the run's `PromptCacheKey` and transport session fields. A compaction fallback target uses derived values instead ([compaction fallback identities](#compaction-fallback-identities)).

Per provider:

- Codex Responses sends `prompt_cache_key` in the body and the affinity headers below.
- The OpenAI chat wire sends `prompt_cache_key` only for provider type `openai`, and `prompt_cache_retention: "24h"` only for models that support extended retention.
- Anthropic does not read `PromptCacheKey`. `assignCacheBreakpoints` (`internal/provider/anthropic_wire.go`) places at most four `cache_control` breakpoints: the last system block (or the last tool when there is no system block), the final non-volatile message block, and the last block of the second-to-last user message. Blocks at or after the first volatile block, which holds image data stripped after one request, are never marked. Advisor-shaped requests use a different placement ([advisor internals](advisor.md#transport-and-cache)).

OpenCode-compatible routing applies only when the wire configures an `X-Opencode-Session` header. `prepareTransportSessionHeaders` (`internal/provider/openai_compat.go`) sends a non-empty `TransportSession` in that header and `ParentTransportSession` as `X-Parent-Session-Id`. The OpenAI chat and Anthropic wires call it. Codex Responses does not. Child agents use `<session>-<agentID>`, the parent advisor uses `<session>-advisor`, and sub-agent advisors use `<session>-subagent-advisor`, all with the session as parent (`internal/delegation/registry.go`).

### Compaction fallback identities

`compactionFallbackRequest` (`internal/agent/compaction_fallback.go`) gives a fallback summary identities derived from the run's values. `compactionFallbackIdentity` hashes length-prefixed main value, `compaction-fallback` label, provider alias, and backend model ID fields with SHA-256, and keeps the first 16 bytes as 32 hex characters. The result becomes the request's `PromptCacheKey`. It becomes the request's `TransportSession` when the main transport session is set. An empty main `PromptCacheKey` stays empty. An empty main transport session gets a new `provider.NewPromptCacheKey` value once per fallback compaction operation, and that operation's summary calls share it. When the main `PromptCacheKey` is non-empty, each target's key is separate from the main run's and from other targets'.

The fallback request sets `CacheBaseline` to nil. Its call neither compares against nor advances the main prefix baseline (`computeRequestCacheStats`, `promoteRequestCacheStats`).

## Codex affinity headers

When the key is non-empty, Codex Responses requests set `session-id` and `thread-id` to the key and `originator` to `codex_cli_rs`. `(*responsesWire).HTTPRequest` sets them in `wire_responses.go`, and the WebSocket dial in `codex_ws_conn.go` sets the same three headers. The headers pin one conversation to one backend shard.

Codex rejects `prompt_cache_retention`, so `responsesWire` never sends it. A Codex replica also rejects that field with an OpenAI-shaped error. `isCodexPromptCacheRetentionRejection` matches that error, and the retry drops the cache key, so the retried request sends no affinity headers.

## Request pacing

`codex.min_request_interval` sets the minimum gap between Codex request starts. The default is `0s`, which turns pacing off. `Client.pace` returns at once when the interval is zero or less. Otherwise it holds the client mutex while it waits until the interval has passed since the last request start, then records the new start. Concurrent calls on one client queue behind that wait. Pacing changes timing only. It does not change keys or headers.

## Codex transport

Transport selection and fallback are in [Codex settings](../user/configuration.md#providers-block). The WebSocket endpoint and beta header are marked unconfirmed in `codex_ws_protocol.go`, so WebSocket stays opt-in. Validation rejects `auto` and other unknown values.

### Codex WebSocket provider cache

`CodexWSCache` (`internal/provider/codex_ws_cache.go`) keeps one live Codex WebSocket provider per `<alias>|<promptCacheKey>`, so a session reuses its socket across turns instead of redialling. Entries are generation-scoped: the prompt cache key is the generation, and acquiring an entry for a new key retires every entry of the previous one. That is the bound on connection lifetime; there is no TTL, entry cap, or per-entry timer. Only `cliRunner.runtimeProvider` acquires entries, and one interactive live driver serialises run and compaction while rotation, clear, load and fork keep the driver idle, so a generation changes between runs, not during one. Same-generation acquisitions (another alias, or the same key) stay cached, and a failed request still evicts its own entry so the next lookup rebuilds and redials.

Retirement detaches entries under the cache lock and retires them outside it. An idle entry closes its socket and joins its keepalive goroutine immediately. An entry with a request in flight is only marked retired: retirement never closes its connection, which closes when that request ends. Retirement does not extend a request either, so a request whose own context is cancelled still ends with that cancellation as usual, and a caller queued behind the active request wakes to a cancellation or a retired-entry error. `cliRuntime.codexWSProviderCache` is closed by `closeRuntime`. `CodexWSCache.Close` is idempotent, does not wait for in-flight requests, and rejects later acquisition; a build that finishes after the close, or after another generation became current, is rejected without being stored, and its never-dialled provider is discarded.

### Uncached Codex WebSocket connections

The cache owns cached connections. Providers the runtime factory builds for a borrowing owner are not cached: an advisor runtime's provider, resolved once per registry build; a delegated child's provider; and a compaction fallback target's provider, memoized per identity and cache scope. Without a release each would keep its socket and keepalive goroutine for the life of the process. `provider.ConnectionReleaser` (`internal/provider/interface.go`) is the optional capability that closes such a connection, and `provider.ReleaseConnection` applies it to a provider that implements it. Only `*codexWSProvider` implements it; `CachingCodexWS` deliberately does not, so a release through a cached provider is a no-op and the cache keeps control of its entries.

A release is non-terminal: it closes the connection, and the next request dials again with the cached `dialCacheKey`, so the affinity headers (`session-id`, `thread-id`, `originator`) stay identical across the redial. `ReleaseConnection` never interrupts an active request. Requests bracket their socket I/O with `enterRequest` and `leaveRequest` under the provider's gate mutex, entering before the request takes the provider mutex and leaving after it drops it, on the goroutine that performs the I/O (`ChatCompletion`, and `streamOnce` for streaming). A release that lands while a request is in flight only records `releasePending` and returns; the last request to leave closes the connection. Leave and release hold the gate mutex while taking the provider mutex and calling `closeConn`, which joins the keepalive goroutine, so lock order is always gate then provider mutex, and a request starting at that moment cannot dial into a connection the close is about to drop. The `inFlight` and `releasePending` fields live in `codexWSProvider` (`internal/provider/codex_responses_ws.go`).

Three call sites release after use: `adviseWithMessages` (`internal/advisor/tool.go`), deferred until the call has drained; `SpawnDelegate` (`internal/delegation/task.go`), deferred past the child run and its remediation, so an async child releases after its supervisor worker's run rather than at dispatch; and `runCompactionOnTarget` (`internal/agent/compaction_fallback.go`), after the fallback operation ends. Each advisor call therefore redials, because the release closes the connection after every call even though the provider itself is shared by every advisor call its registry serves. That redial is the accepted cost of not leaking the socket, and a memoized compaction fallback target redials the same way on its next compaction.

### Codex WebSocket credentials

A Codex WebSocket provider can outlive the token it was built with: a release, a cache eviction or a dropped socket all redial later. `cmd/steiner` therefore sets `ClientConfig.DialBearerResolver` to `provider.ResolveCodexAuth` and `ClientConfig.CodexSignIn` to the build-time `CodexAuth.SignInIdentity()` for the WebSocket transport only, and `codexWSProvider.ensureConnection` (`internal/provider/codex_ws_conn.go`) resolves the stored sign-in again before each dial. The identity is explicit: `APIKeyMode` marks a token carrying an exchanged OpenAI API key, and `AccountID` is the ChatGPT account. It is never derived from configured headers, which a user can set independently of the sign-in, and `NewCodexResponsesWS` rejects a resolver supplied without one. A resolved sign-in whose identity differs fails the dial with `errCodexSignInChanged` instead of sending the new bearer under the old base URL, account header and quota key. A live connection is never re-resolved; without a resolver the static `APIKey` is the bearer.

A resolver failure is wrapped in `wsAuthError`, which `executeRequest` reports without its single dial retry. That retry exists for a dead socket; a retry of a resolver failure would repeat the same resolution, and for a failed refresh it would reach the token endpoint twice for one request. `Unwrap` exposes the cause, so `context.Canceled` stays recognizable and does not evict a cached entry, while a credential failure does evict it. Recovery is per request: the next request on the same provider resolves again, and a cached entry rebuilds from the current sign-in. A provider memoized outside the cache (an advisor runtime's provider, a compaction fallback target, a delegated child) keeps the identity baseline it was built with, so after a re-login to another account or auth mode it fails its dials until it is rebuilt by a new run or a restart. The error carries no credential material.

## Codex conversation replay

Codex replay preserves assistant message boundaries and per-message `phase` (`commentary` or `final_answer`), along with message and function-call order, in provider-native metadata on session messages. Distinct non-empty message items are joined by at least one blank line in flattened content. Streamed text is reconciled with completed output to recover missing text without duplication. Phase preservation does not guarantee narration before tool calls.

## Usage fields

`openAIUsage.toUsageStats` in `openai_wire.go` reads cache hits by presence. `prompt_tokens_details.cached_tokens` wins over `prompt_cache_hit_tokens`, which wins over top-level `cached_tokens`, and a present zero counts. `cache_write_tokens` maps to cache creation tokens. Providers that omit it report zero.

`prompt_tokens` is the raw total including cached input. Diagnostics derive the non-cached count as `prompt_tokens - cache_read_tokens - cache_create_tokens`, floored at zero (`scripts/diagnostics.mjs`).

## Usage statistics storage

The global store is `$XDG_STATE_HOME/steiner/cache-stats.json`, or `~/.local/state/steiner/cache-stats.json` when `XDG_STATE_HOME` is unset. Schema version 2 stores hourly entries keyed by provider alias, provider type, model, hour, and source (`parent`, `sub_agent`, or `advisor`). Entries are retained for eight days (`retentionDays`). Version 1 entries decode with source `SourceUnknown`, not parent. Missing, corrupt, or unknown-version files load as empty. `store.writeBatch` decodes the file it is about to rewrite and returns the decode error for corrupt JSON or an unsupported schema version instead of writing, so unreadable history stays byte-for-byte intact until the file is repaired, replaced from a backup, moved aside, or opened by a build that supports its schema version. A missing or zero-length file is a valid empty store and is written normally.

Persistence is asynchronous. `Recorder.Record` adds a delta to a pending map keyed by `bucketKey`; every delta field is additive (request count and token sums), so entries for one key are summed rather than replaced. A `persistWriter` goroutine (`internal/usagestats/persist.go`) swaps the pending map for a fresh one and calls `store.writeBatch` once per batch, which takes the exclusive `flock` on the sibling `cache-stats.json.lock` (mode `0600`), rereads the file, merges every key as an additive delta, and rewrites it with `fsatomic.WriteFile`. The lock is a separate file because the data file is replaced by rename, which changes its inode. Platforms without `flock` skip persistence rather than write unlocked.

`Drain(ctx)` waits for the pending and in-flight batch; `Close()` stops the writer, drops anything still pending, and wakes a waiting `Drain`. Both are idempotent. After `Close` no batch is claimed, so no new write starts; a batch already claimed may still reach the store. `persistWriter.abandon` logs the count of dropped buckets, and `persistWriter.add` drops and logs any delta that arrives after `Close`, so a post-Close observation never reaches the store. A write blocked on the lock cannot be cancelled: an expired `Drain` reports an error while the writer stays blocked until the lock is released or the process exits, and the queued deltas are lost. While another process holds the `flock`, every new delta queues in the pending map instead of being written, so the file lags the session and the window of data a crash could lose grows until the lock is released. Write and drop failures are logged outside the writer mutex. `cmd/steiner` `closeRuntime` drains with its own 5 second deadline (`usageStatsDrainTimeout`, independent of the quota drain's budget) after delegation shutdown, then closes the recorder.

In-session readers (`Recorder.Window`, `SessionReport`, `SessionReportFor`) read the in-memory buckets that `Record` updates, so they never wait on persistence and always include the current session's observations. A separate process, or a new `Recorder`, sees only flushed data. The `STEINER_USAGE_TELEMETRY` JSONL writer is separate and still synchronous.

## Telemetry

`STEINER_USAGE_TELEMETRY` names a JSONL file for usage records, and `STEINER_USAGE_TELEMETRY_RUN` sets the run ID. `telemetry.record` (`internal/usagestats/telemetry.go`) writes each usage line synchronously under its writer mutex, without buffering or fsync. A slow filesystem can delay `Recorder.Record` and model turns. Each usage line carries `seq`, a per-recorder counter incremented under that mutex, so usage lines from the same recorder follow `seq` order. Sequence numbers can repeat between runs; distinct `run_id` values, when set, distinguish runs. WebSocket events use a separate writer (`recordWSTelemetry` in `internal/provider/codex_ws_telemetry.go`), have no `seq`, and are written synchronously during dial and reconnect handling. Usage bucket persistence is separate and asynchronous.

## Cache diagnostics

Each usage-bearing model response writes one `cache` record (`internal/agent/cache_diagnostics.go`) with model identity and token counts, plus:

- `cold_start`: true on the first usage-bearing record a process writes. A process has one run ID, so this is once per run. Fallback compaction calls write `false` and leave the latch unchanged.
- `prefix_comparison_enabled` and `prefix_predecessor_known`: whether prefix comparison ran and whether a predecessor existed. The predecessor is the last accepted request under the same `CacheBaselineKey` (`internal/agent/cache_baseline.go`), which holds the cache key, agent ID, configured and effective provider type, effective transport, provider alias, and backend model ID. An empty cache key disables baselines.
- `shared_prefix_messages`: leading messages shared with the last accepted request. It separates a pure append from a prefix rewrite.
- `prefix_predecessor_messages`: the predecessor request's message count. `prefix_divergence_role` is the role of the first request message that differs from the predecessor, present only when the request is neither an append of the predecessor nor a strict prefix of it.
- `prefix_hash`: a cumulative 8-hex digest folded over one 8-hex hash per message. Each per-message digest is over the `provider.AppendMessageHashInput` bytes, appended into one reused buffer per request: role, content, reasoning content, tool-call names and arguments, and image media types and data. It hashes logical messages, not wire JSON.
- `cache_key_hash`: an 8-hex hash of the cache key.

`context_generation` is written on every record: the number of compaction stages applied within the current run when the call was issued (`turnProgressor.compactionCount`, which adds each compaction's stage count). It restarts at 0 for each run, so a delegated child's first call reports 0 whatever the parent compacted. `prefix_predecessor_messages` is written whenever `prefix_predecessor_known` is true, including a known 0.

Every other measurement field is emitted only with a non-zero or true value, so an absent field means zero or not measured. `attempts` always exceeds zero when a provider measures it, so only claude_subscription and providers that measure nothing leave it out.

- `attempts`: transport attempts the successful call spent, counting the attempt that succeeded and starting at 1 (`Client.ChatCompletion`, `Client.streamWithRetry`). The Codex WebSocket path counts `sendRequest` calls rather than dials (`codexWSProvider.executeRequest`). A single-attempt call writes `"attempts":1`. claude_subscription never reports it: it does not count its deliveries, even when a pre-output failure makes `claudeSubNormalTurn` run a second delivery (reseed reason `retry`).
- `cache_affinity_dropped`: the successful attempt was sent without `PromptCacheKey` after the cache-retention rejection. Absent when no attempt dropped affinity.
- `claudesub_process`, `claudesub_reseed_reason`, `claudesub_usage_gate_ms`, `claudesub_usage_gate_polls`, `claudesub_usage_probe_spawned`: claude_subscription only, and a present `claudesub_process` marks a claude_subscription call. The process disposition is `reuse`, `resume`, or `spawn`. The reseed reason is one of `no_state`, `fingerprint`, `final_uuid`, `prefix`, `plan`, `checkpoint`, `owner_fork`, `exited`, `retry`, or `helper`. The gate fields report the extra-usage gate's elapsed time truncated to whole milliseconds and its `get_usage` poll count, summed across every delivery attempt of the call, including a failed fork before a fallback. The probe flag reports that a fresh confirmation process was started for the call, and it stays set across attempts.

Provider measurements reach the agent as `provider.CallDiagnostics`: on `ChatResponse` for a non-streaming call, and on the terminal `ChatChunk` for a streaming call, which `handleFinalChunk` copies onto the folded response when the chunk carries them.

No record contains message bodies, tool arguments, images, or the key itself. The baseline advances only in `promoteRequestCacheStats`, after the provider accepts an attempt. Compaction requests carry `call_kind` on their records.

## Cold-turn analysis

`node scripts/diagnostics.mjs coldturns` reports cold turns (script mechanics in [Diagnostics](diagnostics.md#analyzing-diagnostics)). A cold turn has `cache_read_tokens == 0`, which is different from `cold_start`. Models that never report a nonzero `cache_read_tokens` are listed as excluded, not counted as cold. Records are ordered by `seq`, not `ts`, because wall clocks can step backwards.

Each parent-turn gap is attributed to `no_prior_baseline`, `prefix_rewrite`, `delegation`, `idle`, or `unexplained`. `no_prior_baseline` is tested first and needs `prefix_comparison_enabled: true` with `prefix_predecessor_known: false`. The delegation threshold is four minutes (`LONG_DELEGATION_MS`).

## Session reset

`/clear` calls `Recorder.ResetSession`, which clears in-memory latest-request state per source. Persisted hourly buckets are kept.
