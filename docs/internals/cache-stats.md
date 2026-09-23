# Cache Hit Rate Tracking internals

User-facing documentation: [Cache Hit Rate Tracking](../user/cache-stats.md).

## Codex affinity and transport mechanics

Codex Responses requests send three affinity headers from the stable per-conversation `prompt_cache_key`:

- `session-id`: Steiner's session ID
- `thread-id`: the same session ID
- `originator`: `codex_cli_rs`

The headers route a conversation to one cache shard. `prompt_cache_key` alone did not produce the measured improvement; the headers are required. They are set in `(*responsesWire).HTTPRequest` (`internal/provider/wire_responses.go`). The prior measurement was approximately 68% to 89% on `gpt-5.4-mini`.

Codex does not accept `prompt_cache_retention: "24h"`; a live request returned `400 Bad Request: {"detail":"Unsupported parameter: prompt_cache_retention"}`. The native OpenAI chat-completions wire does send that field for supported models. A separate Codex replica quirk, observed in 2026-08, rejects that parameter with an OpenAI-shaped error even though `responsesWire` does not send it. `responsesWire.RefineRetry` recognizes the exact error through `isCodexPromptCacheRetentionRejection`; the retry path drops affinity headers so it is not pinned to the same replica. Remove this workaround when the upstream issue is fixed.

`codex.min_request_interval` serializes Codex requests when positive. It affects burst pacing only and does not improve cache hits according to the measurements below. Codex transport is HTTP by default, with WebSocket as an explicit option. WebSocket errors do not fall back to HTTP.

A non-streaming Responses request can fail with `400 "Stream must be set to true"` on some models. `isStreamRequiredError` in `internal/agent/model_call.go` emits a one-time diagnostic and latches a per-run flag so later turns stream directly. Unrelated 400 responses are unaffected.

The `ChatRequest.AdvisorCacheProfile` path is separate from this Codex behavior. Anthropic breakpoint placement is described in the advisor internals page; the default main-agent and delegation cache profiles are not changed by it.

## Superseded measurement history

The following claims were re-measured on 2026-08-25 and did not reproduce.

### Request pacing

A fast arm (1.5s gap) made 35 requests in 1m01s at 34.2 requests/minute with an 85.4% hit rate and no cold requests. A paced arm (4s gap) made 35 requests in 2m23s at 14.7 requests/minute with an 83.0% hit rate and one cold request. A follow-up soak found 88.9% for burst with a growing prefix at 38.5 requests/minute and 85.5% for three concurrent requests at 137.2 requests/minute, with no cold requests in either arm. Duration-matched runs were also inconsistent: the unpaced arm had 15 successful and 2 failed requests in 4m22s, while the paced arm had 21 successful and 1 failed in 3m28s. The earlier 0.78 to 0.89 claim was likely an aggregate-session artifact. Pacing is not a cache improvement mechanism.

### WebSocket stickiness

The earlier claim that WebSocket could reach approximately 0.95 versus HTTP's approximately 0.89 ceiling did not reproduce. Cache entries expired after about five minutes of idle time on both transports. Warm-turn medians were 1515ms for WebSocket and 1402ms for HTTP, and TCP+TLS handshake cost was a median 14ms. WebSocket provides no measured cache advantage. Issue [#567](https://github.com/luispabon/steiner/issues/567) tracks whether to remove it in v3.0.0.

### Warm and cold turns

Warm-turn cache reads measured approximately 95.2%. An aggregate near 87% is a weighted mix of warm turns and zero-read cold turns; the main aggregate lever is the number of cold turns, not warm-turn behavior. The affinity-header result above remains endorsed because its before/after measurement has not been refuted.

### Delegation-induced cold turns

Issue [#569](https://github.com/luispabon/steiner/issues/569) proposed that any delegated call over about five minutes makes the parent's next turn cold, and proposed batching delegations (lever A) or keepalive pings on the parent's prefix (lever B). Real-session diagnostics from 2026-09-09 to 2026-09-23 (80 runs, 1,446 consecutive same-model parent turn pairs, almost all Codex) supersede both that claim and a provisional 2026-09-10 finding of zero cold turns after long delegations.

There is no five-minute cliff in production. With the parent prefix intact, cold turns rose from 1.6% (17/1,039) after no delegation or one under 240s to 8.8% (13/148) after delegations of 300s or more: roughly 11 excess cold turns in two weeks, or about 8.5 excluding one large-context run that held 58% of the evicted tokens. They cost about 725k uncached tokens, about 0.8% of all uncached input; parent traffic was about 8% of prompt volume, and sub-agents carried most uncached tokens.

Lever B would have cost about 14.6M prefix tokens in pings to save about 0.725M, breaking even only if cached input is priced below about 4.7% of full input. Nine evictions followed delegations under 240s, which a four-minute ping cannot reach. Lever A's cache benefit is bounded by the excess eviction rate, so batching must be justified on wall-clock time alone. Both levers were dropped and #569 closed. The result is Codex-scoped: non-Codex orchestrators contributed four long-delegation windows.

Analysis note: every turn changes `prefix_hash`, so it cannot identify rewrites. `shared_prefix_messages` is the longest common message prefix with the previous request; it grows by each turn's new messages when the prefix is intact and drops on a rewrite. A cold turn after a rewrite is not explained by the rewrite alone: 192 of 216 rewrites stayed warm on the static prefix.

## Storage schema and concurrency

The global file is `$XDG_STATE_HOME/steiner/cache-stats.json`, falling back to `~/.local/state/steiner/cache-stats.json`. It is durable state, not a cache. A sibling `cache-stats.json.lock` holds the write lock and has mode `0600`; it is created on first write, persists, contains no data, and is safe to delete when no Steiner process is running.

Schema version 2 stores hourly entries keyed by provider alias, provider type, model id, and source:

```json
{
  "schema_version": 2,
  "entries": [
    {
      "provider": "local",
      "provider_type": "openai_compat",
      "model": "qwen2.5-coder:14b",
      "hour_unix": 1750514400,
      "source": "parent",
      "requests": 3,
      "input_tokens": 500,
      "cache_read_tokens": 150,
      "cache_create_tokens": 50,
      "completion_tokens": 220
    }
  ]
}
```

Schema version 2 adds `source` (`parent`, `sub_agent`, or `advisor`). Version 1 files load with an unspecified/parent source and are rewritten as version 2. Entries are summed within an hour and retained for eight days. Missing, corrupt, or unknown-version files start empty with a warning where appropriate. Persistence failures drop only the observation, never the model turn.

On Unix, an exclusive `flock` is acquired on the stable sibling lock file before read-modify-write. Locking the data file would be unsafe because temp-file replacement changes its inode. Each observation is merged as an additive delta after rereading the file. Platforms without `flock` cannot lock across processes, so they refuse to persist rather than write unlocked: the delta is dropped and logged, while in-session counts are unaffected. A short lock timeout drops persistence but not in-session counts.

## Diagnostics and analysis machinery

The optional `STEINER_USAGE_TELEMETRY` stream records usage and WebSocket lifecycle events for external analysis. The structured `cache.jsonl` stream is richer: its envelope carries source, agent type, agent id, turn, and `build_sha`; cache payloads carry model identity, token counts, a per-run cold-start bit, flags recording whether prefix comparison ran and whether a predecessor baseline was known, an 8-hex hash of the cache key, a cumulative prefix hash, and the number of leading messages shared with the last accepted request. It never writes message bodies, arguments, images, or the cache key.

The baseline only advances when the provider accepts a request; an attempt the provider rejects (HTTP error, a stream-required retry, a vision-capability retry, cancellation) is compared against the existing baseline but never becomes the predecessor for the next comparison. A turn that retries after a rejection (e.g. the inline image-strip retry, or the turn-level vision retry) is compared and promoted using only the accepted attempt's messages.

`node scripts/diagnostics.mjs coldturns` joins `cache.jsonl` and `tool.jsonl`. A cold turn has `cache_read_tokens == 0`; this differs from `coldStartRecorded`, which latches once per process and occurs at most once per run. The script walks consecutive `source: "parent"` records, finds the longest delegation-class call (`sub_agent`, `follow_up`, or `advisor`) fitting each gap, and derives its call window from `ts - duration_ms`. It orders each run's records by the envelope's process-global `seq`, not by `ts`: the sink appends each record serialized, so file order is write order, while a wall clock can step backwards and mis-pair consecutive turns. Records written before `seq` existed keep their file order.

It attributes parent gaps to `no_prior_baseline`, `prefix_rewrite`, `delegation`, `idle`, or `unexplained`. `no_prior_baseline` outranks the rest and needs both producer flags: `prefix_comparison_enabled: true` (a comparison actually ran) and `prefix_predecessor_known: false` (no in-process predecessor was found), so `shared_prefix_messages` carries no rewrite signal. A record whose comparison flag is `false` or absent, or that omits both fields because it predates them, falls through to the `prefix_rewrite` reading, which outranks delegation because a divergence at message 0 cannot match any cached entry. Delegation and idle use a four-minute threshold. `no_prior_baseline` is not `cold_start`: it marks a missing in-process predecessor on any turn, while `cold_start` still latches once per process on the first usage-bearing record.

Models that do not report cache reads are excluded rather than treated as cold. A backend omitting `prompt_tokens_details.cached_tokens` is indistinguishable from a backend with no hits, so counting it as 100% cold would fabricate a measurement. `--min-n` (default 10) prints `insufficient` for small delegation samples while always printing `n`. Rows group by backend model id, not provider type. The prefix signal cannot detect a partial rewrite because the stream has no message count; use session-log `prefix` mode for that case. That mode walks each agent's `api_request` events in append order, not by `payload.turn`, because an interactive prompt restarts the turn counter within the same agent group. Compaction/context-escalation calls do not emit `cache.jsonl` records even though they feed the aggregate store.

## Accounting invariant

For every provider adapter, `prompt_tokens` is a raw total including cached input. The non-cached denominator is derived as `prompt_tokens - cache_read_tokens - cache_create_tokens`; otherwise cache reads are double-counted and the reported rate is deflated. Provider usage is recorded by `internal/usagestats`, persisted through its hourly store, and surfaced by the session, overlay, and diagnostics paths.

The structured cache stream carries `cold_start: true` on the first usage-bearing record written by a process, one per run id regardless of agent. `cache_key_hash` is an 8-hex hash of the request key. `prefix_hash` is a rolling digest over the logical, post-transform provider messages, one hash per message folded cumulatively. Each per-message hash covers role, content, tool-call order and names, tool-call arguments, reasoning content, and image media/data, so all of those change it. It is not a hash of the raw provider-wire JSON and is unaffected by wire serialization details. `shared_prefix_messages` counts leading messages identical to the previous request for that conversation, distinguishing a pure append from a prefix rewrite. These fields are diagnostic signals only; they do not store prompt content.
