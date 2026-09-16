# Model enumeration

Steiner can discover models exposed by configured providers and add them to the
interactive `/model` chooser. Discovery is enabled by default. Cached choices are
available synchronously at startup; missing or stale providers refresh in the
background, with results added to the chooser when they arrive. Each provider
refresh has a roughly five-second timeout.

## Provider discovery

Enumeration uses the provider's model-list API. Provider names below are the keys
used under `providers`; they are not model aliases.

| Provider type | Endpoint used | Auth | Filtering signal |
|---------------|---------------|------|------------------|
| `openai`, `openai_compat`, `litellm`, `opencode_go`, `opencode_zen` | `GET /v1/models` | Bearer API key when configured | Embedding-like IDs are excluded. LiteLLM also excludes `mode: embedding`; missing mode uses the ID heuristic. |
| `ollama` | `GET /api/tags` | None required | Models with `capabilities` containing `embedding` are excluded. When capabilities are absent, the ID heuristic is used. |
| `lmstudio` | `GET /api/v1/models` | Bearer API key when configured | Entries with `type: embedding` are excluded. `max_context_length` is used as the context length. |
| `openrouter` | `GET /api/v1/models` | Bearer API key when configured | Text-only models are kept by default. `links.next` pagination is followed only when it stays on the same host. |
| `anthropic` | `GET /v1/models` | `x-api-key` or Bearer; sends `anthropic-version: 2023-06-01` | Model capabilities provide supported reasoning efforts. Pagination starts with `limit=1000` and falls back to `limit=20` when the larger limit is rejected. |
| `codex` | `GET {codex-base}/models?client_version=<steiner version>` | OAuth Bearer token and `ChatGPT-Account-ID` | Only models with `visibility: list` are included. Reasoning levels provide supported reasoning efforts. |

Native `gemini` is not a runtime-supported provider type and is not shown as a
supported discovery type. A user-provided compatible endpoint can use generic
`openai_compat` discovery only when it exposes the supported API shape.

Configured provider headers are sent with enumeration requests. Credentials are
used for requests and are never written to the model cache.

## Cache

Steiner stores one versioned JSON cache envelope per provider name under:

```text
$XDG_CACHE_HOME/steiner/provider-models/<sha256(providerName)[:16]>.json
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

The chooser displays `provider-name/model-alias` for a configured model definition,
or `provider-name/model-id` for a raw discovered entry with no configured alias.
It does not use the provider's pretty display name. Raw references are the normal
selection form: `provider-name/model-id`. Use an alias when it supplies a shorter or
stable name or persistent `ModelConfig` settings.

Every model selection accepts a config alias or a raw reference. This applies to
profile assignments, `--model`, `STEINER_MODEL`, and `/model`. Startup precedence
is the selected profile, then `STEINER_MODEL`, then `--model`; the last two affect
only the active orchestrator, not profile role fallback. In the interactive TUI,
`/model` changes only the active orchestrator. `/profile <name>` changes future
role assignments and the profile's fallback.

```text
provider-name/model-id

openrouter/openai/gpt-4o
```

An exact configured alias wins before prefix parsing. Otherwise, the longest
configured provider prefix is used, so model IDs containing slashes are supported.

## Feeding metadata resolution

Beyond the `/model` chooser, the same catalog cache feeds per-model metadata
resolution: context window, max output tokens, and supported reasoning efforts
for a configured model prefer catalog data (when present) over models.dev and
built-in fallbacks. This uses whatever is cached — a stale-but-present entry is
still consulted, the same as the chooser. Disabling discovery
(`models.discovery_enabled: false`) removes catalog data from metadata
resolution as well as from the chooser; resolution then falls through to
models.dev and built-in defaults.

## Popularity

Successful `/model` switches increment a count keyed by the canonical pair
(provider name, backend model ID). Popularity is stored at
`$XDG_STATE_HOME/steiner/model-popularity.json` (or
`~/.local/state/steiner/model-popularity.json` when `XDG_STATE_HOME` is unset).

Counts survive alias renames because they identify the backend model, not the
alias used to display it. Aliases that point to the same backend pool contribute
to the same count. Failed switches do not increment popularity.

## Disable discovery

Set `models.discovery_enabled` to `false` to skip enumeration and all discovery
network refreshes. The chooser then shows configured entries only. Dual-form
model references and popularity continue to work.

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
  a compatible endpoint as described above.
- Steiner does not automatically re-refresh providers during a session beyond the
  startup refresh of missing or stale caches and explicit `steiner models refresh`.
