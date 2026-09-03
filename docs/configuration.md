# Configuration reference

`steiner` is configured through a YAML file. This document covers every field
in the `Config` struct, their types, defaults, and valid values.

## File locations and loading order

Configuration is loaded and merged in the following precedence order (later
entries win):

1. Compiled defaults (`internal/config/defaults.go`)
2. `~/.config/steiner/config.yaml` — user-level config
3. `.steiner/config.yaml` — project-level config (checked in or gitignored)
4. Environment variables with the `STEINER_` prefix
5. CLI flags (`--profile`, `--model`, `--verbose`, `--unsafe`)

`--unsafe` is applied as a config override that forces `sandbox.enabled=false`
after config files and environment variables have been merged.

Key environment variables:

| Variable                          | Maps to                            |
| --------------------------------- | ---------------------------------- |
| `STEINER_MODEL`                   | active orchestrator model override |
| `STEINER_SUB_AGENTS_MAX_PARALLEL` | `sub_agent.max_parallel`           |
| `STEINER_MAX_TURNS`               | `limits.max_turns`                 |
| `STEINER_MAX_TOKENS`              | `limits.max_tokens`                |
| `STEINER_LOG_LEVEL`               | `logging.level`                    |
| `STEINER_LOG_FILE`                | `logging.file`                     |
| `STEINER_TOOL_OUTPUT_MAX_BYTES`   | `limits.tool_output_max_bytes`     |
| `STEINER_MAX_PARALLEL_TOOLS`      | `limits.max_parallel_tools`        |
| `STEINER_TUI_FPS`                 | `tui.fps`                          |

### Environment variable expansion in config values

Configuration values support shell-style environment variable expansion. The expansion applies to scalar values only—keys and comments are never expanded. An undefined variable reference is a hard error that names the file, line number, YAML path, and variable name.

**Recognized forms:**

- `${VAR}` — replaced with the value of `VAR`. If `VAR` is undefined, loading fails. If `VAR` is defined but empty, it expands to nothing (empty string).
- `${VAR:-default}` — replaced with the value of `VAR` if defined and non-empty. If `VAR` is undefined or empty, `default` is used instead (and recursively expanded). Use `${VAR:-}` to allow an empty value and opt out of the error.
- `$VAR` — bare form, replaced with the value of `VAR`. If `VAR` is undefined, loading fails. No defaults; bare form has no `:-default` syntax.
- `$$` — escape sequence for a literal `$` character.

**Behavior details:**

- Backslash is **not** an escape character; write `\\` to mean a backslash.
- Undefined references are not silently dropped; every undefined variable in every config file is reported together in a single error listing the file, line, YAML path, and variable name.
- `${VAR:-}` with an empty default is valid and does not error even if `VAR` is undefined.
- Expanded values round-trip through YAML correctly, including those that look numeric (e.g. `PORT: "${PORT}"` remains a string after expansion).

**Example:**

```yaml
providers:
  openai:
    type: openai
    base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
    api_key: ${OPENAI_API_KEY}
logging:
  file: ~/.local/share/steiner/${STEINER_LOG_FILE:-steiner.log}
```

If `OPENAI_API_KEY` is not set, configuration loading fails. If `OPENAI_BASE_URL` is not set, it defaults to the URL shown.

---

## Top-level fields

| Field                   | Type  | Default   | Description                                                                                                                                                                                                          |
| ----------------------- | ----- | --------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `models`                | block | see below | Consolidated model configuration: shared model definitions and named execution profiles.                                                                                                                             |
| `modes`                 | block | see below | Execution mode configuration.                                                                                                                                                                                        |
| `cave_human`            | bool  | `false`   | When `true`, enables `cave_human` - combines terse output with an "avoid AI-writing tells" instruction that is applied to the system preamble, compaction prompts, and sub-agent prompts.                            |
| `advisor`               | block | see below | Optional stronger-model steering config. When enabled, the advisor tool is available to the main loop and its per-run cap is enforced in handler state so the tool registry stays static for prompt-cache integrity. |
| `oneshot`               | block | empty     | Closeout settings for autonomous oneshot runs. Per-phase model assignments live in the selected model profile.                                                                                                       |
| `desktop_notifications` | block | see below | Desktop notification settings for run completion and events.                                                                                                                                                         |
| `mcp`                   | block | see below | Model Context Protocol server configuration.                                                                                                                                                                         |
| `tui`                   | block | see below | Interactive terminal UI settings.                                                                                                                                                                                    |

## `advisor` block

Controls the optional advisor reasoning pass. The advisor is disabled by default.
When enabled, it uses a stronger model to review the live parent conversation and
return concise strategic guidance. The tool definition stays stable for the whole
session; the `max_uses_per_run` cap is enforced in shared handler state, persisted
for the life of the process rather than reset each turn, rather than by removing
or mutating the tool mid-conversation.

| Field                    | Type      | Default | Description                                                                                                                                                                                                                                                                                                                                                  |
| ------------------------ | --------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `enabled`                | bool      | `false` | Master switch. Set to `true` to enable the advisor tool and prompt steering.                                                                                                                                                                                                                                                                                 |
| `max_uses_per_run`       | int       | `3`     | Per-session use cap enforced in shared handler state. When the cap is exhausted, the handler returns a budget-exhausted result instead of calling the advisor model.                                                                                                                                                                                         |
| `max_uses_per_sub_agent` | int       | `1`     | Per-child use cap for the advisor tool, separate from `max_uses_per_run`. Applies only to `code`, `review`, and `evaluate` children; caps advisor calls for that child's whole lifetime, surviving `follow_up` resumption of the same agent ID.                                                                                                              |
| `max_tokens`             | *int      | `nil`   | Optional output-token ceiling for advisor calls. When set, the value is forwarded to the provider request.                                                                                                                                                                                                                                                   |
| `timeout`                | *Duration | `180s`  | Optional HTTP timeout override applied only to advisor calls. When set, it overrides `providers.<name>.timeout` for the advisor model only; the main chat model and other models using the same provider are unaffected. Useful because advisor calls send a large parent-conversation prompt and frequently hit the provider's default header-read timeout. |

The model alias used for advisor calls is configured in the selected profile's `advisor` field (see the [`models` block](#models-block)), not under `advisor` itself.

```yaml
advisor:
  enabled: true
  max_uses_per_run: 2
  max_tokens: 256
  timeout: 5m

models:
  definitions:
    advisor-model:
      provider: local
      id: advisor-model
  profiles:
    default:
      default_model: default
      advisor: advisor-model
```

---

## `oneshot` block

Controls the optional closeout PR flow for the autonomous `oneshot` mode.
Per-phase model assignments live in the selected profile's `oneshot` map (see the
[`models` block](#models-block)) and are sparse: omit a phase to let runtime use
that profile's `default_model` when the phase is resolved.

| Field     | Type | Default | Description                                                                                |
| --------- | ---- | ------- | ------------------------------------------------------------------------------------------ |
| `auto_pr` | bool | `false` | When `true`, oneshot closeout may push the branch and open a PR/MR after a passing review. |

```yaml
oneshot:
  auto_pr: false

models:
  definitions:
    planner-model:
      provider: local
      id: planner-model
    coder-model:
      provider: local
      id: coder-model
    reviewer-model:
      provider: local
      id: reviewer-model
  profiles:
    default:
      default_model: coder-model
      oneshot:
        plan: planner-model
        implement: coder-model
        review: reviewer-model
```

---

## `desktop_notifications` block

Controls desktop notification behavior for run completion and events. All fields are optional and default to disabled (no notifications).

| Field      | Type | Default | Description                                                                                                                                                                                         |
| ---------- | ---- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enabled`  | bool | `false` | Master switch. Set to `true` to enable desktop notifications.                                                                                                                                       |
| `duration` | int  | `0`     | Notification display duration in seconds. Set to `0` for persistent/sticky notifications that do not auto-dismiss. Set to a positive integer to auto-dismiss after that many seconds. Must be >= 0. |

```yaml
desktop_notifications:
  enabled: true
  duration: 5
```

Setting `duration: 0` creates permanent notifications that the user must manually dismiss:

```yaml
desktop_notifications:
  enabled: true
  duration: 0
```

---

## `tui` block

Interactive terminal UI settings. Ignored outside interactive mode.

| Field | Type | Default | Description                                                                                                                                                                                                                                   |
| ----- | ---- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fps` | int  | `60`    | Renderer frame rate. Bubble Tea flushes the terminal on this ticker, so it bounds how long a processed keystroke waits before it becomes visible - the average wait is half a frame (~8.3ms at 60, ~4.2ms at 120). Must be between 1 and 120. |

Raising `fps` reduces input latency and increases CPU proportionally: renderer
flush is roughly three quarters of TUI CPU cost, so moving from 60 to 120
roughly doubles the flush work (measured at +61% process CPU during a
150 token/s stream at 220x60). Raise it if input feels sluggish and you have
CPU headroom; leave it at 60 on battery or on a constrained machine.

```yaml
tui:
  fps: 60
```

---

## `providers` block

A map of named provider configurations. Each entry describes how steiner
connects to a model API. The key is an arbitrary name referenced by
`models.definitions.*.provider`.

```yaml
providers:
  <name>:
    type: <provider_type>
    ...
```

### `ProviderConfig` fields

| Field         | Type                  | Default                                                           | Description                                                                |
| ------------- | --------------------- | ----------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `type`        | string (ProviderType) | —                                                                 | The provider type. See [Provider types](#provider-types) below.            |
| `base_url`    | string                | `"http://localhost:11434/v1"` (for the built-in `local` provider) | Base URL for the API endpoint.                                             |
| `api_key`     | string                | —                                                                 | API key value. Prefer `api_key_env` to avoid committing secrets.           |
| `api_key_env` | string                | —                                                                 | Name of an environment variable containing the API key. Loaded at startup. |
| `headers`     | map[string]string     | —                                                                 | Additional HTTP headers sent with every request to this provider.          |
| `timeout`     | duration string       | `"30s"`                                                           | Per-request HTTP timeout. Accepts Go duration strings: `30s`, `2m`, etc.   |
| `codex`       | block                 | see below                                                         | Codex-specific configuration (provider type `codex` only).                 |

### Provider types

| Type            | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `openai_compat` | Generic OpenAI-compatible HTTP API. Works with any server that follows the OpenAI chat completions shape.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `ollama`        | Ollama server. Uses Ollama's native endpoint conventions. `base_url` defaults to `http://localhost:11434`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `lmstudio`      | LM Studio's built-in OpenAI-compatible server. Typically at `http://127.0.0.1:1234/v1`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `openrouter`    | OpenRouter cloud gateway. Requires `api_key` or `api_key_env`. No `base_url` needed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `openai`        | Native OpenAI API. Requires `api_key` or `api_key_env`. No `base_url` needed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `anthropic`     | Native Anthropic API. Requires `api_key` or `api_key_env`. No `base_url` needed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `gemini`        | **Not implemented by the runtime provider factory** — configuring `type: gemini` passes config validation but fails at startup with `provider type "gemini" is not implemented by the runtime provider factory`. As a workaround, use `type: openai_compat` against a Gemini-compatible OpenAI endpoint and set `models.<alias>.advanced.transport: openai_compat` as an explicit override.                                                                                                                                                                                                                                                                                                                                                                                                           |
| `litellm`       | LiteLLM gateway endpoint. Works like `openai_compat` but with LiteLLM-specific retry handling: when a 429 response lacks a `Retry-After` header, steiner parses the delay from the response body (e.g. "Try again in N seconds"). Budget-exhaustion 429s are detected and treated as non-retryable. Set `base_url` to your LiteLLM server.                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `codex`         | OpenAI Codex subscription via OAuth. Authenticates using your OpenAI account instead of an API key and uses the Responses wire format. Run `steiner login codex` before use. When login can exchange the ChatGPT ID token for an API-key style credential, Steiner sends requests to `https://api.openai.com/v1/responses`; otherwise it uses `https://chatgpt.com/backend-api/codex/responses` with the saved OAuth access token and `ChatGPT-Account-ID`. `api_key` and `api_key_env` are not used - authentication is managed by the OAuth token stored at `~/.config/steiner/codex_auth.json`. Older token files still load, but re-running `steiner login codex` refreshes stored ChatGPT account metadata and the optional exchanged API credential used for direct OpenAI Responses API calls. |
| `opencode_go`   | opencode.ai's OpenCode Go gateway. `base_url` defaults to `https://opencode.ai/zen/go/v1`. Requires `api_key` or `api_key_env` (obtained via `/connect` in opencode's own TUI — no OAuth/login flow in steiner). Every request automatically carries an `X-Opencode-Session` header set to steiner's stable per-session ID.                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `opencode_zen`  | opencode.ai's OpenCode Zen gateway. `base_url` defaults to `https://opencode.ai/zen/v1`. Requires `api_key` or `api_key_env`, same as `opencode_go`. Claude-family models served through Zen automatically dispatch over the Anthropic-native transport (via the existing models.dev-driven transport fallback) while still carrying the `X-Opencode-Session` header.                                                                                                                                                                                                                                                                                                                                                                                                                                 |

**Field applicability by provider type:**

| Field         | openai_compat |  ollama  | lmstudio | openrouter | openai | anthropic | gemini¹ | litellm  |  codex   | opencode_go | opencode_zen |
| ------------- | :-----------: | :------: | :------: | :--------: | :----: | :-------: | :-----: | :------: | :------: | :---------: | :----------: |
| `base_url`    |   required    | optional | required |     —      |   —    |     —     |    —    | required | optional |  optional   |   optional   |
| `api_key`     |   optional    |    —     |    —     |     ✓      |   ✓    |     ✓     |    ✓    | optional |    —     |      ✓      |      ✓       |
| `api_key_env` |   optional    |    —     |    —     |     ✓      |   ✓    |     ✓     |    ✓    | optional |    —     |      ✓      |      ✓       |
| `headers`     |       ✓       |    ✓     |    ✓     |     ✓      |   ✓    |     ✓     |    ✓    |    ✓     |    ✓     |      ✓      |      ✓       |
| `timeout`     |       ✓       |    ✓     |    ✓     |     ✓      |   ✓    |     ✓     |    ✓    |    ✓     |    ✓     |      ✓      |      ✓       |
| `codex`       |       —       |    —     |    —     |     —      |   —    |     —     |    —    |    —     |    ✓     |      —      |      —       |

¹ `gemini` passes config validation but is not implemented by the runtime provider factory; see the [Provider types](#provider-types) table above for the workaround.

### `codex` sub-block

Codex-specific configuration. Applies only when `type: codex`.

| Field                  | Type            | Default  | Description                                                                                                                                                                                                                                                                                                                                                                                                                             |
| ---------------------- | --------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `min_request_interval` | duration string | `"0"`    | Minimum interval enforced between consecutive Codex requests. Defaults to `0` (disabled). When set to a positive duration (e.g., `4s`), enforces a minimum gap between requests and serialises them. Affects only bursts; has no effect on interactive use where think-time already far exceeds any sensible interval. Has no effect on cache hit rate (see [cache-stats.md](cache-stats.md#superseded-claims-that-did-not-reproduce)). |
| `transport`            | string          | `"http"` | Transport used for Codex requests. Valid values: `http` (default), `websocket`. `http`: use HTTP-only transport. `websocket`: use the WebSocket transport, with no HTTP fallback — failures return an error rather than silently degrading. Opt-in and experimental; see [cache-stats.md](cache-stats.md#superseded-claims-that-did-not-reproduce) for why it is not the default.                                                       |

---

## `models` block

Consolidates all model configuration: the shared model definition registry and
named execution profiles. The `default` profile is required and is the complete
baseline. Other profiles are partial overlays: omitted fields and map entries
inherit from `models.profiles.default` when selected.

| Field               | Type                    | Default   | Description                                                                                                                                                               |
| ------------------- | ----------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `definitions`       | map[string]ModelConfig  | empty     | Shared named model definitions. Each entry binds a provider to a specific model ID and sets request-level parameters.                                                     |
| `discovery_enabled` | bool                    | `true`    | When `true`, discover available models from configured providers. When `false`, skip provider enumeration and network refresh; the chooser shows configured entries only. |
| `profiles`          | map[string]ModelProfile | see below | Named model-assignment profiles. `profiles.default` is required and must define `default_model`; named profiles may override any role partially.                          |

Each profile supports these fields:

| Field              | Type              | Description                                                                                                                                                              |
| ------------------ | ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `default_model`    | string            | Model alias or `provider/model-id` reference used as the profile's default and as fallback for roles without a more specific assignment. Required in `profiles.default`. |
| `advisor`          | string            | Model reference used for advisor calls when `advisor.enabled` is `true`.                                                                                                 |
| `sub_agents`       | map[string]string | Per-agent-type model references, keyed by agent type.                                                                                                                    |
| `oneshot`          | map[string]string | Per-phase model references, keyed by `plan`, `implement`, and `review`. Missing phases fall back to the selected profile's `default_model`.                              |
| `workflow_handoff` | map[string]string | Persistent handoff model references, keyed by `implement`, `review`, and `build`. Missing destinations use the selected profile's `default_model` (`profile default`).   |

```yaml
models:
  discovery_enabled: true
  definitions:
    local:
      provider: local
      id: qwen2.5-coder:14b
    sonnet:
      provider: anthropic
      id: claude-sonnet-4-5
  profiles:
    default:
      default_model: local
      advisor: sonnet
      sub_agents:
        code: sonnet
        evaluate: sonnet
        sanity_check: local
      oneshot:
        plan: local
        implement: sonnet
        review: sonnet
      workflow_handoff:
        implement: sonnet
        review: sonnet
    fast:
      default_model: local
      sub_agents:
        code: local
```

`workflow_handoff` supports destination keys `implement`, `review`, and `build`. If a
destination has no entry, handoff uses the selected profile's `default_model` (`profile default`). The
interactive handoff picker can still override the pending model for one
handoff without changing configuration.

### Model references and selection

Every model selection accepts either a configured alias or a raw `provider/model-id` reference:

```text
alias | provider/model-id
```

This applies to profile assignments, the `--model` flag, `STEINER_MODEL`, and the
`/model` command. For example, `openrouter/openai/gpt-4o` selects model ID
`openai/gpt-4o` from the configured `openrouter` provider.

At startup, configuration is resolved in this order:

1. Config files are merged, then the selected profile is resolved. `--profile <name>` selects a named profile; with no flag, `default` is selected.
2. `STEINER_MODEL` overrides the active orchestrator model.
3. `--model` overrides `STEINER_MODEL` and the profile's active orchestrator model.

The env and CLI model overrides affect only the active orchestrator selection.
They do not replace the selected profile's role assignments or its
`default_model` fallback, so advisor, sub-agent, oneshot, and workflow-handoff
roles continue to resolve from the profile.

In interactive mode, `/model` changes only the active orchestrator selection.
`/profile <name>` changes future advisor, sub-agent, oneshot, and workflow-handoff
assignments and the selected `default_model` fallback, but preserves the current
active orchestrator model, any current `/model` override, conversation, and
prompt-cache identity. `/profile` requires a name; it does not open a picker.
An unknown or invalid profile reports an error and leaves the current selection
unchanged.

An exact configured alias takes precedence over provider-prefix parsing. Otherwise,
steiner uses the longest matching configured provider prefix, so provider and model
IDs may contain additional slashes.

For recommended model tiers per sub-agent type, see [Recommended model tiers](sub-agent-delegation.md#recommended-model-tiers) in the delegation documentation.

### `definitions` entries

Each key under `models.definitions` is an arbitrary model alias name. Each
entry binds a provider to a specific model ID and sets request-level
parameters.

```yaml
models:
  definitions:
    <name>:
      provider: <provider_name>
      id: <model_id>
      ...
```

### `ModelConfig` fields

| Field           | Type           | Default                | Description                                                                                                                                                                                                                         |
| --------------- | -------------- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `provider`      | string         | `"local"`              | Name of the provider (key in `providers`) to use for this model.                                                                                                                                                                    |
| `id`            | string         | `"qwen3-35b-a3b"`      | The model identifier as expected by the provider API (e.g. `claude-sonnet-4-5`, `gpt-4o`, `llama3:8b`).                                                                                                                             |
| `params`        | map[string]any | —                      | Standard request parameters sent in the request body. Common keys: `temperature`, `top_p`, `top_k`.                                                                                                                                 |
| `extra_params`  | map[string]any | —                      | Additional provider-specific parameters merged into the request body. Use for fields not covered by `params`.                                                                                                                       |
| `prompt_suffix` | string         | —                      | Text appended to every user message before sending to the model. Useful for model-specific control tokens (e.g. `<                                                                                                                  | think_off | >`). |
| `retry`         | RetryConfig    | see below              | Retry policy for failed or rate-limited requests.                                                                                                                                                                                   |
| `prompts`       | ModelPrompts   | —                      | Per-model system and compaction prompt overrides.                                                                                                                                                                                   |
| `advanced`      | AdvancedConfig | see below              | Token limit and inference-level settings.                                                                                                                                                                                           |
| `vision`        | bool or null   | null (assumed capable) | When set to `false`, image attachments are stripped from requests to this model and a diagnostic warning is emitted. When `null` or unset, vision capability is assumed. Set to `false` for older models that reject image content. |

### `RetryConfig` fields

| Field             | Type            | Default   | Description                                                     |
| ----------------- | --------------- | --------- | --------------------------------------------------------------- |
| `enabled`         | bool            | `true`    | Whether to retry on transient errors.                           |
| `max_attempts`    | int             | `3`       | Maximum number of total attempts (initial + retries).           |
| `initial_backoff` | duration string | `"250ms"` | Wait time before the first retry.                               |
| `max_backoff`     | duration string | `"5s"`    | Upper cap on exponential backoff wait time.                     |
| `retry_after_max` | duration string | `"30s"`   | Maximum time to honour a `Retry-After` header before giving up. |

### `ModelPrompts` fields

| Field           | Type   | Default | Description                                                                                                                                                              |
| --------------- | ------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `system`        | string | —       | Overrides the embedded default system prompt for this model.                                                                                                             |
| `system_suffix` | string | —       | Text appended after all default preamble content (including `cave_human` instructions). Enables per-model system prompt steering without replacing the default preamble. |
| `compaction`    | string | —       | Overrides the embedded compaction (context summarisation) prompt for this model.                                                                                         |

### `AdvancedConfig` fields

| Field                 | Type                 | Default   | Description                                                                                                                                                                                         |
| --------------------- | -------------------- | --------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `limits`              | AdvancedLimitsConfig | see below | Token budget settings for this model.                                                                                                                                                               |
| `reasoning_echo_back` | *bool                | —         | When set, controls whether reasoning tokens are echoed back in the response. Provider-dependent.                                                                                                    |
| `transport`           | string               | `"auto"`  | Transport override for request formatting. Supported values: `auto`, `openai_compat`, `anthropic`. `auto` uses models.dev metadata when available and otherwise keeps the configured provider type. |
| `reasoning`           | ReasoningConfig      | see below | Reasoning effort configuration for this model. Only meaningful for providers that support configurable reasoning effort (currently OpenAI and Codex); unsupported providers ignore it.              |

#### Automatic transport resolution

When `transport` is set to `auto` (the default), Steiner resolves the request transport per model using this precedence:

1. **Explicit config override** — If `models.<alias>.advanced.transport` is set to `openai_compat` or `anthropic`, that value wins unconditionally.
2. **models.dev metadata** — If the models.dev cache lists a provider NPM (e.g. `@ai-sdk/anthropic`) for the model, Steiner switches the effective provider type to match the metadata transport.
3. **Configured provider type** — If neither override nor metadata is available, the transport from the model's configured `provider` entry is used.

This means a single `openai_compat` provider can serve both OpenAI-compatible and Anthropic-native models as long as the metadata is available. Use `steiner model inspect <alias>` to see the resolved `effective_provider_type`, `effective_transport`, and `transport_override_reason` for any model.

### `ReasoningConfig` fields

| Field               | Type     | Default | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| ------------------- | -------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `effort`            | string   | —       | Reasoning effort to request for this model. This is a provider/model-native string, not a Steiner-owned enum — the accepted values depend entirely on the provider and model. For OpenAI/Codex models the accepted values are model-specific (e.g. `none`, `low`, `medium`, `high`, `xhigh` for gpt-5.4 variants) and are discovered from the models.dev metadata cache when `supported_efforts` is not set. When unset, no `effort` is applied and no request-level reasoning field is sent at all, so the provider's own default behavior applies.                                                                                                                                      |
| `supported_efforts` | []string | —       | The set of provider/model-native effort values this model accepts, used to validate `effort` and to populate the `/model` reasoning-effort picker in the TUI. When unset, Steiner discovers per-model efforts from the models.dev metadata cache (the same source used for context limits), falling back to a conservative built-in list (`minimal`, `low`, `medium`, `high`) only for recognized OpenAI/Codex model families (model IDs containing `gpt-5`, `o1`, `o3`, `o4`, or `codex`) not found in models.dev. For all other providers and model families, unset means Steiner has no known valid efforts and the `/model` reasoning-effort picker offers no options for that model. |

Wire shape differs by transport: Codex (OpenAI Responses API) sends the resolved effort as a `reasoning: {"effort": "...", "summary": "auto"}` object — `summary: "auto"` is always included alongside a resolved effort so the API returns reasoning summaries, which Steiner parses and replays as thinking blocks; OpenAI-compatible chat-completions providers (`type: openai`) send it as a flat `reasoning_effort` string field. Both are omitted entirely when no effort is resolved.

Example (OpenAI/Codex-style values — other providers may use a different vocabulary, or may not support configurable reasoning effort at all):

```yaml
models:
  definitions:
    codex-high:
      provider: codex
      id: gpt-5-codex
      advanced:
        reasoning:
          effort: high
          supported_efforts: [minimal, low, medium, high]
  profiles:
    default:
      default_model: codex-high
```

Reasoning effort can also be changed at runtime for the current session via the `/model` command in the interactive TUI (select a model, then a reasoning effort from `supported_efforts`, or "provider default" to omit the field). Runtime `/model` reasoning selections are session-only and never write back to the config file. Use `steiner model inspect <alias>` to see the resolved `supported_efforts`, `provider_default_effort`, `configured_effort`, and `effective_effort` for a model.

### `AdvancedLimitsConfig` fields

| Field               | Type | Default | Description                                                                            |
| ------------------- | ---- | ------- | -------------------------------------------------------------------------------------- |
| `context_window`    | int  | `32768` | Maximum context window size in tokens. Used by the prompt assembler to budget context. |
| `max_output_tokens` | int  | `8192`  | Maximum tokens the model may generate per response.                                    |

---

## `modes` block

Controls execution mode configuration.

| Field     | Type   | Default   | Description                                                                                                                                                               |
| --------- | ------ | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `default` | string | `"build"` | Execution mode for interactive sessions. Allowed values: `"plan"` or `"build"`. `plan` restricts project edits to `.steiner/plans/`; `build` is normal workspace editing. |

```yaml
modes:
  default: build
```

---

## `sandbox` block

Controls bubblewrap sandbox behavior.

The runtime sandbox status (`active`, `unavailable`, or `bypassed`) is computed at startup
and surfaced only in the TUI (sidebar/badge) and startup warnings; it is not part of `steiner
config` output and is not user-configurable.

| Field                             | Type     | Default | Description                                                                                                                                                                                                          |
| --------------------------------- | -------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enabled`                         | bool     | `true`  | Enable bubblewrap sandboxing. `--unsafe` applies a CLI override that forces this to `false` at load time.                                                                                                            |
| `warning_on_unsupported_platform` | bool     | `true`  | When enabled, shows a warning in the TUI when sandbox is unavailable or bypassed.                                                                                                                                    |
| `env_passthrough`                 | []string | `[]`    | Additional host environment variable names (beyond the built-in allowlist) passed through to sandboxed processes. Entries may end in `*` to match by prefix (e.g. `MYAPP_*`); no other wildcard forms are supported. |
| `env_passthrough_all`             | bool     | `false` | When `true`, disables environment filtering entirely and passes the full host environment through, including credentials.                                                                                            |
| `host_mounts`                     | []object | `[]`    | Additional host paths to bind-mount into the sandbox. Use `mode: rw` to grant writable access to paths outside the workspace (all host paths are already readable through the root bind).                            |

Each entry has:

| Field  | Type   | Description                                                                          |
| ------ | ------ | ------------------------------------------------------------------------------------ |
| `path` | string | Host path to mount (supports `~` expansion).                                         |
| `mode` | string | Mount mode: `rw` for writable access (host is already read-only by default) or `ro`. |

```yaml
sandbox:
  enabled: true                       # default; --unsafe overrides this to false at runtime
  warning_on_unsupported_platform: true # default; warns when sandbox is unavailable/bypassed
  env_passthrough: []                 # default; extra allowlisted env var names, "*" suffix allowed for prefix match
  env_passthrough_all: false          # default; true disables env filtering entirely
  host_mounts:
    - path: ~/.kube
      mode: rw
    - path: /opt/tools
      mode: rw
```

---

## `permissions` block

Opt-in permissions for additional capabilities.

| Field    | Type | Default | Description                                                                                                                                                                                                                                                                         |
| -------- | ---- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `docker` | bool | `false` | Gates Docker socket access inside the sandbox. `false` (default) masks any reachable Docker socket and unsets `DOCKER_HOST`, denying sandboxed tools access to the host daemon. `true` leaves the socket reachable. See [tool-sandboxing.md](tool-sandboxing.md#docker-permission). |

```yaml
permissions:
  docker: false
```

---

## `limits` block

Runtime limits for turns, tokens, and tool execution.

| Field                   | Type                       | Default   | Description                                                                                                                                                                                                                                                                                                                                                                                       |
| ----------------------- | -------------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `max_turns`             | int                        | `50`      | Maximum agent loop turns before the run is stopped.                                                                                                                                                                                                                                                                                                                                               |
| `max_tokens`            | int                        | `500000`  | Maximum total tokens (input + output) consumed before the run is stopped.                                                                                                                                                                                                                                                                                                                         |
| `tool_timeout_default`  | duration string            | `"30s"`   | Default timeout applied to any tool not listed in `tool_timeouts`.                                                                                                                                                                                                                                                                                                                                |
| `tool_timeouts`         | map[string]duration string | see below | Per-tool timeout overrides.                                                                                                                                                                                                                                                                                                                                                                       |
| `tool_output_max_bytes` | int                        | `65536`   | Maximum bytes of output captured from a single tool call. Output is truncated to this limit. Applies to both the parent run and each child sub-agent's own tool executor.                                                                                                                                                                                                                         |
| `max_parallel_tools`    | int                        | `4`       | Maximum number of ordinary parallel-safe tool calls (`read`, `glob`, `grep`, `ls`, `fetch_url`, `web_search`) executed concurrently within a single turn. Must be at least `1`; `1` forces serial execution for these tools. Distinct from `sub_agent.max_parallel`, which bounds delegation-tool concurrency independently. Applies to both the parent run and each child sub-agent's own turns. |

> **Breaking change**: `max_parallel_tools: 0` previously meant unbounded concurrency; it is now rejected at startup. Set it to `1` for the equivalent serial behaviour, or a positive number for bounded concurrency. `sub_agent.max_parallel: 0` previously had no runtime effect at all (the field was dead); it is likewise now rejected — set it to `1` or higher.

Default `tool_timeouts`:

| Tool   | Timeout |
| ------ | ------- |
| `bash` | `120s`  |
| `read` | `5s`    |
| `grep` | `30s`   |
| `ls`   | `5s`    |

```yaml
limits:
  max_turns: 50
  max_tokens: 500000
  tool_timeout_default: 30s
  tool_timeouts:
    bash: 120s
    read: 5s
    grep: 30s
    ls: 5s
  tool_output_max_bytes: 65536
  max_parallel_tools: 4
```

`tool_timeout_default` and `tool_timeouts` also apply to MCP tools: every MCP call is
bounded by `tool_timeout_default` (the `30s` default), unless the tool's full registered
name has an entry in `tool_timeouts`. That key is the registry name shown by
`steiner tools` — `mcp__<server>__<tool>`, or the hashed form when the name needed
sanitisation or exceeded the length limit.

---

## `sub_agent` block

Controls delegated child-agent execution. For details on what sub-agents can
do and tool allowlists for each specialised agent type, see
[docs/sub-agent-delegation.md](sub-agent-delegation.md).

| Field          | Type | Default  | Description                                                                                                                                                                                                                                                                                                                                                                     |
| -------------- | ---- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enabled`      | bool | `true`   | Master switch. Set to `false` to remove all delegation tools from the model.                                                                                                                                                                                                                                                                                                    |
| `max_turns`    | int  | `30`     | Maximum turns allowed for each child agent run. A floor of 15 turns is enforced internally.                                                                                                                                                                                                                                                                                     |
| `max_tokens`   | int  | `100000` | Maximum tokens a child agent may consume.                                                                                                                                                                                                                                                                                                                                       |
| `max_parallel` | int  | `3`      | Maximum number of delegation-tool calls (specialized sub-agent spawns, `follow_up`) executed concurrently within a single parent turn. Must be at least `1`; `1` forces serial execution for delegation calls. Independent of `limits.max_parallel_tools`, which bounds ordinary tool-call concurrency — a mixed batch never lets the two compete for the same semaphore slots. |

Each specialised agent type (`explore`, `research`, `code`, `plan`, `verify`,
`vision`) has its own hardcoded tool allowlist; there is no user-configurable
tool allowlist field.

Per-agent-type model overrides live in the selected profile's `sub_agents` map
(see the [`models` block](#models-block)), keyed by agent type (e.g. `code`,
`evaluate`, `sanity_check`, `explore`, `vision`). If an agent type has no entry, the
sub-agent uses the profile's default assignment.

```yaml
sub_agent:
  enabled: true
  max_parallel: 3
  max_turns: 30
  max_tokens: 100000

models:
  definitions:
    gpt-4o:
      provider: openai
      id: gpt-4o
    claude-sonnet-4:
      provider: anthropic
      id: claude-sonnet-4
  profiles:
    default:
      default_model: gpt-4o
      sub_agents:
        code: gpt-4o
        research: claude-sonnet-4
        vision: claude-sonnet-4   # required to enable the vision tool
```

The `vision` agent type requires a vision-capable model. When the selected profile's
`sub_agents.vision` is empty or unset, the `vision` tool is not registered.

---

## `tools` block

A map of externally configured tools registered alongside the built-in tools.
Each key becomes the tool name the model uses.
Tool names that collide with a built-in (`read`, `glob`, `grep`, `ls`, `bash`, `display_file`, `mutate`, `fetch_url`, `workflow_handoff`) are rejected at config load — a config tool replaces the built-in definition, substituting an `ExecPath`-only tool for the built-in's handler and silently dropping its behaviour.

```yaml
tools:
  <tool_name>:
    exec: <binary_path>
    ...
```

### `ToolConfig` fields

| Field         | Type            | Default | Description                                                               |
| ------------- | --------------- | ------- | ------------------------------------------------------------------------- |
| `exec`        | string          | —       | Path to the executable to run when this tool is called.                   |
| `subcommand`  | string          | —       | Subcommand or argument passed as the first positional argument to `exec`. |
| `description` | string          | —       | Human-readable description shown to the model in the tool schema.         |
| `parameters`  | map[string]any  | —       | JSON Schema fragment describing the tool's input parameters.              |
| `timeout`     | duration string | —       | Per-tool timeout. Overrides `limits.tool_timeout_default` for this tool.  |

### Tool contract

steiner invokes `exec` (with `subcommand` as its first argument, if set) as a
subprocess for each call. The contract is fixed and not configurable per tool:

- **stdin**: the model's validated input arguments, as a single JSON object,
  written once and then closed.
- **stdout**: a single JSON object on completion:
  - Success: `{"ok": true, "result": <any JSON value>}`. `result` is what the
    model sees, verbatim.
  - Failure: `{"ok": false, "error": {"kind": "<short slug>", "message": "<human-readable>", "details": <optional any>}}`.
    `kind` and `message` are model-visible; `details` is host-side only (shown
    in logs/TUI, not sent to the model).
  - Optionally, alongside `result`: `"model_result": <any JSON value>`. When
    present, `model_result` — not `result` — is what the model sees;
    `result` is then retained host-side only (session history, TUI). Use
    this when your tool's full result is large, includes raw
    logs/diagnostics, or otherwise carries more than the model needs to act
    on. Omit it to keep sending `result` to the model directly (default,
    unchanged behavior).
- **exit code**: a nonzero exit code is always treated as a failure,
  regardless of what stdout contains — even a well-formed
  `{"ok": true, ...}` response is discarded and reported to the model as an
  error if the process exits nonzero. Exit `0` on every successful call; use
  the `ok: false` envelope, not a nonzero exit code, to signal a specific
  failure.
- stdout/stderr raw bytes are captured host-side (bounded by
  `limits.tool_output_max_bytes`) and are never sent to the model on
  success; they surface only inside a failure's `details` when stdout
  wasn't valid JSON or the process itself failed to run.

```yaml
tools:
  fmt:
    exec: gofmt
    subcommand: -w
    description: Format Go source files in place.
    timeout: 10s
```

A minimal conforming tool, reading arguments from stdin and writing the
envelope to stdout:

```json
// stdin
{"path": "main.go"}

// stdout (success, no model_result — result goes straight to the model)
{"ok": true, "result": {"formatted": true}}

// stdout (success, with model_result — result is host-only)
{"ok": true, "result": {"formatted": true, "diff": "...200 lines..."}, "model_result": {"formatted": true}}

// stdout (failure)
{"ok": false, "error": {"kind": "invalid_input", "message": "path not found"}}
```

---

## `mcp` block

Configures Model Context Protocol (MCP) servers. MCP is enabled by default.
Individual servers must still be enabled explicitly via `servers.<name>.enabled`. See
[docs/mcp.md](mcp.md) for the TUI surfaces and approval behavior.

| Field     | Type | Default | Description                                          |
| --------- | ---- | ------- | ---------------------------------------------------- |
| `enabled` | bool | `true`  | Master switch for the MCP client.                    |
| `servers` | map  | —       | Per-server configuration under `mcp.servers.<name>`. |

Each server entry (`MCPServerConfig`) supports:

| Field               | Type              | Default   | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------- | ----------------- | --------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enabled`           | bool              | `false`   | Whether this server is started.                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `transport`         | string            | `"stdio"` | Transport used to reach the server. One of `stdio` (starts a subprocess) or `http` (connects via HTTP).                                                                                                                                                                                                                                                                                                                                                                                             |
| `command`           | string            | —         | Executable that starts the server. Required for `stdio` transport; must be empty for `http`.                                                                                                                                                                                                                                                                                                                                                                                                        |
| `args`              | []string          | —         | Arguments passed to the command. Used only with `stdio` transport; must be empty for `http`.                                                                                                                                                                                                                                                                                                                                                                                                        |
| `env`               | map[string]string | —         | Extra environment variables for the server process. Used only with `stdio` transport; must be empty for `http`.                                                                                                                                                                                                                                                                                                                                                                                     |
| `url`               | string            | —         | HTTP endpoint (http or https). Required for `http` transport; must be empty for `stdio`.                                                                                                                                                                                                                                                                                                                                                                                                            |
| `headers`           | map[string]string | —         | HTTP headers sent with every request. Used only with `http` transport; must be empty for `stdio`. Header names cannot collide with SDK-reserved names (case-insensitive): `Content-Type`, `Accept`, `Mcp-Protocol-Version`, `Mcp-Session-Id`, `Last-Event-Id`, `Mcp-Method`, `Mcp-Name`, or names with the `Mcp-Param-` prefix. `Authorization` is allowed for static bearer tokens sourced from `${VAR}` (see [environment variable expansion](#environment-variable-expansion-in-config-values)). |
| `approval`          | string            | `"ask"`   | Approval mode for the server's tools. One of `ask` (prompt per tool call), `allow` (run without prompting in build mode, downgraded to `ask` in plan mode), or `deny` (register no tools).                                                                                                                                                                                                                                                                                                          |
| `trust_annotations` | bool              | `false`   | When `true`, tools advertised with `readOnlyHint: true` skip approval; `destructiveHint` and `openWorldHint` tools still prompt.                                                                                                                                                                                                                                                                                                                                                                    |
| `connect_timeout`   | duration          | `"15s"`   | Maximum time to wait for the MCP connection to be established. Absent or `0` falls back to `15s` (mirrors crush's default, range 5-30s); negative values are rejected.                                                                                                                                                                                                                                                                                                                              |
| `allowed_tools`     | []string          | —         | Optional allowlist of MCP-native tool names for this server. Only listed names are registered; registered names like `mcp__<server>__<tool>` are not accepted as entries. Missing means no allowlist restriction; an explicit empty list (`allowed_tools: []`) registers no tools (denies all).                                                                                                                                                                                                     |
| `blocked_tools`     | []string          | —         | Optional denylist of MCP-native tool names. Applied after `allowed_tools`: a tool that survives the allowlist is removed if its name appears here.                                                                                                                                                                                                                                                                                                                                                  |
| `sub_agents`        | []string          | —         | Agent types allowed to use this server's tools. Defaults to closed: missing or `[]` grants no MCP tools to any child. Valid values: `explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, `vision`.                                                                                                                                                                                                                                                                                  |

```yaml
mcp:
  enabled: true
  servers:
    my-stdio-server:
      enabled: true
      command: "npx"
      args: ["-y", "@some/mcp-server"]
      approval: ask
      trust_annotations: false
    my-http-server:
      enabled: true
      transport: http
      url: "http://localhost:3000/mcp"
      headers:
        Authorization: "Bearer ${MCP_AUTH_TOKEN}"
      approval: ask
```

When using `http` transport with an `Authorization` header, use the strict env expansion syntax (e.g. `${VAR}`) to inject environment variables. See the [environment variable expansion](#environment-variable-expansion-in-config-values) section for details.

MCP behaviour is covered by hermetic, CI-safe integration tests under `internal/mcp/` for both transports (stdio and HTTP) through the manager path; live validation against third-party MCP servers remains manual work tracked in #438. See [docs/mcp.md](mcp.md).

---

## `project_context` block

Configures extra files and a byte budget for project-level context injected
into the system prompt.

| Field          | Type     | Default | Description                                                                                                                                                                                                                                                                                           |
| -------------- | -------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `max_bytes`    | int      | `8000`  | Byte budget for extra project context files. The prompt assembler will truncate or skip files to stay within this budget.                                                                                                                                                                             |
| `max_tokens`   | int      | —       | **Deprecated** alias for `max_bytes`. When `max_bytes` is unset, converted to bytes as `max_tokens * 4` at load time; `max_bytes` wins when both are set. When used, a deprecation warning is shown in the interactive TUI at startup and emitted as a `config_warning` event on the `--exec` stream. |
| `extra_files`  | []string | —       | Additional files to include in project context. Paths are relative to the project root.                                                                                                                                                                                                               |
| `ignore_files` | []string | —       | Files to exclude from `extra_files`. There is no automatic project-context discovery.                                                                                                                                                                                                                 |

```yaml
project_context:
  max_bytes: 8000
  extra_files:
    - docs/ARCHITECTURE.md
    - .steiner/skills/project.md
  ignore_files:
    - vendor/
    - generated/
```

---

## `paths` block

Constrains filesystem access for tools that read or write files.

| Field               | Type     | Default | Description                                                                  |
| ------------------- | -------- | ------- | ---------------------------------------------------------------------------- |
| `project_root_only` | bool     | `true`  | When `true`, tools are restricted to paths within the detected project root. |
| `writable_paths`    | []string | `[]`    | Additional paths outside the project root that mutation tools may write to.  |
| `blocked_paths`     | []string | `[]`    | Paths that are always denied, even if they fall within the project root.     |
| `exclude_paths`     | []string | —       | Paths excluded from directory listings and glob results.                     |
| `exclude_patterns`  | []string | —       | Glob patterns excluded from directory listings and glob results.             |

Note: the TUI file picker shows `.steiner/` contents except `.steiner/tmp` and `.steiner/worktrees`, which are always hidden to keep the picker fast. The same exclusion rules still apply to `glob` and `grep` tools.

This block also applies to each child sub-agent's own tool executor, not just the parent's.

```yaml
paths:
  project_root_only: true
  writable_paths:
    - /tmp/steiner-scratch
  blocked_paths:
    - .env
    - secrets/
  exclude_paths:
    - vendor/
    - node_modules/
  exclude_patterns:
    - "**/*.secret"
    - "**/.env*"
```

---

## `logging` block

Controls diagnostic log output.

| Field                 | Type   | Default                                | Description                                                                                                      |
| --------------------- | ------ | -------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `enabled`             | bool   | `false`                                | Whether file logging is active.                                                                                  |
| `level`               | string | `"info"`                               | Minimum log level. One of `debug`, `info`, `warn`, `error`.                                                      |
| `file`                | string | `"~/.local/share/steiner/steiner.log"` | Path to the log file. Tilde expansion is supported. Treat as sensitive — it may capture prompts and tool output. |
| `thinking_chunk`      | bool   | `false`                                | When `true`, reasoning/thinking tokens from the model are included in the log.                                   |
| `compaction_log_file` | string | —                                      | Separate log file for context compaction events. Useful for debugging compaction behaviour.                      |

```yaml
logging:
  enabled: true
  level: debug
  file: ~/.local/share/steiner/steiner.log
  thinking_chunk: false
  compaction_log_file: ~/.local/share/steiner/compaction.log
```

---

## `context_management` block

Baseline context management settings.

| Field              | Type | Default | Description                                                                                                                                                                                      |
| ------------------ | ---- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `read_annotations` | bool | `true`  | When `true`, file reads are annotated in the conversation with metadata (path, line range) to help the model track context provenance. Disable if annotations add unwanted noise for your model. |

```yaml
context_management:
  read_annotations: true
```

This block also applies to each child sub-agent's own context manager, not just the parent's.

---

## TUI preferences

TUI preferences are stored separately from the main config in `~/.config/steiner/prefs.yaml`. They are updated by slash commands in the interactive TUI, not by editing the config YAML.

| Field              | Type   | Default | Description                                                                                                                                                                                                                                                                                                      |
| ------------------ | ------ | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `accent`           | string | `amber` | Accent colour preset for the TUI. Valid values: `amber`, `coral`, `rose`, `magenta`, `gold`, `violet`, `indigo`, `blue`, `cyan`, `teal`, `green`, `mint`, `lime`, `red`, `pink`, `sky`, `lavender`, `terracotta`, `yellow`, `purple`, or `random`. `random` selects a different concrete preset on each startup. |
| `show_thinking`    | bool   | `true`  | When `true`, model reasoning/thinking tokens are rendered in the TUI transcript.                                                                                                                                                                                                                                 |
| `sidebar_position` | string | `left`  | Position of the sidebar panel: `left` or `right`.                                                                                                                                                                                                                                                                |

Use `/accent` in the TUI to open a colour picker (all 20 presets with colour swatches, listed in chromatic order), or `/accent <preset>` to set directly. Use `/thinking` to toggle thinking display. Use `/sidebar` to move the sidebar.

---

## `search` block

Configures web search integration.

| Field            | Type   | Default | Description                                                                                                              |
| ---------------- | ------ | ------- | ------------------------------------------------------------------------------------------------------------------------ |
| `backend`        | string | —       | Search backend to use. One of `searxng`, `google`, `kagi`, `brave`.                                                      |
| `searxng_url`    | string | —       | Base URL of the SearXNG instance. Required when `backend: searxng`.                                                      |
| `google_cx`      | string | —       | Google Custom Search Engine ID. Required when `backend: google`.                                                         |
| `google_api_key` | string | —       | Google API key for Custom Search. Required when `backend: google`. Prefer an environment variable via `STEINER_` prefix. |
| `kagi_api_key`   | string | —       | Kagi API key. Required when `backend: kagi`.                                                                             |
| `brave_api_key`  | string | —       | Brave Search API key. Required when `backend: brave`.                                                                    |

**Per-backend required fields:**

| Backend   | Required fields               |
| --------- | ----------------------------- |
| `searxng` | `searxng_url`                 |
| `google`  | `google_cx`, `google_api_key` |
| `kagi`    | `kagi_api_key`                |
| `brave`   | `brave_api_key`               |

```yaml
search:
  backend: searxng
  searxng_url: http://localhost:8080
```

---

## Examples

### Example 1: minimal local LLM (Ollama or LM Studio)

```yaml
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1

models:
  definitions:
    local:
      provider: local
      id: qwen3:14b
  profiles:
    default:
      default_model: local
```

Use `http://127.0.0.1:1234/v1` as `base_url` for LM Studio. Use `type: ollama`
with `base_url: http://localhost:11434` (no `/v1`) when targeting the Ollama
native endpoint.

---

### Example 2: cloud provider (Anthropic)

```yaml
providers:
  anthropic:
    type: anthropic
    api_key_env: ANTHROPIC_API_KEY

models:
  definitions:
    sonnet:
      provider: anthropic
      id: claude-sonnet-4-5
    opus:
      provider: anthropic
      id: claude-opus-4-5
  profiles:
    default:
      default_model: sonnet
```

For OpenAI, replace `type: anthropic` with `type: openai` and set
`api_key_env: OPENAI_API_KEY`.

---

### Example 3: multi-provider with fallback model selection

```yaml
providers:
  local:
    type: ollama
    base_url: http://localhost:11434
  router:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY

models:
  definitions:
    local-fast:
      provider: local
      id: qwen3:14b
    local-deep:
      provider: local
      id: deepseek-r1:32b
    sonnet:
      provider: router
      id: anthropic/claude-3.7-sonnet
    gpt-4o:
      provider: router
      id: openai/gpt-4o
  profiles:
    default:
      default_model: local-fast
```

Switch models at runtime with `--model sonnet` without changing config.

---

### Example 4: advanced limits with per-tool timeouts

```yaml
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
    timeout: 60s

models:
  definitions:
    local:
      provider: local
      id: qwen3-35b-a3b
      advanced:
        limits:
          context_window: 65536
          max_output_tokens: 16384
      retry:
        enabled: true
        max_attempts: 5
        initial_backoff: 500ms
        max_backoff: 10s
  profiles:
    default:
      default_model: local

limits:
  max_turns: 80
  max_tokens: 800000
  tool_timeout_default: 45s
  tool_timeouts:
    bash: 300s
    read: 10s
    grep: 60s
    ls: 10s
  tool_output_max_bytes: 131072
```

---

### Example 5: kitchen-sink — every block populated

```yaml
tui:
  fps: 60

providers:
  local:
    type: ollama
    base_url: http://localhost:11434
    timeout: 45s
  cloud:
    type: anthropic
    api_key_env: ANTHROPIC_API_KEY
    timeout: 120s
  router:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY
    headers:
      X-Title: steiner

models:
  definitions:
    local-fast:
      provider: local
      id: qwen3:14b
      params:
        temperature: 0.2
        top_p: 0.95
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
      advanced:
        limits:
          context_window: 32768
          max_output_tokens: 8192
        reasoning_echo_back: false
    sonnet:
      provider: cloud
      id: claude-sonnet-4-5
      extra_params:
        metadata:
          user_id: dev
      advanced:
        limits:
          context_window: 200000
          max_output_tokens: 64000
    mini:
      provider: router
      id: openai/gpt-4o-mini
      prompt_suffix: " Answer concisely."
      prompts:
        system: "You are a concise coding assistant."
        system_suffix: "Always respond in structured JSON when possible."
  profiles:
    default:
      default_model: local-fast
      advisor: sonnet
      sub_agents:
        code: sonnet
        evaluate: sonnet
        sanity_check: mini
        review: sonnet
      oneshot:
        plan: local-fast
        implement: sonnet
        review: sonnet
      workflow_handoff:
        implement: sonnet
        review: sonnet

limits:
  max_turns: 60
  max_tokens: 600000
  tool_timeout_default: 30s
  tool_timeouts:
    bash: 180s
    read: 5s
    grep: 30s
    ls: 5s
  tool_output_max_bytes: 65536

sandbox:
  enabled: true

permissions:
  docker: false

sub_agent:
  enabled: true
  max_turns: 25
  max_tokens: 80000

advisor:
  enabled: true
  max_uses_per_run: 3

oneshot:
  auto_pr: false

tools:
  fmt:
    exec: gofmt
    subcommand: -w
    description: Format Go source files in place.
    timeout: 10s
  lint:
    exec: golangci-lint
    subcommand: run
    description: Run golangci-lint on the project.
    timeout: 60s
    parameters:
      path:
        type: string
        description: Directory to lint. Defaults to ./...

project_context:
  max_bytes: 8000
  extra_files:
    - docs/ARCHITECTURE.md
    - .steiner/skills/project.md
  ignore_files:
    - vendor/
    - testdata/

paths:
  project_root_only: true
  writable_paths:
    - /tmp/steiner-scratch
  blocked_paths:
    - .env
    - .env.local
  exclude_paths:
    - vendor/
    - node_modules/
  exclude_patterns:
    - "**/*.secret"

sandbox:
  enabled: true
  host_mounts:
    - path: ~/.kube
      mode: rw
    - path: /opt/tools
      mode: rw

permissions:
  docker: false

logging:
  enabled: true
  level: info
  file: ~/.local/share/steiner/steiner.log
  thinking_chunk: false
  compaction_log_file: ~/.local/share/steiner/compaction.log

context_management:
  read_annotations: true

search:
  backend: searxng
  searxng_url: http://localhost:8080

cave_human: false
```
