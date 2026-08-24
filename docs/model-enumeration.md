# Model enumeration

Steiner can discover models exposed by configured providers and add them to the
interactive `/model` chooser. Discovery is enabled by default. Cached choices are
available synchronously at startup; missing or stale providers refresh in the
background, with results added to the chooser when they arrive. Each provider
refresh has a roughly five-second timeout.

## Provider discovery

Enumeration uses the provider's model-list API. Provider aliases below are the
names used under `providers` in configuration.

| Provider type | Endpoint used | Auth | Filtering signal |
|---------------|---------------|------|------------------|
| `openai`, `openai_compat`, `litellm` | `GET /v1/models` | Bearer API key | Embedding-like IDs are excluded. LiteLLM also excludes `mode: embedding`; missing mode uses the ID heuristic. |
| `ollama` | `GET /api/tags` | None required | Models with `capabilities` containing `embedding` are excluded. When capabilities are absent, the ID heuristic is used. |
| `lmstudio` | `GET /api/v1/models` | Bearer API key when configured | Entries with `type: embedding` are excluded. `max_context_length` is used as the context length. |
| `openrouter` | `GET /api/v1/models` | Bearer API key when configured | Text-only models are kept by default. `links.next` pagination is followed only when it stays on the same host. |
| `anthropic` | `GET /v1/models` | `x-api-key` or Bearer; sends `anthropic-version: 2023-06-01` | Model capabilities provide supported reasoning efforts. Pagination starts with `limit=1000` and falls back to `limit=20` when the larger limit is rejected. |
| `codex` | `GET {codex-base}/models?client_version=<steiner version>` | OAuth Bearer token and `ChatGPT-Account-ID` | Only models with `visibility: list` are included. Reasoning levels provide supported reasoning efforts. |

Configured provider headers are sent with enumeration requests. Credentials are
used for requests and are never written to the model cache.

## Cache

Steiner stores one versioned JSON cache envelope per provider alias under:

```text
$XDG_CACHE_HOME/steiner/provider-models/<sha256(providerAlias)[:16]>.json
```

When `XDG_CACHE_HOME` is not set, the cache uses
`~/.cache/steiner/provider-models/`. A cache fingerprint contains the provider
type and base URL. Changing either invalidates the cache. Entries remain fresh for
seven days; stale entries can still provide chooser choices while a refresh runs.

Cache writes are atomic and use a per-provider file lock. Concurrent writers use
last-writer-wins behavior. Codex sends the cached ETag when available; a `304 Not
Modified` response extends that cache's freshness when the ETag matches.

At startup, cached choices load before network refresh. Steiner refreshes missing
or stale providers in the background, best effort, and sends updated choices to
the chooser as each provider finishes.

## Model chooser and references

Configured model definitions and discovered models are merged. A configured alias
suppresses its matching raw provider/model entry. The chooser ranks by switch count
descending, then puts aliased definitions before raw entries, then sorts by display
name alphabetically. Supported reasoning efforts from a configured definition take
precedence over discovered efforts.

The chooser displays `provider-alias/model-alias` for a configured model definition,
or `provider-alias/model-id` for a raw discovered entry with no configured alias —
never the provider's pretty display name.

Every model selection accepts either a config alias or a raw `provider/model-id`
reference. This applies to `models.default`, `models.advisor`,
`models.sub_agents.*`, `models.oneshot.*`, `models.workflow_handoff.*`, the
`--model` flag, `STEINER_MODEL`, and the `/model` command.

```text
alias | provider/model-id

openrouter/openai/gpt-4o
```

An exact alias wins before prefix parsing. Otherwise, the longest configured
provider prefix is used, so model IDs containing slashes are supported.

## Popularity

Successful `/model` switches increment a count keyed by the canonical pair
(provider alias, backend model ID). Popularity is stored at
`$XDG_STATE_HOME/steiner/model-popularity.json` (or
`~/.local/state/steiner/model-popularity.json` when `XDG_STATE_HOME` is unset).

Counts survive alias renames because they identify the backend model, not the
alias used to display it. Aliases that point to the same backend pool contribute
to the same count. Failed switches do not increment popularity.

## Disable discovery

Set the flat boolean `models.discovery_enabled` to `false` to skip enumeration and
all discovery network refreshes. The chooser then shows configured entries only.
Dual-form model references and popularity continue to work.

```yaml
models:
  discovery_enabled: false
```

## CLI

Force enumeration of every configured provider:

```bash
steiner models refresh
```

The command prints one result line per provider and exits nonzero only when every
provider fails. When discovery is disabled, it prints that model discovery is
disabled.

Inspect cache freshness without refreshing:

```bash
steiner models status
```

Status prints `fresh`, `stale`, or `missing` for each configured provider. It
prints `disabled` when discovery is off.

## Non-goals

- Discovery does not write model definitions back to configuration.
- Discovery does not provide a Gemini transport. Use a supported provider type or
  a compatible endpoint as described in the configuration reference.
- Steiner does not automatically re-refresh providers during a session beyond the
  startup refresh of missing or stale caches and explicit `steiner models refresh`.
