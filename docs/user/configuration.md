# Configuration reference

Steiner is configured with YAML files, a small set of environment variables, and command-line flags. This page is the reference for every config field, its type, default and constraints, plus file locations, precedence, environment variables, expansion syntax and project trust.

For provider and model recipes (aliases, profiles, selection), see [Provider and model setup](provider-and-model-setup.md). Command-line flags are listed in [CLI](cli.md).

## File locations and precedence

Sources are applied in this order; later sources win:

1. Compiled defaults.
2. Global config: `~/.config/steiner/config.yaml` (under `$HOME`).
3. Project config: `<project>/.steiner/config.yaml`, applied only once the project is trusted (see [Project trust](#project-trust)).
4. [Environment variables](#environment-variables).
5. Command-line flags that override config: `--profile`, `--model`, `--verbose` (sets `logging.level` to `debug`) and `--unsafe` (sets `sandbox.enabled` to `false`).

Both files are optional and neither is created for you. `--config <path>` replaces the project config path (the global file is still read); unlike the default path, that file must exist. With no providers, models or profiles configured, starting a session fails with `no model configured`; inspection commands such as `steiner config` still work.

Maps merge by key across files (providers, model definitions, profiles, tools, MCP and LSP servers, `limits.tool_timeouts`, and the string maps inside them). Scalars and lists are replaced by the later file.

Unknown structured configuration fields are rejected. Map entries have their own validation rules. Durations are Go duration strings such as `250ms`, `30s`, `5m` or `1h`. A leading `~/` is expanded to your home directory in these path fields: `logging.file`, `logging.compaction_log_file`, `diagnostics.dir`, `lsp.cache_dir`, `paths.writable_paths`, `paths.blocked_paths`, `paths.exclude_paths`, `sandbox.host_mounts.<index>.path`, `tools.<name>.exec`, `project_context.extra_files` and `project_context.ignore_files`.

`steiner config` prints the resolved configuration. It includes secrets in the output, so redact before sharing.

## Environment variables

Only the variables below override configuration; there is no generic `STEINER_` prefix mapping. Integer variables that do not parse make config loading fail. Steiner reads a few other variables outside config loading: `STEINER_GITHUB_TOKEN` for update checks (see [Installation](installation.md)), `STEINER_USAGE_TELEMETRY` and `STEINER_USAGE_TELEMETRY_RUN` for usage telemetry (see [Usage and cache statistics](usage.md)), the variable named by `providers.<name>.api_key_env`, and any variable referenced by `${VAR}` expansion below.

| Variable | Effect |
| --- | --- |
| `STEINER_MODEL` | Active orchestrator model: a `models.definitions` alias or `provider/model-id`. An invalid value is silently ignored. `--model` wins over it; an invalid `--model` is also silently ignored. |
| `STEINER_SUB_AGENTS_MAX_PARALLEL` | Sets `sub_agent.max_parallel`. |
| `STEINER_TUI_FPS` | Sets `tui.fps`. |
| `STEINER_MAX_TURNS` | Sets `limits.max_turns`. |
| `STEINER_MAX_TOKENS` | Sets `limits.max_tokens`. |
| `STEINER_TOOL_OUTPUT_MAX_BYTES` | Sets `limits.tool_output_max_bytes`. |
| `STEINER_MAX_PARALLEL_TOOLS` | Sets `limits.max_parallel_tools`. |
| `STEINER_LOG_LEVEL` | Sets `logging.level`. |
| `STEINER_LOG_FILE` | Sets `logging.file`. |
| `STEINER_COMPACTION_LOG_FILE` | Sets `logging.compaction_log_file`. |
| `KAGI_API_KEY` | Fills `search.kagi_api_key` when it is empty. |
| `BRAVE_API_KEY` | Fills `search.brave_api_key` when it is empty. |
| `STEINER_TRUST_PROJECT_CONFIG` | `1` or `true` trusts the current project's config for this run without prompting. Nothing is persisted. See [Project trust](#project-trust). |

## Environment variable expansion in config values

Scalar values in config files can reference environment variables. Keys and comments are never expanded.

| Form | Result |
| --- | --- |
| `${VAR}` | Value of `VAR`. Loading fails if `VAR` is unset. A set but empty `VAR` expands to an empty string. |
| `${VAR:-default}` | Value of `VAR` if set and non-empty, otherwise `default` (itself expanded). `${VAR:-}` allows an empty value. |
| `$VAR` | Same as `${VAR}`; has no default syntax. |
| `$$` | A literal `$`. |

Backslash is not an escape character. An unquoted expansion is re-typed after substitution, so `max_attempts: ${RETRIES:-5}` and `enabled: ${FLAG:-true}` work; quoted values stay strings.

An undefined variable aborts loading at the first file that references one. The error lists every undefined variable in that file with its line, YAML path and name.

```yaml
providers:
  openai:
    type: openai
    base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
    api_key: ${OPENAI_API_KEY}
```

## Project trust

The first time Steiner runs in a directory it has not seen, it asks whether to trust that project before applying `.steiner/config.yaml`. The prompt appears whether or not the project has a config file. The trust key is the symlink-resolved absolute working directory, not a discovered git root, so a subdirectory of a trusted project prompts again.

When the project has a config, the dialog lists each leaf setting it overrides, compared against your global config (or the compiled defaults where the global file does not set it). Security-relevant fields are flagged with a leading `!`: `sandbox`, `permissions`, `paths.writable_paths`, `paths.blocked_paths`, `paths.project_root_only`, `mcp.servers.<name>.transport`, `command`, `args`, `env`, `url`, `headers`, `approval` and `trust_annotations`, `lsp.servers.<name>.command`, `args` and `env`, `tools.<name>.exec` and `subcommand`, and `providers.<name>.base_url`, `headers`, `api_key` and `api_key_env`. Existing values of `api_key` fields and anything under `headers` or `env` are shown as `(set)`. Project values are shown unexpanded, so `${VAR}` shows which variable would be used. If the project YAML fails to parse, the dialog shows the parse error instead of the diff.

Choices:

- `a` Always trust: recorded in `~/.config/steiner/trusted_projects.json`; later config changes do not prompt again.
- `s` This session only: nothing is written.
- `d` Deny and exit: the default; `Esc` and `Ctrl-C` choose it. The process exits non-zero.

There is no revoke command: edit or delete `trusted_projects.json`. A corrupt or unrecognised-version file is deleted with a one-time notice; a read or permission error is fatal and leaves the file untouched.

The dialog needs an interactive terminal. With `--exec` or without a TTY, an untrusted project makes Steiner exit non-zero unless you pass `--trust-project-config` or set `STEINER_TRUST_PROJECT_CONFIG=1` (or `true`). Both trust the project for that run only. `steiner oneshot` follows the same rule.

`steiner config` never prompts: it prints the resolved config including the project layer, prefixed with `# project <root> is NOT trusted` when the project is not in the trust store.

`--unsafe` and project trust are independent of each other.

## Top-level keys

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `providers` | map | empty | Model API connections. See [`providers` block](#providers-block). |
| `models` | block | see section | Model definitions, discovery and profiles. See [`models` block](#models-block). |
| `limits` | block | see section | Turn, token, timeout and concurrency limits. See [`limits` block](#limits-block). |
| `sandbox` | block | enabled | Bubblewrap sandbox. See [`sandbox` block](#sandbox-block). |
| `permissions` | block | see section | Host capabilities granted to the sandbox. See [`permissions` block](#permissions-block). |
| `sub_agent` | block | enabled | Delegated child agents. See [`sub_agent` block](#sub_agent-block). |
| `advisor` | block | disabled | Stronger-model advisor. See [`advisor` block](#advisor-block). |
| `oneshot` | block | see section | Oneshot closeout. See [`oneshot` block](#oneshot-block). |
| `desktop_notifications` | block | disabled | Desktop notifications. See [`desktop_notifications` block](#desktop_notifications-block). |
| `update_check` | block | enabled | Startup update check. See [`update_check` block](#update_check-block). |
| `tools` | map | empty | Custom subprocess tools. See [`tools` block](#tools-block). |
| `project_context` | block | see section | Extra files added to the prompt. See [`project_context` block](#project_context-block). |
| `paths` | block | see section | Filesystem access limits. See [`paths` block](#paths-block). |
| `logging` | block | see section | Session log files. See [`logging` block](#logging-block). |
| `diagnostics` | block | disabled | Structured diagnostics capture. See [`diagnostics` block](#diagnostics-block). |
| `context_management` | block | see section | Context management baseline. See [`context_management` block](#context_management-block). |
| `search` | block | unset | Web search backend. See [`search` block](#search-block). |
| `mcp` | block | enabled | MCP servers. See [`mcp` block](#mcp-block). |
| `lsp` | block | disabled | Language servers. See [`lsp` block](#lsp-block). |
| `modes` | block | see section | Execution mode default. See [`modes` block](#modes-block). |
| `tui` | block | see section | Interactive UI settings. See [`tui` block](#tui-block). |

TUI appearance preferences (accent, sidebar, backgrounds) are not config keys; they live in `~/.config/steiner/prefs.yaml`. See [Interactive mode](interactive.md).

## `providers` block

A map of named provider connections. The key is an arbitrary name that `models.definitions.<alias>.provider` and `provider/model-id` references use. Setup recipes are in [Provider and model setup](provider-and-model-setup.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `providers.<name>.type` | string | none | Required. One of `openai_compat`, `ollama`, `lmstudio`, `openrouter`, `openai`, `anthropic`, `gemini`, `litellm`, `codex`, `opencode_go`, `opencode_zen`, `claude_subscription`. See [Provider types](#provider-types). |
| `providers.<name>.base_url` | string | per type | API endpoint. Required for `openai_compat`, `ollama`, `lmstudio` and `litellm`. |
| `providers.<name>.api_key` | string | none | API key. Prefer `api_key_env` or `${VAR}` to keep secrets out of files. |
| `providers.<name>.api_key_env` | string | none | Name of an environment variable holding the API key, read at startup. `api_key` wins when both are set. |
| `providers.<name>.headers.<header>` | string | none | Extra HTTP header sent with every request to this provider. |
| `providers.<name>.timeout` | duration | unset | Timeout for each request attempt, covering headers, body and streaming. Unset means no overall request timeout, and a 30s response-header timeout applies instead. Setting a timeout bounds the whole request, including headers, and replaces the header timeout. Retries replay the request, so one call can take up to `retry.max_attempts` times `timeout` plus backoff. |
| `providers.<name>.compaction_fallback_after` | duration | `5m` anthropic, `60m` claude_subscription, `30m` openai and codex, `0` otherwise | Idle time after which compaction may use `models.profiles.<profile>.compaction_fallback`. `0` disables; negative is invalid. See [Context management](context-management.md). |
| `providers.<name>.codex.min_request_interval` | duration | `0s` | `codex` only. Minimum gap between consecutive requests; `0` disables pacing. |
| `providers.<name>.codex.transport` | string | `http` | `codex` only. `http`, or `websocket` (experimental; failures return an error with no HTTP fallback). |

### Provider types

| Type | `base_url` | Credentials | Notes |
| --- | --- | --- | --- |
| `openai_compat` | required | `api_key` or `api_key_env` optional | Any OpenAI chat-completions compatible server. |
| `ollama` | required, e.g. `http://localhost:11434/v1` | optional | Ollama through its OpenAI-compatible API. |
| `lmstudio` | required, typically `http://127.0.0.1:1234/v1` | optional | LM Studio's local server. |
| `litellm` | required | optional | LiteLLM gateway. A 429 without `Retry-After` has its delay parsed from the body; budget-exhaustion 429s are not retried. |
| `openrouter` | default `https://openrouter.ai/api/v1` | required | OpenRouter gateway. |
| `openai` | default `https://api.openai.com/v1` | required | Native OpenAI API. |
| `anthropic` | default `https://api.anthropic.com/v1` | required | Native Anthropic API. Steiner appends `/messages` to `base_url`, so a custom `base_url` must include the `/v1` suffix. |
| `opencode_go` | default `https://opencode.ai/zen/go/v1` | required | OpenCode Go gateway. Create the key in your OpenCode account. |
| `opencode_zen` | default `https://opencode.ai/zen/v1` | required | OpenCode Zen gateway. |
| `codex` | optional | ignored | OpenAI Codex subscription. Sign in with `steiner login codex`; authentication uses that OAuth login, not `api_key`. |
| `claude_subscription` | rejected | rejected | Runs your signed-in `claude` CLI. `base_url`, `api_key`, `api_key_env`, `headers` and `timeout` are rejected. See [Claude subscription](claude-subscription.md). |
| `gemini` | none | required | Accepted by validation but not supported at runtime; do not use. |

"Required" credentials mean one of `api_key` or `api_key_env` must be set, or validation fails.

## `models` block

Model definitions, discovery and named profiles. A profile assigns models to roles; a definition (alias) persists settings for one model. Every selection accepts a raw `provider/model-id` reference, so an alias is only needed to persist settings or give a model a short name. Selection order, aliases and profile usage are in [Provider and model setup](provider-and-model-setup.md).

No profile is required. Every declared profile must set `default_model`, and profiles never inherit from each other. Without `--profile`, the profile named `default` is used; if there is none, role assignments are unselected and `--model` or `STEINER_MODEL` still sets the orchestrator model.

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `models.discovery_enabled` | bool | `true` | Discover models from configured providers. `false` skips provider enumeration and network refresh; the chooser lists configured entries only. |
| `models.definitions.<alias>.provider` | string | none | Required. Name of a key in `providers`. |
| `models.definitions.<alias>.id` | string | none | Required. Model ID as the provider's API expects it. |
| `models.definitions.<alias>.params` | map[string]any | none | Request parameters sent in the request body, such as `temperature`, `top_p`, `top_k`. |
| `models.definitions.<alias>.extra_params` | map[string]any | none | Additional provider-specific body parameters. |
| `models.definitions.<alias>.prompt_suffix` | string | none | Text appended to the last user message of each request. |
| `models.definitions.<alias>.vision` | bool or null | null | `false` strips image attachments from requests to this model with a warning. Unset assumes vision support. |
| `models.definitions.<alias>.retry.enabled` | bool | `true` | Retry transient and rate-limit errors. Usage and quota limit errors are never retried. |
| `models.definitions.<alias>.retry.max_attempts` | int | `5` | Total attempts including the first; at least 1. |
| `models.definitions.<alias>.retry.initial_backoff` | duration | `250ms` | Wait before the first retry; greater than zero. |
| `models.definitions.<alias>.retry.max_backoff` | duration | `5s` | Cap on exponential backoff; at least `initial_backoff`. |
| `models.definitions.<alias>.retry.retry_after_max` | duration | `60s` | Longest `Retry-After` wait honoured; at least `initial_backoff`. |
| `models.definitions.<alias>.prompts.system` | string | none | Replaces the default system prompt for this model. |
| `models.definitions.<alias>.prompts.system_suffix` | string | none | Appended after the default preamble. |
| `models.definitions.<alias>.prompts.compaction` | string | none | Replaces the compaction prompt for this model. |
| `models.definitions.<alias>.advanced.limits.context_window` | int | resolved from metadata | Context window in tokens. An explicit value wins over discovered metadata. If nothing resolves, `32768` is used. |
| `models.definitions.<alias>.advanced.limits.max_output_tokens` | int | resolved from metadata | Maximum tokens per response. If nothing resolves, `4096` is used. |
| `models.definitions.<alias>.advanced.limits.compaction_threshold_tokens` | int | 70% of the context window | Prompt-token count that triggers compaction; greater than zero and below the resolved context window. |
| `models.definitions.<alias>.advanced.reasoning_echo_back` | bool or null | resolved from metadata | Whether the model's reasoning content is echoed back in later requests. |
| `models.definitions.<alias>.advanced.transport` | string | `auto` | `auto`, `openai_compat` or `anthropic`. `auto` follows models.dev metadata when available, otherwise the provider type. An explicit value overrides metadata. |
| `models.definitions.<alias>.advanced.reasoning.effort` | string | unset | Reasoning effort to request, as the provider names it (for example `low`, `high`). Unset sends no reasoning field. Must not be blank and must appear in `supported_efforts` when that is set. |
| `models.definitions.<alias>.advanced.reasoning.supported_efforts` | []string | discovered | Efforts this model accepts, used to validate `effort` and fill the `/model` picker. No blank or duplicate values. Unset uses models.dev data. |
| `models.definitions.<alias>.advanced.codex.use_max_context_window` | bool | `false` | `codex` providers only (rejected otherwise). Use the catalogue's `max_context_window` when positive, else its `context_window`. |
| `models.profiles.<profile>.default_model` | string | none | Required in every declared profile. A model reference, also the fallback for roles without their own assignment. |
| `models.profiles.<profile>.advisor` | string | unset | Model for advisor calls; falls back to `default_model`. |
| `models.profiles.<profile>.compaction_fallback` | string | unset | Model for compaction when the prompt cache is presumed cold. Needs a positive `compaction_fallback_after` on the main model's provider. Unset disables it; it does not fall back to `default_model`. |
| `models.profiles.<profile>.sub_agents.<type>` | string | unset | Model per agent type: `explore`, `research`, `code`, `documentor`, `evaluate`, `sanity_check`, `review`, `vision`. Empty or unset assignments use `default_model`, except `vision`, which requires an explicit assignment. |
| `models.profiles.<profile>.oneshot.<phase>` | string | unset | Model per oneshot phase: `plan`, `implement`, `review`. Unset phases use `default_model`. |
| `models.profiles.<profile>.workflow_handoff.<destination>` | string | unset | Model per handoff destination: `implement`, `review`, `build`. Unset destinations use `default_model`. |

The `vision` sub-agent type needs a vision-capable model; the `vision` tool is not registered unless `sub_agents.vision` is set. See [Sub-agent delegation](sub-agent-delegation.md).

## `limits` block

Runtime limits for turns, tokens, timeouts and tool concurrency. The caps apply to the parent run; `tool_output_max_bytes`, `max_parallel_tools`, the `bash` cap, the `grep` deadline and MCP call timeouts also apply to each sub-agent. See [Tools](tools.md) for tool behaviour.

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `limits.max_turns` | int | `50` | Maximum agent-loop turns before the run stops. `0` removes the limit; negative is invalid. |
| `limits.max_tokens` | int | `500000` | Maximum output (completion) tokens generated across a run before it stops; at least 1. Input tokens are not counted. |
| `limits.model_call_timeout` | duration | `10m` | Longest a single provider call may take. `0` disables it. Tool work and sub-agent calls inside a turn are not covered. |
| `limits.tool_timeout_default` | duration | `30s` | Fallback timeout for MCP tool calls whose tool has no `tool_timeouts` entry; greater than zero. Built-in tools and custom tools do not use it. |
| `limits.tool_timeouts.<tool>` | duration | see below | Per-tool timeout; greater than zero. Tool names must be non-empty but are not validated against the tool registry; an unmatched name has no effect. Defaults: `bash` `600s`, `grep` `30s`. For an MCP tool the key is its registered name `mcp__<server>__<tool>`, as shown by `steiner tools`. The `bash` entry caps the built-in Bash tool: whole seconds, at most `600s`. The `grep` entry is enforced cooperatively: an interrupted search returns an error instead of partial results. The deadline is checked between directory entries and before each file scan, so regex matching on one file or a blocking filesystem operation can run past it. `read` has no enforced deadline; a `read` entry has no effect. Custom subprocess tools use `tools.<name>.timeout` instead. |
| `limits.tool_output_max_bytes` | int | `65536` | Output kept from one tool call; the rest is truncated. At least 1. |
| `limits.max_parallel_tools` | int | `4` | Parallel-safe tool calls run concurrently in one turn, including built-in read-only tools, LSP tools and MCP tools with a read-only hint. At least 1; `1` is serial. Separate from `sub_agent.max_parallel`. |

## `sandbox` block

Bubblewrap sandbox for `bash` and subprocess tools. What the sandbox isolates, how approvals interact with it and platform limits are in [Sandboxing](sandboxing.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `sandbox.enabled` | bool | `true` | Enable the sandbox. `--unsafe` forces `false`. |
| `sandbox.warning_on_unsupported_platform` | bool | `true` | Warn when the sandbox is unavailable or bypassed. |
| `sandbox.env_passthrough` | []string | `[]` | Extra host environment variable names passed to sandboxed processes. A trailing `*` matches a prefix (`MYAPP_*`). Entries must be non-empty, contain no `=`, and `*` may only be the last character. A bare `*` is rejected; use `env_passthrough_all`. |
| `sandbox.env_passthrough_all` | bool | `false` | Pass the full host environment, credentials included, with no filtering. Always prints a warning while the sandbox is enabled, regardless of `warning_on_unsupported_platform`. |
| `sandbox.bind_host_cache` | bool | `false` | Bind the real `~/.cache` read-write instead of a sandbox-private cache directory. |
| `sandbox.host_mounts.<index>.path` | string | none | Host path to mount; `~/` expands. |
| `sandbox.host_mounts.<index>.mode` | string | read-only | `rw` for writable access; anything else mounts read-only. |

## `permissions` block

Opt-in host capabilities for the sandbox. See [Sandboxing](sandboxing.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `permissions.docker` | bool | `false` | `true` leaves the Docker socket reachable inside the sandbox. `false` masks it and unsets `DOCKER_HOST`. |

## `sub_agent` block

Delegated child agents. Behaviour, agent types and worktrees are in [Sub-agent delegation](sub-agent-delegation.md). Per-type models are set in `models.profiles.<profile>.sub_agents`.

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `sub_agent.enabled` | bool | `true` | `false` removes all delegation tools. The constraints below apply only when enabled. |
| `sub_agent.orchestration_level` | string | `standard` | `standard` or `low`. `low` omits the delegation-steering sections of the system prompt. |
| `sub_agent.max_turns` | int | `120` | Turn cap for each child run; at least 15. |
| `sub_agent.max_tokens` | int | `400000` | Maximum output (completion) tokens per child run; at least 1. |
| `sub_agent.max_parallel` | int | `3` | Session-wide cap on running children. Extra spawns queue; more than twice this many outstanding (running plus queued) fail. At least 1. |
| `sub_agent.max_follow_ups` | int | `5` | Follow-up resumes allowed per child; at least 1. Each resume extends the child's turn budget. |

## `advisor` block

Optional advisor tool: a stronger model reviews the conversation and returns guidance. The model is chosen by `models.profiles.<profile>.advisor`. See [Advisor](advisor.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `advisor.enabled` | bool | `false` | Enable the advisor tool. |
| `advisor.max_uses_per_run` | int | `3` | Advisor calls allowed per session; at least 1 when enabled. |
| `advisor.max_uses_per_sub_agent` | int | `1` | Advisor calls allowed per child agent; at least 1 when enabled. |
| `advisor.max_tokens` | int | unset | Output-token ceiling for advisor calls; greater than zero when set. |
| `advisor.context_reduction_threshold_tokens` | int | `64000` | Soft input-token threshold above which the advisor trims older ordinary tool output. `0` disables; any other value must be at least `48000`. The advisor model's context window can lower the effective trigger below the configured value. |
| `advisor.timeout` | duration | `180s` | Timeout for each advisor request attempt. Overrides `providers.<name>.timeout` for advisor calls only; greater than zero when set. |

## `oneshot` block

Closeout settings for autonomous `steiner oneshot` runs. Per-phase models are set in `models.profiles.<profile>.oneshot`. See [Oneshot](oneshot.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `oneshot.auto_pr` | bool | `false` | Let closeout push the branch and open a PR or MR after a passing review. |

## `desktop_notifications` block

Desktop notifications from the interactive TUI. They fire when a tool approval is required and when a workflow handoff is requested; there is no run-completion notification. See [Desktop notifications](desktop-notifications.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `desktop_notifications.enabled` | bool | `false` | Enable notifications. |
| `desktop_notifications.duration` | int | `0` | Display time in seconds. `0` leaves the notification until dismissed; negative is invalid. |

## `update_check` block

Background check for a newer Steiner release at startup. It runs in the interactive TUI only. See [Installation](installation.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `update_check.enabled` | bool | `true` | Enable the startup check. |
| `update_check.interval_hours` | int | `6` | Minimum hours between checks; `0` checks on every startup; negative is invalid. |

## `tools` block

Custom subprocess tools registered next to the built-ins. The map key is the tool name the model sees. Names reserved by built-ins (`read`, `glob`, `grep`, `bash`, `mutate`, `fetch_url`, `workflow_handoff`) are rejected. The stdin/stdout contract a tool must follow is in [Tools](tools.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `tools.<name>.exec` | string | none | Required. Executable to run; `~/` expands. |
| `tools.<name>.subcommand` | string | none | Passed to `exec` as its first argument. |
| `tools.<name>.description` | string | none | Description shown to the model. |
| `tools.<name>.parameters` | map[string]any | none | JSON Schema fragment for the tool's input. |
| `tools.<name>.timeout` | duration | none | Required; greater than zero. |

## `project_context` block

Extra files added to the system prompt, under a byte budget. See [Context management](context-management.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `project_context.max_bytes` | int | `8000` | Byte budget for extra context files; files are truncated or skipped to fit. At least 1. |
| `project_context.max_tokens` | int | none | Deprecated alias of `max_bytes` (multiplied by 4); `max_bytes` wins when both are set. |
| `project_context.extra_files` | []string | none | Files to include, relative to the project root. A path resolving outside the root is an error; missing files and directories are skipped. |
| `project_context.ignore_files` | []string | none | Entries of `extra_files` to skip. An ignore entry matches a file by exact path, by base name, or as a trailing path suffix. Globs and directories do not match. |

## `paths` block

Filesystem access limits for file tools, including each sub-agent's tools. Exclusion semantics are in [Tools](tools.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `paths.project_root_only` | bool | `true` | Restrict tools to paths inside the project root. |
| `paths.writable_paths` | []string | `[]` | When set, an allowlist that restricts mutation tools to these paths, including inside the project root. Outside the root it only adds to what `project_root_only` permits once that restriction is lifted by approval. May be inside or outside the project root; `~/` expands. |
| `paths.blocked_paths` | []string | `[]` | Paths always denied, even inside the project root; `~/` expands. |
| `paths.exclude_paths` | []string | none | Paths excluded from searches and directory walks. Relative entries are anchored to the workspace root; `~/` expands. `glob` applies them to each entry's own path, so a symlink into an excluded path is still listed by name. |
| `paths.exclude_patterns` | []string | none | Globs excluded from searches and directory walks. Braces expand before matching and one entry is capped at 256 expanded forms. A malformed entry, or one that exceeds that cap, is ignored; the interactive error log (`alt+e`) reports it, and the `internal` diagnostics stream receives the same message when enabled. Syntax is in [Tools](tools.md#paths-and-exclusions). |

## `logging` block

Session log files. Treat log files as sensitive: they can contain prompts and tool output. The `--log-file` flag overrides `logging.file` and enables logging for the run.

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `logging.enabled` | bool | `false` | Write the session log to `logging.file`. |
| `logging.level` | string | `info` | `trace`, `debug`, `info`, `warn` or `error`. |
| `logging.file` | string | `~/.local/share/steiner/steiner.log` | Log path; required, `~/` expands. JSONL, appended across runs and rotated by size. |
| `logging.thinking_chunk` | bool | `false` | Include reasoning chunks in the log. |
| `logging.assistant_chunk` | bool | `false` | Include streamed assistant chunks, which duplicate the completed assistant message record. |
| `logging.compaction_log_file` | string | none | Separate log of compaction events; `~/` expands. |

## `diagnostics` block

Structured JSONL capture for measuring cache hit rates, provider calls, tool runs and TUI cost. Disabled by default and independent of `logging`: the directory is never derived from `logging.file`. Each record carries `run_id`, `build_sha` and `dirty`. Streams that write to disk are listed below; see [Usage](usage.md) for cache statistics.

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `diagnostics.enabled` | bool | `false` | Master switch. When `false` nothing is created. |
| `diagnostics.dir` | string | `$XDG_STATE_HOME/steiner/diagnostics`, else `~/.local/state/steiner/diagnostics` | Directory with one file per stream. Required when enabled; must not resolve inside the project root; `~/` expands. |
| `diagnostics.retention_days` | int | `30` | Records older than this are dropped when the writer opens; greater than zero when enabled. |
| `diagnostics.streams.cache` | bool | `false` | Prompt-cache observations (`cache.jsonl`). |
| `diagnostics.streams.provider` | bool | `false` | One record per model call, whatever the outcome (`provider.jsonl`). |
| `diagnostics.streams.tool` | bool | `false` | Tool execution and delegation traces (`tool.jsonl`). |
| `diagnostics.streams.tui` | bool | `false` | Interactive TUI timings, counts and sizes, never content (`tui.jsonl`). |
| `diagnostics.streams.internal` | bool | `false` | TUI developer error entries (`internal.jsonl`); sanitised and capped, but may still carry prompt or tool content. |
| `diagnostics.capture_bodies` | bool | `false` | Capture full message, tool and block content instead of bounded fields. Expensive, and captures prompts. |

## `context_management` block

Baseline context management. Applies to the parent and to each sub-agent. See [Context management](context-management.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `context_management.read_annotations` | bool | `true` | Annotate a read when an identical full read (same `file_hash` and range) is already in the carried conversation. `false` keeps full read results. |

## `search` block

Backend for the `web_search` tool. Setup is in [Web search](web-search.md). The key fields can be filled from the unprefixed environment variables in [Environment variables](#environment-variables) when empty.

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `search.backend` | string | unset | `searxng`, `kagi` or `brave`. Selects which fields below are required. |
| `search.searxng_url` | string | none | SearXNG base URL. Required for `searxng`. |
| `search.kagi_api_key` | string | none | Kagi API key. Required for `kagi`. |
| `search.brave_api_key` | string | none | Brave Search API key. Required for `brave`. |

## `mcp` block

Model Context Protocol servers. MCP is on by default, but each server must be enabled individually. Approval behaviour and TUI surfaces are in [MCP](mcp.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `mcp.enabled` | bool | `true` | Master switch for the MCP client. |
| `mcp.servers.<server>.enabled` | bool | `false` | Start this server. The name must not contain `__`. |
| `mcp.servers.<server>.transport` | string | `stdio` | `stdio` (subprocess) or `http`. |
| `mcp.servers.<server>.command` | string | none | Executable. Required for `stdio`; must be empty for `http`. |
| `mcp.servers.<server>.args` | []string | none | Command arguments; must be empty for `http`. |
| `mcp.servers.<server>.env.<key>` | string | none | Extra environment variable for the server process; must be empty for `http`. |
| `mcp.servers.<server>.url` | string | none | `http` or `https` endpoint. Required for `http`; must be empty for `stdio`. |
| `mcp.servers.<server>.headers.<header>` | string | none | Header sent with every request; must be empty for `stdio`. Names reserved by the MCP SDK (`Content-Type`, `Accept`, `Mcp-Protocol-Version`, `Mcp-Session-Id`, `Last-Event-Id`, `Mcp-Method`, `Mcp-Name`, and the `Mcp-Param-` prefix) are rejected. Use `${VAR}` for bearer tokens. |
| `mcp.servers.<server>.approval` | string | `ask` | `ask`, `allow` or `deny`. `deny` registers no tools. |
| `mcp.servers.<server>.trust_annotations` | bool | `false` | Honour a tool's `readOnlyHint` annotation to skip approval. |
| `mcp.servers.<server>.connect_timeout` | duration | `15s` | Time allowed to connect. Unset or `0` uses `15s`; negative is invalid. |
| `mcp.servers.<server>.allowed_tools` | []string | none | Allowlist of the server's native tool names. Unset means no restriction; `[]` registers nothing. |
| `mcp.servers.<server>.blocked_tools` | []string | none | Native tool names to drop, applied after `allowed_tools`. |
| `mcp.servers.<server>.sub_agents` | []string | none | Agent types allowed to use this server's tools. Unset or `[]` grants none. Valid types: `explore`, `research`, `code`, `documentor`, `evaluate`, `sanity_check`, `review`, `vision`. |

## `lsp` block

Optional language servers for code intelligence tools. Disabled by default; servers are never installed by Steiner. Validation runs only when `lsp.enabled` is `true`. Server setup is in [LSP](lsp.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `lsp.enabled` | bool | `false` | Enable LSP tools. |
| `lsp.idle_timeout` | duration | `5m` | Stop an idle server after this long; greater than zero. |
| `lsp.request_timeout` | duration | `10s` | Timeout for one request; greater than zero. |
| `lsp.ready_timeout` | duration | `30s` | Wait for a server to finish indexing before answering anyway; greater than zero. |
| `lsp.ready_grace_period` | duration | `2s` | Grace period for servers that never report progress; greater than zero. |
| `lsp.diagnostics_window` | duration | `2s` | Time to collect published diagnostics after opening a file; greater than zero. |
| `lsp.max_results` | int | `200` | Maximum results per navigation query; greater than zero. |
| `lsp.cache_dir` | string | user cache directory | Base directory for server state; `~/` expands. |
| `lsp.servers.<name>.enabled` | bool | `false` | Enable this server. The fields below are validated only for enabled servers. |
| `lsp.servers.<name>.command` | string | none | Required. Server executable. |
| `lsp.servers.<name>.args` | []string | none | Command arguments. |
| `lsp.servers.<name>.env.<key>` | string | none | Extra environment variable for the server process. |
| `lsp.servers.<name>.file_extensions` | []string | none | Required, non-empty, each starting with `.`. Two enabled servers cannot claim the same extension (case-insensitive). |
| `lsp.servers.<name>.root_markers` | []string | none | Required, non-empty. Files or directories that identify a workspace root. |
| `lsp.servers.<name>.initialization_options` | map[string]any | none | Options passed to the server in the initialize handshake. |

## `modes` block

Execution mode at startup. Mode behaviour and switching are in [Execution modes](working-with-steiner.md#plan-and-build-modes).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `modes.default` | string | `build` | `plan` or `build`. `plan` limits project edits to `.steiner/plans` and `.steiner/security`. |

## `tui` block

Interactive terminal UI settings; ignored outside the TUI. See [Interactive mode](interactive.md).

| Path | Type | Default | Description |
| --- | --- | --- | --- |
| `tui.fps` | int | `60` | Renderer frame rate, 1 to 120. Higher values reduce input latency at the cost of CPU. |
| `tui.quota_refresh_interval` | duration | `5m` | Age after which a finished run refetches subscription usage for accounts it used. `0` disables only this refresh; negative is invalid. See [Usage](usage.md). |

## Examples

Minimal local setup with Ollama:

```yaml
providers:
  local:
    type: ollama
    base_url: http://localhost:11434/v1
models:
  profiles:
    default:
      default_model: local/qwen2.5-coder:14b
```

Cloud provider (Anthropic):

```yaml
providers:
  cloud:
    type: anthropic
    base_url: https://api.anthropic.com/v1
    api_key_env: ANTHROPIC_API_KEY
models:
  profiles:
    default:
      default_model: cloud/claude-sonnet-4-5
```
