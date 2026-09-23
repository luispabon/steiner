# Configuration: Internals

User-facing documentation: [Configuration reference](../user/configuration.md). The user page is the sole exhaustive field reference. This page records implementation mechanics that are useful when diagnosing configuration behavior.

## Loading and merge mechanics

Configuration is loaded and merged in this order, with later sources winning:

1. Compiled defaults from `internal/config/defaults.go`.
2. `~/.config/steiner/config.yaml`.
3. `.steiner/config.yaml`.
4. `STEINER_` environment variables.
5. CLI overrides.

The `--unsafe` flag is applied after file and environment merging and forces `sandbox.enabled=false`. Environment expansion is performed on scalar YAML values before decoding; undefined references are collected across files and reported with file, line, YAML path, and variable. The syntax and user-visible error behavior remain documented in the [configuration reference](../user/configuration.md#environment-variable-expansion-in-config-values).

`config.Load` gates the project config layer on `LoadOptions.ProjectTrust`, a `ProjectTrust` enum whose zero value, `ProjectTrustUntrusted`, is fail-closed. `checkProjectTrust` runs per config-file item before it's read: when the item is the project layer, trust isn't `ProjectTrustTrusted`, and the project config file exists on disk, `Load` returns `ErrProjectUntrusted` immediately, without reading or applying it. A missing project config file is always allowed through regardless of trust, since there is nothing to cascade. User-facing behavior is documented in [Project trust](../user/configuration.md#project-trust).

`config.InspectProject` reads the project config layer for display purposes: it decodes the file to a raw `yaml.Node` with no environment-variable expansion and no validation, then diffs it against the global config and compiled defaults to produce `ProjectInspection.Changes`. Despite the read-only name, it has a side effect — it calls the same trust-store read path as `Load`'s trust check, which deletes a corrupt or unrecognized-version trust store file and surfaces that as `ProjectInspection.StoreNotice` rather than an error. A YAML parse failure on the project file is reported as `ProjectInspection.ParseError`, a string, not a returned Go error, so callers can still show the trust dialog with a parse-error message in place of a diff.

Trust resolution happens exactly once per process, in `cmd/steiner`'s `loadCLIConfig`/`ensureProjectTrust`, memoized on `cliFlags.trustResolved` so repeated config loads within a run (interactive TUI, sub-commands) don't re-resolve or re-prompt. Resolution order: already-resolved → stored trust (`ProjectInspection.Trusted`) → `--trust-project-config` flag → `STEINER_TRUST_PROJECT_CONFIG` env var → interactive trust dialog (`tui.RunTrustDialog`, gated on `!flags.exec && isInteractiveTerminal()`) → hard error naming the fix. Only the "Always trust" dialog choice calls `config.TrustProject`, which persists to `~/.config/steiner/trusted_projects.json`. `steiner config` bypasses this path entirely, always loading with `ProjectTrustTrusted` and printing an untrusted-project header from `InspectProject` instead of gating the load.

Trust and notice dialogs load styling only from `~/.config/steiner/prefs.yaml` (`tui.dialogStyles`), never from steiner project or global config — an untrusted repo's config must not be able to control the presentation of the dialog asking whether to trust it.

`internal/config/security_fields_test.go` pins `securityFieldPatterns` (in `security_fields.go`) against reflection over the config patch structs' `yaml` tags: one test confirms every pattern still resolves against `configPatch`, and another walks every `mcpServerPatch` field, failing if it's neither covered by a `mcp.servers.*` pattern nor in a small named exception set (`enabled`, `connect_timeout`, `allowed_tools`, `blocked_tools`, `sub_agents`). A newly added MCP server field fails CI until it's explicitly classified as security-relevant or added to the exception set.

## Stable handler and prompt-cache identities

The advisor tool definition is registered once for the session when the advisor is enabled. Its per-run and per-child limits are checked in shared handler state instead of by removing or changing the tool definition between turns. This keeps the registry and the prompt prefix stable while still enforcing the configured budgets. The advisor's model alias is resolved from the selected profile.

Profile and model selection has two separate paths. The selected profile resolves role assignments first. `STEINER_MODEL` and then `--model` override only the active orchestrator model; advisor, sub-agent, oneshot, and workflow-handoff roles continue to resolve from the profile. In the TUI, `/profile` changes future role assignments and the profile fallback while preserving the active orchestrator selection, conversation, and prompt-cache identity. An exact configured alias wins before provider-prefix parsing; otherwise the longest configured provider prefix is used.

The model catalog and provider layers preserve that distinction when building requests. A configured `advanced.transport` override wins first. With `auto`, models.dev metadata may select the effective transport, and the configured provider type is the fallback when metadata is absent. The resolved effective provider type, transport, and reason are exposed by `steiner model inspect`; this is the implementation path behind the user-visible selection rules, not another configuration precedence layer.

Prompt-cache identity is kept separate from the active model override. Profile changes preserve the current cache identity because they change future role resolution rather than rewriting the current conversation's static prefix. The provider-specific cache mechanisms remain responsible for the final wire behavior.

### Model metadata resolution

Steiner resolves each model fact (context window, max output tokens, vision, reasoning efforts, reasoning echo-back, transport) independently, in `internal/provider`'s fact resolver (`fact_resolver.go`, `fact_source_*.go`). Each fact is answered by the first source, in precedence order, that can answer it; lower-precedence sources are never consulted once a higher-precedence source has answered a given field (lazy per-field resolution — a fully-configured model never triggers a models.dev cache load for its limits, though it may still trigger one for unconfigured facts like vision or reasoning efforts).

models.dev cache data (`internal/metadata`) is parsed once into an `Index` (`ParseIndex`), not re-parsed per lookup; `Index.LookupProvider` walks the already-parsed maps. Loading is also lazy at the resolution level: `internal/provider`'s `modelsDevSource` holds a shared `modelsDevLoader` that reads and parses the cache file (and refreshes it over the network if stale) on first use only, via `sync.Once`.

Resolution is session-scoped through `provider.Resolver` (`NewResolver`/`Resolve`/`Invalidate`): one `Resolver` per interactive session or CLI run (a throwaway `Resolver` for one-shot commands like `steiner model inspect`) holds a single `modelsDevLoader` for its whole lifetime, so the cache is touched at most once across every resolution the session makes, not once per call. `Resolver.Resolve` memoizes successful resolutions keyed by reference plus a fingerprint of the resolved `ModelConfig`/`ProviderConfig`, so a mid-session config edit for the same alias is picked up as a fresh resolution; concurrent calls for the same key are coalesced (single-flight) rather than resolved redundantly. Failed resolutions are never memoized. `Invalidate` drops all memoized entries (e.g. after a config reload) without discarding the loaded models.dev index.

Per-field precedence, as of this stage:

| Fact | Order |
|---|---|
| Context window / max output tokens | explicit config → provider model catalog → live provider probe (Ollama only) → models.dev → conservative fallback (32768/4096); Codex catalog context selects `max_context_window` only for `advanced.codex.use_max_context_window: true` and positive max, otherwise `context_window` |
| Vision | config → models.dev → unknown |
| Reasoning efforts | config → provider model catalog → models.dev → built-in OpenAI/Codex family table → unknown |
| Reasoning echo-back | config → models.dev → unknown (false) |
| Transport | config override → provider-fixed (Codex) → models.dev npm-based override → configured provider type |

Provider model catalog participation (`catalogSource` in `fact_source_catalog.go`) answers context window, max output tokens, and reasoning efforts from `internal/modelcatalog`'s cache, ahead of the Ollama live probe and models.dev. `internal/provider` cannot import `internal/modelcatalog` (package boundary), so the lookup is defined as the `provider.ModelCatalog` interface and injected via `ResolverOptions.Catalog`; the concrete adapter (`cmd/steiner/catalog_metadata.go`) wraps `modelcatalog.Service` and snapshots `*config.Config` at construction time (mirroring how `modelCatalogEndpoints` is derived once in `buildModelCatalogService`), not a live config getter. A cache miss or a nil catalog leaves the fields unknown, falling through the rest of the precedence order exactly as before catalogSource existed. Ollama's live HTTP probe (`fact_source_probe.go`) is unaffected; OpenRouter's former live probe was retired in favor of catalog data, since the catalog already carries the same numbers without a live network round trip.

Generic provider profiles (`openai_compat`, `ollama`, `litellm` — no fixed `ModelsDevID`) resolve models.dev facts differently from named providers. `modelsDevSource` first tries a strict `Index.LookupProvider` using the provider's configured alias as the models.dev provider ID, for users who happen to name an alias after a real models.dev provider key (e.g. an `openai_compat` provider aliased `openrouter`). Only if that strict lookup comes back `not_found` or `provider_mismatch` does it fall back to `Index.LookupMerged`, which scans every provider in the index for the model and merges every non-malformed entry it finds: the minimum of each provider's positive context window and max output tokens, `AND` across vision and reasoning echo-back, an interleaved-field value only kept when every entry agrees, and the intersection of reasoning-supported-efforts preserving the first entry's order. A merged result answers at `"low"` confidence with a note recording how many providers were merged, and never contributes a transport override (npm/api provenance is cleared, so there's nothing to base one on). A malformed strict result is never overridden by the merge fallback. Named providers with a fixed `ModelsDevID` are unaffected by any of this and keep the single-provider `LookupProvider` behavior described above.

Every resolved fact carries its own provenance (`provider.ModelFacts`, exposed as `ResolvedModel.Facts` and printed by `steiner model inspect`'s `facts:` block): a value, whether it's known, its source, a confidence level, and an optional note (e.g. a models.dev lookup degradation reason). The legacy `MetadataSource`/`Confidence` fields on `ResolvedModel` continue to describe limits provenance only, for backward compatibility with existing consumers.

Warnings are derived once, from the final resolved facts, not from individual lookups that lost precedence to a higher-priority source. Only two situations produce a warning: a source that was actually consulted failed outright (e.g. the models.dev cache could not be loaded at all), or the context window ended up on the conservative fallback default. A models.dev lookup that returns `provider_mismatch`/`not_found` for a fact that another source already answered (e.g. limits fully configured) produces no warning — it remains visible only as a `note` in `model inspect`'s `facts:` block. This reverses the pre-stage-B behavior of warning on every degraded models.dev lookup regardless of whether it mattered.

## Logging handler

When file logging is enabled, Steiner installs a process-wide `slog` handler at the configured level. It writes to a sibling `*.slog` file next to the session log, or discards records when no session log is configured. The interactive TUI does not receive `slog` output on stderr. The session log is JSONL, starts each run with a `log_started` record, and is size-capped and rotated. The user page documents the fields and defaults; this section records the handler and file-routing behavior.

## Diagnostics implementation

Diagnostics are independent of `logging`. When disabled, no writer or directory is constructed. When enabled, the writer creates a user-state directory, not a project-root path, and maintains one JSONL stream per enabled category (`cache`, `provider`, and `tool`). Files use mode `0o600` in a directory with mode `0o700`; records are appended across runs, size-capped, rotated as `<file>.1`, `<file>.2`, and so on, and old records are removed according to `retention_days`. Each record carries `run_id`, `build_sha`, and `dirty`, allowing comparisons to select a build rather than infer one from wall-clock time. `capture_bodies` changes bounded scalar capture into full message, tool, and block content and also makes session-log `api_request` records unbounded, so it can capture prompts. Tool-stream records also carry `source` (`parent` or `sub_agent`), `agent_id`, `agent_type` and `turn`, plus `payload.model`, attributing each record to the agent and model that issued the call; these fields are absent on paths that do not set them.

### Analyzing diagnostics

`scripts/diagnostics.mjs` aggregates the `cache`, `provider`, and `tool` streams for hit rates, retry rates, latency percentiles, and failure reasons without printing individual records. `--compare <shaA> <shaB>` compares builds because each record carries `build_sha`. `prefix <logfile>` reads a session log and reports whether each turn grew append-only or rewrote the prefix (`BREAK-AT-N`). The script header contains its usage and mode reference; `make test-scripts` runs its smoke tests.

The `mutate` mode (`scripts/diagnostics_mutate.mjs`) reads the `tool` and `provider` streams and reports call/ops metrics, message grouping, failure reasons, a feature report and a cause taxonomy. An assistant message is a maximal run of tool records with the same `(run_id, source, agent_id)`, in `seq` order, with the same `turn`; a new message starts whenever `turn` changes, and a turn number that repeats after `/clear` starts a new message rather than rejoining an earlier one. Runs whose records lack `turn` fall back to the pre-attribution rule (the mutate records between two provider records) and are labelled approximate.

Failed `replace` operations with reason `no_match`, `ambiguous_match` or `stale_read` carry `failures[].match`, a fixed set of scalar features: `old_bytes`, `old_lines`, `nonblank_lines`, `lines_found`, `longest_run`, `exact_prefix_lines`, `trim_prefix_lines`, `ws_kind`, `indent_delta_max`, `tabs_vs_spaces`, `crlf_mismatch`, `unescape_matches`, `line_prefix`, `matches_original`, `match_count`, `read_state`, `turns_since_read`, `in_read_range`, `locus_line`, `file_lines`, `file_hash_supplied` and `truncated`. The field definitions live in `internal/tool/builtin/mutate_match_features.go`. Under `capture_bodies` the same failures also carry `failures[].sample`, the attempted `old_string` and a bounded file region.

`coldturns` joins the `cache` and `tool` streams by time to identify turns that read nothing from cache, such as long delegated calls, prefix rewrites, or idle time. It requires both streams. See [cache statistics internals](cache-stats.md#diagnostics-and-analysis-machinery) for how to interpret the report.

## MCP test boundary

MCP behavior is covered by hermetic, CI-safe integration tests under `internal/mcp/` for both stdio and HTTP transports through the manager path. Live validation against third-party MCP servers remains manual work tracked in #438. The user-facing MCP behavior and approval rules are in [MCP](../user/mcp.md).
