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

Sole canonical compact reference for safe configuration edits; use this file as the source of truth.

**Targets/precedence.** Project: `.steiner/config.yaml` or `--config <path>`; global: `~/.config/steiner/config.yaml`. Order: defaults, global YAML, project YAML, env, CLI; later wins. `--profile <name>` selects a profile; `STEINER_MODEL`, then `--model <ref>`, select the active model; `--verbose` enables verbose logging; `--unsafe` forces `sandbox.enabled: false`. Scalar expansion: `${VAR}`, `${VAR:-default}`, `$VAR`, `$$`; undefined variables fail except `${VAR:-}`. Keys/comments are unchanged.

**Environment.** `STEINER_MODEL` -> active model; `STEINER_SUB_AGENTS_MAX_PARALLEL` -> `sub_agent.max_parallel`; `STEINER_TUI_FPS` -> `tui.fps`; `STEINER_MAX_TURNS`, `STEINER_MAX_TOKENS`, `STEINER_TOOL_OUTPUT_MAX_BYTES`, `STEINER_MAX_PARALLEL_TOOLS` -> matching `limits` fields; `STEINER_LOG_LEVEL`, `STEINER_LOG_FILE`, `STEINER_COMPACTION_LOG_FILE` -> matching `logging` fields. `GOOGLE_SEARCH_CX`, `GOOGLE_SEARCH_API_KEY`, `KAGI_API_KEY`, `BRAVE_API_KEY` fill empty matching `search` fields. Integer overrides must parse as integers.

**Path notation.** `<name>`, `<alias>`, `<profile>`, `<server>`, `<tool>`, `<key>` are map keys; `<index>` is a list index. `duration` is a Go duration. `—` means unset/required/conditional. Defaults are compiled; profile maps inherit from `models.profiles.default` where stated.

|Path|Type|Default|Semantics|
|-|-|-|-
| `providers.<name>.type`|string|—|`openai_compat`, `ollama`, `lmstudio`, `openrouter`, `openai`, `anthropic`, `gemini`, `litellm`, `codex`, `opencode_go`, `opencode_zen`. `gemini` passes validation but is unimplemented. |
| `providers.<name>.base_url`|string|local: `http://localhost:11434/v1`|API endpoint; required for `openai_compat`, `ollama`, `lmstudio`, `litellm`. |
| `providers.<name>.api_key`|string|—|Literal credential; prefer `api_key_env`. Required unless `api_key_env` set. |
| `providers.<name>.api_key_env`|string|—|Environment variable containing credential. |
| `providers.<name>.headers`|map[string]string|—|Extra provider HTTP headers. |
| `providers.<name>.headers.<key>`|string|—|One extra header value. |
| `providers.<name>.timeout`|duration|local: `30s`|Per-request timeout. |
| `providers.<name>.codex.min_request_interval`|duration|`0s`|Min gap between Codex requests; positive enables pacing. |
| `providers.<name>.codex.transport`|string|`http`|`http` or `websocket`; websocket is experimental and has no HTTP fallback. |
| `models.discovery_enabled`|bool|`true`|Discover provider models, or use configured entries only when false. |
| `models.definitions.<alias>.provider`|string|`local`|Provider name. |
| `models.definitions.<alias>.id`|string|`qwen3-35b-a3b`|Model ID. |
| `models.definitions.<alias>.params`|map[string]any|—|Request parameters. |
| `models.definitions.<alias>.params.<key>`|any|—|One standard request parameter. |
| `models.definitions.<alias>.extra_params`|map[string]any|—|Provider parameters. |
| `models.definitions.<alias>.extra_params.<key>`|any|—|One provider-specific parameter. |
| `models.definitions.<alias>.prompt_suffix`|string|—|Appended to each user message. |
| `models.definitions.<alias>.retry.enabled`|bool|`true`|Retry transient or rate-limit errors. |
| `models.definitions.<alias>.retry.max_attempts`|int|`5`|Total attempts; at least 1. |
| `models.definitions.<alias>.retry.initial_backoff`|duration|`250ms`|First retry wait. |
| `models.definitions.<alias>.retry.max_backoff`|duration|`5s`|Exponential cap; not below initial backoff. |
| `models.definitions.<alias>.retry.retry_after_max`|duration|`60s`|Max `Retry-After` wait; not below initial backoff. |
| `models.definitions.<alias>.prompts.system`|string|—|Replaces default system prompt. |
| `models.definitions.<alias>.prompts.system_suffix`|string|—|Appends after default system prompt. |
| `models.definitions.<alias>.prompts.compaction`|string|—|Replaces compaction prompt. |
| `models.definitions.<alias>.advanced.limits.context_window`|int|`32768`|Context window in tokens. |
| `models.definitions.<alias>.advanced.limits.max_output_tokens`|int|`8192`|Per-response output-token ceiling. |
| `models.definitions.<alias>.advanced.reasoning_echo_back`|bool or null|—|Provider reasoning echo control. |
| `models.definitions.<alias>.advanced.transport`|string|`auto`|`auto`, `openai_compat`, or `anthropic`; explicit values override metadata. |
| `models.definitions.<alias>.advanced.reasoning.effort`|string|—|Native reasoning effort. |
| `models.definitions.<alias>.advanced.reasoning.supported_efforts`|[]string|—|Native efforts used for validation and selection. |
| `models.definitions.<alias>.vision`|bool or null|null|Null assumes vision; false strips images. |
| `models.profiles.<profile>.default_model`|string|default: `default`|Default model and role fallback; required for default profile. |
| `models.profiles.<profile>.advisor`|string|—|Advisor model. |
| `models.profiles.<profile>.sub_agents`|map[string]string|—|Agent models. |
| `models.profiles.<profile>.sub_agents.<key>`|string|—|Model by `explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, or `vision`. |
| `models.profiles.<profile>.oneshot`|map[string]string|—|Phase models. |
| `models.profiles.<profile>.oneshot.<key>`|string|—|Model by `plan`, `implement`, or `review`; missing uses profile default. |
| `models.profiles.<profile>.workflow_handoff`|map[string]string|—|Destination models. |
| `models.profiles.<profile>.workflow_handoff.<key>`|string|—|Model by `implement`, `review`, or `build`; missing uses profile default. |
| `limits.max_turns`|int|`50`|Maximum turns; non-negative. |
| `limits.max_tokens`|int|`500000`|Total input plus output tokens; at least 1. |
| `limits.model_call_timeout`|duration|`10m`|Model-call limit. |
| `limits.tool_timeout_default`|duration|`30s`|Default tool and MCP timeout; positive. |
| `limits.tool_timeouts.<tool>`|duration|—|One per-tool timeout override. |
| `limits.tool_output_max_bytes`|int|`65536`|Captured output limit per tool call; at least 1. |
| `limits.max_parallel_tools`|int|`4`|Concurrent ordinary tools; >=1. |
| `sandbox`|block|see fields below|Bubblewrap sandbox settings. |
| `sandbox.enabled`|bool|`true`|Enable bubblewrap; `--unsafe` forces false. |
| `sandbox.warning_on_unsupported_platform`|bool|`true`|Warn if sandbox is unavailable or bypassed. |
| `sandbox.env_passthrough`|[]string|`[]`|Extra environment names; only trailing `*` is a prefix wildcard. |
| `sandbox.env_passthrough_all`|bool|`false`|Disable environment filtering, including credential filtering. |
| `sandbox.host_mounts.<index>.path`|string|—|Host path; `~` expands. |
| `sandbox.host_mounts.<index>.mode`|string|—|`ro` or `rw`; host paths are read-only by default. |
| `permissions.docker`|bool|`false`|Allow sandboxed tools to reach Docker socket; otherwise masked. |
| `sub_agent.enabled`|bool|`true`|Enable child agents. |
| `sub_agent.max_turns`|int|`30`|Per-child turns; runtime floor 15. |
| `sub_agent.max_tokens`|int|`100000`|Per-child token limit. |
| `sub_agent.max_parallel`|int|`3`|Concurrent delegation calls; at least 1. |
| `advisor.enabled`|bool|`false`|Enable advisor. |
| `advisor.max_uses_per_run`|int|`3`|Per-session advisor cap; at least 1 when enabled. |
| `advisor.max_uses_per_sub_agent`|int|`1`|Per-child advisor cap; at least 1 when enabled. |
| `advisor.max_tokens`|int or null|nil|Optional positive advisor output-token ceiling. |
| `advisor.timeout`|duration or null|`180s`|Optional positive advisor-only HTTP timeout. |
| `oneshot.auto_pr`|bool|`false`|Allow oneshot closeout to push and open a PR/MR. |
| `desktop_notifications.enabled`|bool|`false`|Enable desktop notifications. |
| `desktop_notifications.duration`|int|`0`|0 persists, positive auto-dismisses, negative invalid. |
| `update_check.enabled`|bool|`true`|Enable startup update check. |
| `update_check.interval_hours`|int|`6`|Hours between checks; non-negative. |
| `tools.<tool>.exec`|string|—|Executable; required. |
| `tools.<tool>.subcommand`|string|—|First executable argument. |
| `tools.<tool>.description`|string|—|Model-visible description. |
| `tools.<tool>.parameters`|map[string]any|—|JSON Schema input data. |
| `tools.<tool>.parameters.<key>`|any|—|JSON Schema input data. |
| `tools.<tool>.timeout`|duration|—|Tool timeout overriding default; positive. |
| `project_context.max_bytes`|int|`8000`|Extra-context byte budget; at least 1. |
| `project_context.max_tokens`|int|—|Deprecated alias; if max_bytes is unset, becomes `max_tokens * 4`. |
| `project_context.extra_files`|[]string|—|Project-root-relative context files. |
| `project_context.ignore_files`|[]string|—|Excludes entries from extra_files. |
| `paths.project_root_only`|bool|`true`|Restrict tool paths to project root. |
| `paths.writable_paths`|[]string|`[]`|Extra paths mutation tools may write. |
| `paths.blocked_paths`|[]string|`[]`|Always-denied paths. |
| `paths.exclude_paths`|[]string|—|Excluded from listings and glob results. |
| `paths.exclude_patterns`|[]string|—|Excluded glob patterns. |
| `logging.enabled`|bool|`false`|Enable file logging. |
| `logging.level`|string|`info`|`trace`, `debug`, `info`, `warn`, or `error`. |
| `logging.file`|string|`~/.local/share/steiner/steiner.log`|Log path; may contain prompts/tool output. |
| `logging.thinking_chunk`|bool|`false`|Include reasoning tokens in logs. |
| `logging.compaction_log_file`|string|—|Separate compaction-event log path. |
| `context_management.read_annotations`|bool|`true`|Annotate reads with path and line range. |
| `search.backend`|string|—|`searxng`, `google`, `kagi`, or `brave`; selects requirements. |
| `search.searxng_url`|string|—|Required for `searxng`. |
| `search.google_cx`|string|—|Required for `google`. |
| `search.google_api_key`|string|—|Required for `google`; prefer environment storage. |
| `search.kagi_api_key`|string|—|Required for `kagi`; prefer environment storage. |
| `search.brave_api_key`|string|—|Required for `brave`; prefer environment storage. |
| `mcp.enabled`|bool|`true`|Enable MCP client. |
| `mcp.servers.<server>.enabled`|bool|`false`|Start server. |
| `mcp.servers.<server>.transport`|string|`stdio`|`stdio` or `http`. |
| `mcp.servers.<server>.command`|string|—|Required for stdio; empty for http. |
| `mcp.servers.<server>.args`|[]string|—|Stdio arguments; empty for http. |
| `mcp.servers.<server>.env`|map[string]string|—|Stdio process environment; empty for http. |
| `mcp.servers.<server>.env.<key>`|string|—|One environment variable. |
| `mcp.servers.<server>.url`|string|—|HTTP(S) endpoint required for http; empty for stdio. |
| `mcp.servers.<server>.headers`|map[string]string|—|HTTP headers for http; empty for stdio. |
| `mcp.servers.<server>.headers.<key>`|string|—|One HTTP header. |
| `mcp.servers.<server>.approval`|string|`ask`|`ask`, `allow`, or `deny`; allow becomes ask in plan mode. |
| `mcp.servers.<server>.trust_annotations`|bool|`false`|Annotated read-only tools may skip approval. |
| `mcp.servers.<server>.connect_timeout`|duration|`15s`|Zero uses 15s; negative invalid. |
| `mcp.servers.<server>.allowed_tools`|[]string|—|Native-name allowlist; explicit `[]` denies all. |
| `mcp.servers.<server>.blocked_tools`|[]string|—|Native-name denylist after allowlist. |
| `mcp.servers.<server>.sub_agents`|[]string|closed|Allowed types: `explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, `vision`. |
| `modes.default`|string|`build`|`plan` or `build`; plan limits edits to `.steiner/plans/`. |
| `tui.fps`|int|`60`|Interactive renderer rate, 1 through 120. |
| `cave_human`|bool|`false`|Add terse output and anti-AI-writing-tells instructions. |

`steiner config` validates and prints resolved configuration. `/config` in the TUI opens the compiled-config modal. Resolved output can contain credentials: obtain consent before a full secret-bearing read, redact secrets by default, and never echo existing secret values.

When editing, preserve unrelated YAML text and make the smallest textual mutation. Use `mutate`, never `bash`, for file changes. Changes require a `steiner` restart.

