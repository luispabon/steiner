# Cache Hit Rate Tracking internals

User-facing documentation: [Cache Hit Rate Tracking](../user/cache-stats.md).

## Codex affinity and transport mechanics

Codex Responses requests send three affinity headers from the stable per-conversation `prompt_cache_key`:

- `session-id`: Steiner's session ID
- `thread-id`: the same session ID
- `originator`: `codex_cli_rs`

The headers route a conversation to one cache shard. `prompt_cache_key` alone did not produce the measured improvement; the headers are required. They are set in `buildResponsesHTTPRequest` (`internal/provider/codex_responses.go`). The prior measurement was approximately 68% to 89% on `gpt-5.4-mini`.

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

A provisional Codex-only analysis on 2026-09-10 found 22 parent turns after delegations lasting 261s to 1132s and none cold. It covered roughly 15 hours of sessions, 21 of 22 observations from Codex, and diagnostics streams that had only recently landed. The result is not a settled claim and issue #569 remains open.

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

The optional `STEINER_USAGE_TELEMETRY` stream records usage and WebSocket lifecycle events for external analysis. The structured `cache.jsonl` stream is richer: its envelope carries source, agent type, agent id, turn, and `build_sha`; cache payloads carry model identity, token counts, a per-run cold-start bit, an 8-hex hash of the cache key, a cumulative prefix hash, and the number of leading messages shared with the last sent request. It never writes message bodies, arguments, images, or the cache key.

`node scripts/diagnostics.mjs coldturns` joins `cache.jsonl` and `tool.jsonl`. A cold turn has `cache_read_tokens == 0`; this differs from `coldStartRecorded`, which latches once per process and occurs at most once per run. The script walks consecutive `source: "parent"` records, finds the longest delegation-class call (`sub_agent`, `follow_up`, or `advisor`) fitting each gap, and derives its call window from `ts - duration_ms`. It attributes parent gaps to `prefix_rewrite`, `delegation`, `idle`, or `unexplained`. Prefix rewrite outranks delegation because a divergence at message 0 cannot match any cached entry. Delegation and idle use a four-minute threshold.

Models that do not report cache reads are excluded rather than treated as cold. A backend omitting `prompt_tokens_details.cached_tokens` is indistinguishable from a backend with no hits, so counting it as 100% cold would fabricate a measurement. `--min-n` (default 10) prints `insufficient` for small delegation samples while always printing `n`. Rows group by backend model id, not provider type. The prefix signal cannot detect a partial rewrite because the stream has no message count; use session-log `prefix` mode for that case. Compaction/context-escalation calls do not emit `cache.jsonl` records even though they feed the aggregate store.

## Accounting invariant

For every provider adapter, `prompt_tokens` is a raw total including cached input. The non-cached denominator is derived as `prompt_tokens - cache_read_tokens - cache_create_tokens`; otherwise cache reads are double-counted and the reported rate is deflated. Provider usage is recorded by `internal/usagestats`, persisted through its hourly store, and surfaced by the session, overlay, and diagnostics paths.

The structured cache stream carries `cold_start: true` on the first usage-bearing record written by a process, one per run id regardless of agent. `cache_key_hash` is an 8-hex hash of the request key. `prefix_hash` folds role, content, and tool-call names over the sent messages, never tool arguments or images. `shared_prefix_messages` counts leading messages identical to the previous request for that conversation, distinguishing a pure append from a prefix rewrite. These fields are diagnostic signals only; they do not store prompt content.
