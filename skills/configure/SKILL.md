---
name: configure
description: Safely inspect and edit Steiner YAML configuration with focused scope, minimal textual mutations, secret handling, validation, and restart guidance.
---

Use this skill when a user asks to understand or change Steiner configuration. Access limits below are instructions for this workflow, not policy guarantees.

**Establish outcome and scope.** Ask what result the user wants, whether it applies to the current project or all projects, and which setting or provider is involved. Treat `.steiner/config.yaml` as project scope. Treat `~/.config/steiner/config.yaml` as global scope. Global changes are limited to that exact path; never target tokens, global skills, or secrets. Do not invent another config path. Preserve the existing `/config` compiled-config modal.

**Ask focused questions.** Resolve only questions needed for the requested change: target scope, exact YAML path, desired value, and whether an environment-variable reference is available for sensitive values. Do not read the full config when a focused path or user-provided excerpt is enough.

**Handle secrets safely.** Never inspect or reveal tokens, global skills, or existing secrets. Obtain consent before any full read that may contain secrets. Prefer an environment reference such as `api_key_env` or `${VAR}`. Write a literal secret only after the user confirms that choice; never echo the literal or an existing secret in a response, diff, log, or command. Redact secret-bearing resolved output by default and obtain separate consent before showing it in full.

**Edit minimally.** Make the smallest YAML text mutation for the requested path. Preserve comments, formatting, ordering, and unrelated settings. Use `mutate`, never `bash`, to edit files. For global edits, use the existing exact-path mutate approval; there is no sandbox exception. Do not claim this skill enforces filesystem or secret access policy. Report changed paths and explain precedence when both scopes may work.

**Validate and close.** After the user consents to validation, run `steiner config >/dev/null` as the safe validation command. Report any load errors emitted on stderr, and explain that this validates the effective merged configuration chain for the invocation directory, not one file alone. Do not print resolved configuration unless the user consents and sensitive values are redacted, or the user explicitly consents to a full secret-bearing read. Tell the user that a restart of `steiner` is required for configuration changes to take effect.

## Configure Skill Reference

Compact canonical reference for safe configuration edits.

**Targets/precedence.** Project: `.steiner/config.yaml` or `--config <path>`; global: `~/.config/steiner/config.yaml`. Precedence: defaults, global, project, env, CLI; later wins; project needs trust. `--profile <name>` selects a profile; `STEINER_MODEL`, then `--model <ref>`, select the model; `--verbose` enables verbose logging; `--unsafe` disables `sandbox.enabled`. Expansion: `${VAR}`, `${VAR:-default}`, `$VAR`, `$$`; undefined fails except `${VAR:-}`.

**Environment.** `STEINER_MODEL` -> model; `STEINER_SUB_AGENTS_MAX_PARALLEL` -> `sub_agent.max_parallel`; `STEINER_TUI_FPS` -> `tui.fps`; `STEINER_MAX_TURNS`, `STEINER_MAX_TOKENS`, `STEINER_TOOL_OUTPUT_MAX_BYTES`, `STEINER_MAX_PARALLEL_TOOLS` -> `limits`; `STEINER_LOG_LEVEL`, `STEINER_LOG_FILE`, `STEINER_COMPACTION_LOG_FILE` -> `logging`. Search keys `KAGI_API_KEY`, `BRAVE_API_KEY` fill empty fields. Integer overrides parse.

**Path notation.** `<name>` is a map key; `<index>` is a list index. `unset` means no value.

|Path|Type|Default|Semantics|
|-|-|-|-|
|`providers.<name>.type`|string|unset|Required. `openai_compat`, `ollama`, `lmstudio`, `openrouter`, `openai`, `anthropic`, `gemini`, `litellm`, `codex`, `opencode_go`, `opencode_zen`, `claude_subscription`; `gemini` validates but is unsupported at runtime.|
|`providers.<name>.base_url`|string|per type|Required for `openai_compat`, `ollama`, `lmstudio`, and `litellm`. Defaults exist for `openai`, `anthropic`, `openrouter`, `opencode_go`, and `opencode_zen`.|
|`providers.<name>.api_key`|string|unset|Credential; prefer `api_key_env`. Required for providers that need credentials.|
|`providers.<name>.api_key_env`|string|unset|Environment variable containing the credential.|
|`providers.<name>.headers.<key>`|string|unset|One extra header value.|
|`providers.<name>.timeout`|duration|unset|Overall request timeout; unset has no overall limit. The default transport has a separate 30s response-header timeout.|
|`providers.<name>.compaction_fallback_after`|duration|type default|Cold-cache delay; `0` disables.|
|`providers.<name>.codex.min_request_interval`|duration|`0s`|Positive values pace Codex requests.|
|`providers.<name>.codex.transport`|string|`http`|`http` or experimental `websocket`; no HTTP fallback.|
|`models.discovery_enabled`|bool|`true`|Discover provider models; false uses configured entries only.|
|`models.definitions.<alias>.provider`|string|unset|Provider name.|
|`models.definitions.<alias>.id`|string|unset|Model ID.|
|`models.definitions.<alias>.params`|map[string]any|unset|Request params.|
|`models.definitions.<alias>.extra_params`|map[string]any|unset|Provider params.|
|`models.definitions.<alias>.prompt_suffix`|string|unset|Appended to each user message.|
|`models.definitions.<alias>.retry.enabled`|bool|`true`|Retry transient and rate-limit errors.|
|`models.definitions.<alias>.retry.max_attempts`|int|`5`|Total attempts; at least 1.|
|`models.definitions.<alias>.retry.initial_backoff`|duration|`250ms`|First retry wait.|
|`models.definitions.<alias>.retry.max_backoff`|duration|`5s`|Exponential cap; at least initial.|
|`models.definitions.<alias>.retry.retry_after_max`|duration|`60s`|Maximum Retry-After wait.|
|`models.definitions.<alias>.prompts.system`|string|unset|Replaces the default system prompt.|
|`models.definitions.<alias>.prompts.system_suffix`|string|unset|Appends to the default system prompt.|
|`models.definitions.<alias>.prompts.compaction`|string|unset|Replaces the compaction prompt.|
|`models.definitions.<alias>.advanced.limits.context_window`|int|metadata|Context window; explicit value wins.|
|`models.definitions.<alias>.advanced.limits.compaction_threshold_tokens`|int|70% of context window|Compaction trigger; positive and below the resolved window.|
|`models.definitions.<alias>.advanced.codex.use_max_context_window`|bool|`false`|Codex only; use catalog max context when positive.|
|`models.definitions.<alias>.advanced.limits.max_output_tokens`|int|metadata|Output-token ceiling.|
|`models.definitions.<alias>.advanced.reasoning_echo_back`|bool or null|unset|Provider reasoning echo control.|
|`models.definitions.<alias>.advanced.transport`|string|`auto`|`auto`, `openai_compat`, or `anthropic`.|
|`models.definitions.<alias>.advanced.reasoning.effort`|string|unset|Native reasoning effort.|
|`models.definitions.<alias>.advanced.reasoning.supported_efforts`|[]string|unset|Native efforts for validation and selection.|
|`models.definitions.<alias>.vision`|bool or null|null|Null assumes vision; false removes images.|
|`models.profiles.<profile>.default_model`|string|unset|Required in every declared profile.|
|`models.profiles.<profile>.advisor`|string|unset|Advisor model.|
|`models.profiles.<profile>.compaction_fallback`|string|unset|Cold-cache compaction model; needs a positive provider fallback delay.|
|`models.profiles.<profile>.sub_agents.<key>`|string|unset|Model by supported sub-agent type.|
|`models.profiles.<profile>.oneshot.<key>`|string|unset|Model by `plan`, `implement`, or `review`.|
|`models.profiles.<profile>.workflow_handoff.<key>`|string|unset|Model by `implement`, `review`, or `build`.|
|`limits.max_turns`|int|`50`|Maximum turns; non-negative.|
|`limits.max_tokens`|int|`500000`|Output (completion) tokens only; at least 1.|
|`limits.model_call_timeout`|duration|`10m`|Single model-call limit; `0` disables it.|
|`limits.tool_timeout_default`|duration|`30s`|MCP fallback for tools without a `tool_timeouts` entry; positive.|
|`limits.tool_timeouts.<tool>`|duration|per tool|Per-tool override; positive. `bash` default and max `600s`; `grep` `30s` (cooperative); `read` ignored.|
|`limits.tool_output_max_bytes`|int|`65536`|Captured output per tool call; at least 1.|
|`limits.max_parallel_tools`|int|`4`|Parallel-safe built-in, LSP, and MCP read-only-hint tools; at least 1.|
|`sandbox`|block|see fields|Bubblewrap settings.|
|`sandbox.enabled`|bool|`true`|Enable bubblewrap; `--unsafe` forces false.|
|`sandbox.warning_on_unsupported_platform`|bool|`true`|Warn when unavailable or bypassed.|
|`sandbox.env_passthrough`|[]string|`[]`|Extra env names; trailing `*` is a prefix.|
|`sandbox.env_passthrough_all`|bool|`false`|Pass all host env, including credentials; always warns.|
|`sandbox.bind_host_cache`|bool|`false`|Bind real `~/.cache` read-write.|
|`sandbox.host_mounts.<index>.path`|string|unset|Host path; `~` expands.|
|`sandbox.host_mounts.<index>.mode`|string|unset|`ro` or `rw`; default is read-only.|
|`permissions.docker`|bool|`false`|Allow Docker socket access.|
|`sub_agent.enabled`|bool|`true`|Enable child agents.|
|`sub_agent.orchestration_level`|string|`standard`|`low` or `standard`.|
|`sub_agent.max_turns`|int|`120`|Child turn cap; at least 15 when enabled.|
|`sub_agent.max_tokens`|int|`400000`|Child output-token cap; at least 1.|
|`sub_agent.max_parallel`|int|`3`|Session-wide running cap; at least 1.|
|`sub_agent.max_follow_ups`|int|`5`|Follow-up resumes per child; at least 1.|
|`advisor.enabled`|bool|`false`|Enable advisor.|
|`advisor.max_uses_per_run`|int|`3`|Run cap; at least 1 when enabled.|
|`advisor.max_uses_per_sub_agent`|int|`1`|Child cap; at least 1 when enabled.|
|`advisor.max_tokens`|int or null|nil|Optional positive output-token ceiling.|
|`advisor.timeout`|duration or null|`180s`|Optional positive HTTP timeout.|
|`oneshot.auto_pr`|bool|`false`|Allow closeout to push and open a PR or MR.|
|`desktop_notifications.enabled`|bool|`false`|Interactive TUI only. Notifications are for approval required and workflow handoff requested.|
|`desktop_notifications.duration`|int|`0`|0 persists; positive auto-dismisses; negative invalid.|
|`update_check.enabled`|bool|`true`|Interactive TUI startup check only.|
|`update_check.interval_hours`|int|`6`|Hours between checks; non-negative.|
|`tools.<tool>.exec`|string|unset|Executable; required.|
|`tools.<tool>.subcommand`|string|unset|First executable argument.|
|`tools.<tool>.description`|string|unset|Description shown to the model.|
|`tools.<tool>.parameters`|map[string]any|unset|JSON Schema input data.|
|`tools.<tool>.timeout`|duration|unset|Required custom-tool timeout; positive; bounds each call, and `limits` timeouts do not apply. Script contract: [Tools](../../docs/user/tools.md).|
|`project_context.max_bytes`|int|`8000`|Extra-context byte budget; at least 1.|
|`project_context.max_tokens`|int|unset|Deprecated alias; converts to bytes when max_bytes is unset.|
|`project_context.extra_files`|[]string|unset|Root-relative context files; directories are skipped; `~` expands.|
|`project_context.ignore_files`|[]string|unset|Exact path, basename, or path-suffix match; directories do not match; `~` expands.|
|`paths.project_root_only`|bool|`true`|Confine tool paths to the project root.|
|`paths.writable_paths`|[]string|`[]`|Extra writable paths, inside or outside root; `~` expands.|
|`paths.blocked_paths`|[]string|`[]`|Always-denied paths; `~` expands.|
|`paths.exclude_paths`|[]string|unset|Excluded from walks and searches. See [Tools](../../docs/user/tools.md).|
|`paths.exclude_patterns`|[]string|unset|Excluded glob patterns. See [Tools](../../docs/user/tools.md).|
|`logging.enabled`|bool|`false`|Enable file logging.|
|`logging.level`|string|`info`|`trace`, `debug`, `info`, `warn`, or `error`.|
|`logging.file`|string|default path|Log path; may contain prompts and tool output.|
|`logging.thinking_chunk`|bool|`false`|Include reasoning chunks.|
|`logging.assistant_chunk`|bool|`false`|Include streamed assistant chunks.|
|`logging.compaction_log_file`|string|unset|Separate compaction log.|
|`diagnostics.enabled`|bool|`false`|Enable structured diagnostics.|
|`diagnostics.dir`|string|XDG state path|JSONL directory; rejected inside project root.|
|`diagnostics.retention_days`|int|`30`|Drop older records; positive when enabled.|
|`diagnostics.streams.cache`|bool|`false`|Prompt-cache stream.|
|`diagnostics.streams.provider`|bool|`false`|Provider-call stream.|
|`diagnostics.streams.tool`|bool|`false`|Tool and delegation stream.|
|`diagnostics.streams.tui`|bool|`false`|TUI timing stream.|
|`diagnostics.streams.internal`|bool|`false`|TUI internal error stream.|
|`diagnostics.capture_bodies`|bool|`false`|Capture full message, tool, block, and prompt content.|
|`context_management.read_annotations`|bool|`true`|Annotate identical full reads in carried context.|
|`search.backend`|string|unset|`searxng`, `kagi`, or `brave`.|
|`search.searxng_url`|string|unset|Required for `searxng`.|
|`search.kagi_api_key`|string|unset|Required for `kagi`; prefer env storage.|
|`search.brave_api_key`|string|unset|Required for `brave`; prefer env storage.|
|`mcp.enabled`|bool|`true`|Enable MCP client.|
|`mcp.servers.<server>.enabled`|bool|`false`|Start server.|
|`mcp.servers.<server>.transport`|string|`stdio`|`stdio` or `http`.|
|`mcp.servers.<server>.command`|string|unset|Required for stdio; empty for http.|
|`mcp.servers.<server>.args`|[]string|unset|Stdio arguments; empty for http.|
|`mcp.servers.<server>.env`|map[string]string|unset|Stdio process environment; empty for http.|
|`mcp.servers.<server>.env.<key>`|string|unset|One environment variable.|
|`mcp.servers.<server>.url`|string|unset|Required for http; empty for stdio.|
|`mcp.servers.<server>.headers`|map[string]string|unset|HTTP headers for http; empty for stdio.|
|`mcp.servers.<server>.headers.<key>`|string|unset|One HTTP header.|
|`mcp.servers.<server>.approval`|string|`ask`|`ask`, `allow`, or `deny`; allow becomes ask in plan mode.|
|`mcp.servers.<server>.trust_annotations`|bool|`false`|Read-only annotated tools may skip approval.|
|`mcp.servers.<server>.connect_timeout`|duration|`15s`|Zero uses 15s; only negative values are invalid.|
|`mcp.servers.<server>.allowed_tools`|[]string|unset|Native-name allowlist; `[]` denies all.|
|`mcp.servers.<server>.blocked_tools`|[]string|unset|Native-name denylist after allowlist.|
|`mcp.servers.<server>.sub_agents`|[]string|closed|Allowed sub-agent types.|
|`lsp.enabled`|bool|`false`|Enable LSP tools.|
|`lsp.idle_timeout`|duration|`5m`|Stop idle server; positive.|
|`lsp.request_timeout`|duration|`10s`|One request timeout; positive.|
|`lsp.ready_timeout`|duration|`30s`|Server readiness wait; positive.|
|`lsp.ready_grace_period`|duration|`2s`|Readiness grace period; positive.|
|`lsp.diagnostics_window`|duration|`2s`|Diagnostics collection window; positive.|
|`lsp.max_results`|int|`200`|Maximum navigation results; positive.|
|`lsp.cache_dir`|string|unset|Server state directory; `~` expands.|
|`lsp.servers.<name>.enabled`|bool|`false`|Enable this server.|
|`lsp.servers.<name>.command`|string|unset|Required server executable.|
|`lsp.servers.<name>.args`|[]string|unset|Command arguments.|
|`lsp.servers.<name>.env.<name>`|string|unset|Extra server environment variable.|
|`lsp.servers.<name>.file_extensions`|[]string|unset|Required non-empty extensions.|
|`lsp.servers.<name>.root_markers`|[]string|unset|Required non-empty workspace markers.|
|`lsp.servers.<name>.initialization_options`|map[string]any|unset|Server initialise options.|
|`modes.default`|string|`build`|`plan` or `build`; plan writes only `.steiner/plans` and `.steiner/security`. See [Execution modes](../../docs/user/working-with-steiner.md#plan-and-build-modes).|
|`tui.fps`|int|`60`|Interactive renderer rate, 1 through 120.|
|`tui.quota_refresh_interval`|duration|`5m`|Post-run usage refetch age; zero disables.|

`steiner config` validates and prints resolved configuration. Resolved output can contain credentials. Changes require a restart.
