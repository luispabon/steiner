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

## Stable handler and prompt-cache identities

The advisor tool definition is registered once for the session when the advisor is enabled. Its per-run and per-child limits are checked in shared handler state instead of by removing or changing the tool definition between turns. This keeps the registry and the prompt prefix stable while still enforcing the configured budgets. The advisor's model alias is resolved from the selected profile.

Profile and model selection has two separate paths. The selected profile resolves role assignments first. `STEINER_MODEL` and then `--model` override only the active orchestrator model; advisor, sub-agent, oneshot, and workflow-handoff roles continue to resolve from the profile. In the TUI, `/profile` changes future role assignments and the profile fallback while preserving the active orchestrator selection, conversation, and prompt-cache identity. An exact configured alias wins before provider-prefix parsing; otherwise the longest configured provider prefix is used.

The model catalog and provider layers preserve that distinction when building requests. A configured `advanced.transport` override wins first. With `auto`, models.dev metadata may select the effective transport, and the configured provider type is the fallback when metadata is absent. The resolved effective provider type, transport, and reason are exposed by `steiner model inspect`; this is the implementation path behind the user-visible selection rules, not another configuration precedence layer.

Prompt-cache identity is kept separate from the active model override. Profile changes preserve the current cache identity because they change future role resolution rather than rewriting the current conversation's static prefix. The provider-specific cache mechanisms remain responsible for the final wire behavior.

## Logging handler

When file logging is enabled, Steiner installs a process-wide `slog` handler at the configured level. It writes to a sibling `*.slog` file next to the session log, or discards records when no session log is configured. The interactive TUI does not receive `slog` output on stderr. The session log is JSONL, starts each run with a `log_started` record, and is size-capped and rotated. The user page documents the fields and defaults; this section records the handler and file-routing behavior.

## Diagnostics implementation

Diagnostics are independent of `logging`. When disabled, no writer or directory is constructed. When enabled, the writer creates a user-state directory, not a project-root path, and maintains one JSONL stream per enabled category (`cache`, `provider`, and `tool`). Files use mode `0o600` in a directory with mode `0o700`; records are appended across runs, size-capped, rotated as `<file>.1`, `<file>.2`, and so on, and old records are removed according to `retention_days`. Each record carries `run_id`, `build_sha`, and `dirty`, allowing comparisons to select a build rather than infer one from wall-clock time. `capture_bodies` changes bounded scalar capture into full message, tool, and block content and also makes session-log `api_request` records unbounded, so it can capture prompts.

### Analyzing diagnostics

`scripts/diagnostics.mjs` aggregates the `cache`, `provider`, and `tool` streams for hit rates, retry rates, latency percentiles, and failure reasons without printing individual records. `--compare <shaA> <shaB>` compares builds because each record carries `build_sha`. `prefix <logfile>` reads a session log and reports whether each turn grew append-only or rewrote the prefix (`BREAK-AT-N`). The script header contains its usage and mode reference; `make test-scripts` runs its smoke tests.

`coldturns` joins the `cache` and `tool` streams by time to identify turns that read nothing from cache, such as long delegated calls, prefix rewrites, or idle time. It requires both streams. See [cache statistics internals](cache-stats.md#diagnostics-and-analysis-machinery) for how to interpret the report.

## MCP test boundary

MCP behavior is covered by hermetic, CI-safe integration tests under `internal/mcp/` for both stdio and HTTP transports through the manager path. Live validation against third-party MCP servers remains manual work tracked in #438. The user-facing MCP behavior and approval rules are in [MCP](../user/mcp.md).
