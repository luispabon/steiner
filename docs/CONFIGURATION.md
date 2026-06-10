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
5. CLI flags

Key environment variables:

| Variable                      | Maps to                           |
|-------------------------------|-----------------------------------|
| `STEINER_MODEL`               | `default_model`                   |
| `STEINER_SCHEDULER_PARALLELISM` | `scheduler.parallelism`         |
| `STEINER_MAX_TURNS`           | `limits.max_turns`                |
| `STEINER_MAX_TOKENS`          | `limits.max_tokens`               |
| `STEINER_CAVEMAN_MODE`        | `caveman_mode`                    |
| `STEINER_LOG_LEVEL`           | `logging.level`                   |
| `STEINER_LOG_FILE`            | `logging.file`                    |
| `STEINER_TOOL_OUTPUT_MAX_BYTES` | `limits.tool_output_max_bytes`  |

---

## Top-level fields

| Field               | Type     | Default     | Description |
|---------------------|----------|-------------|-------------|
| `default_model`     | string   | `"default"` | Name of the model alias to use when none is specified on the command line. Must reference a key in `models`. |
| `caveman_mode`      | bool     | `false`     | When `true`, enables caveman mode — extremely terse model output for minimal token use. |

---

## `scheduler` block

Controls provider-level concurrency.

| Field         | Type | Default | Description |
|---------------|------|---------|-------------|
| `parallelism` | int  | `1`     | Maximum number of concurrent in-flight provider requests. Set higher only when using a provider that supports parallel calls. |

```yaml
scheduler:
  parallelism: 1
```

---

## `providers` block

A map of named provider configurations. Each entry describes how steiner
connects to a model API. The key is an arbitrary name referenced by `models`.

```yaml
providers:
  <name>:
    type: <provider_type>
    ...
```

### `ProviderConfig` fields

| Field         | Type                      | Default  | Description |
|---------------|---------------------------|----------|-------------|
| `type`        | string (ProviderType)     | —        | The provider type. See [Provider types](#provider-types) below. |
| `base_url`    | string                    | `"http://localhost:11434/v1"` (for the built-in `local` provider) | Base URL for the API endpoint. |
| `api_key`     | string                    | —        | API key value. Prefer `api_key_env` to avoid committing secrets. |
| `api_key_env` | string                    | —        | Name of an environment variable containing the API key. Loaded at startup. |
| `headers`     | map[string]string         | —        | Additional HTTP headers sent with every request to this provider. |
| `timeout`     | duration string           | `"30s"`  | Per-request HTTP timeout. Accepts Go duration strings: `30s`, `2m`, etc. |

### Provider types

| Type             | Description |
|------------------|-------------|
| `openai_compat`  | Generic OpenAI-compatible HTTP API. Works with any server that follows the OpenAI chat completions shape. |
| `ollama`         | Ollama server. Uses Ollama's native endpoint conventions. `base_url` defaults to `http://localhost:11434`. |
| `lmstudio`       | LM Studio's built-in OpenAI-compatible server. Typically at `http://127.0.0.1:1234/v1`. |
| `openrouter`     | OpenRouter cloud gateway. Requires `api_key` or `api_key_env`. No `base_url` needed. |
| `openai`         | Native OpenAI API. Requires `api_key` or `api_key_env`. No `base_url` needed. |
| `anthropic`      | Native Anthropic API. Requires `api_key` or `api_key_env`. No `base_url` needed. |
| `gemini`         | Native Google Gemini API. Requires `api_key` or `api_key_env`. No `base_url` needed. |
| `litellm`        | LiteLLM gateway endpoint. Works like `openai_compat` but with LiteLLM-specific retry handling: when a 429 response lacks a `Retry-After` header, steiner parses the delay from the response body (e.g. "Try again in N seconds"). Budget-exhaustion 429s are detected and treated as non-retryable. Set `base_url` to your LiteLLM server. |

**Field applicability by provider type:**

| Field         | openai_compat | ollama | lmstudio | openrouter | openai | anthropic | gemini | litellm |
|---------------|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| `base_url`    | required | optional | required | — | — | — | — | required |
| `api_key`     | optional | — | — | ✓ | ✓ | ✓ | ✓ | optional |
| `api_key_env` | optional | — | — | ✓ | ✓ | ✓ | ✓ | optional |
| `headers`     | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `timeout`     | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

---

## `models` block

A map of named model aliases. Each entry binds a provider to a specific model
ID and sets request-level parameters.

```yaml
models:
  <name>:
    provider: <provider_name>
    id: <model_id>
    ...
```

### `ModelConfig` fields

| Field           | Type                 | Default | Description |
|-----------------|----------------------|---------|-------------|
| `provider`      | string               | `"local"` | Name of the provider (key in `providers`) to use for this model. |
| `id`            | string               | `"qwen3-35b-a3b"` | The model identifier as expected by the provider API (e.g. `claude-sonnet-4-5`, `gpt-4o`, `llama3:8b`). |
| `params`        | map[string]any       | —       | Standard request parameters sent in the request body. Common keys: `temperature`, `top_p`, `top_k`. |
| `extra_params`  | map[string]any       | —       | Additional provider-specific parameters merged into the request body. Use for fields not covered by `params`. |
| `prompt_suffix` | string               | —       | Text appended to every user message before sending to the model. Useful for model-specific control tokens (e.g. `<|think_off|>`). |
| `retry`         | RetryConfig          | see below | Retry policy for failed or rate-limited requests. |
| `prompts`       | ModelPrompts         | —       | Per-model system and compaction prompt overrides. |
| `advanced`      | AdvancedConfig       | see below | Token limit and inference-level settings. |

### `RetryConfig` fields

| Field            | Type            | Default  | Description |
|------------------|-----------------|----------|-------------|
| `enabled`        | bool            | `true`   | Whether to retry on transient errors. |
| `max_attempts`   | int             | `3`      | Maximum number of total attempts (initial + retries). |
| `initial_backoff`| duration string | `"250ms"`| Wait time before the first retry. |
| `max_backoff`    | duration string | `"5s"`   | Upper cap on exponential backoff wait time. |
| `retry_after_max`| duration string | `"30s"`  | Maximum time to honour a `Retry-After` header before giving up. |

### `ModelPrompts` fields

| Field        | Type   | Default | Description |
|--------------|--------|---------|-------------|
| `system`     | string | —       | Overrides the embedded default system prompt for this model. |
| `compaction` | string | —       | Overrides the embedded compaction (context summarisation) prompt for this model. |

### `AdvancedConfig` fields

| Field                | Type                 | Default | Description |
|----------------------|----------------------|---------|-------------|
| `limits`             | AdvancedLimitsConfig | see below | Token budget settings for this model. |
| `reasoning_echo_back`| *bool                | —       | When set, controls whether reasoning tokens are echoed back in the response. Provider-dependent. |
| `transport`          | string               | `"auto"`| Transport override for request formatting. Supported values: `auto`, `openai_compat`, `anthropic`. `auto` uses models.dev metadata when available and otherwise keeps the configured provider type. |

#### Automatic transport resolution

When `transport` is set to `auto` (the default), Steiner resolves the request transport per model using this precedence:

1. **Explicit config override** — If `models.<alias>.advanced.transport` is set to `openai_compat` or `anthropic`, that value wins unconditionally.
2. **models.dev metadata** — If the models.dev cache lists a provider NPM (e.g. `@ai-sdk/anthropic`) for the model, Steiner switches the effective provider type to match the metadata transport.
3. **Configured provider type** — If neither override nor metadata is available, the transport from the model's configured `provider` entry is used.

This means a single `openai_compat` provider can serve both OpenAI-compatible and Anthropic-native models as long as the metadata is available. Use `steiner model inspect <alias>` to see the resolved `effective_provider_type`, `effective_transport`, and `transport_override_reason` for any model.

### `AdvancedLimitsConfig` fields

| Field              | Type | Default  | Description |
|--------------------|------|----------|-------------|
| `context_window`   | int  | `32768`  | Maximum context window size in tokens. Used by the prompt assembler to budget context. |
| `max_output_tokens`| int  | `8192`   | Maximum tokens the model may generate per response. |

---

## `sandbox` block

Controls bubblewrap sandbox behavior.

| Field     | Type | Default | Description |
|-----------|------|---------|-------------|
| `enabled` | bool | `true`  | Enable bubblewrap sandboxing. Set to `false` with `--unsafe` flag. |

```yaml
sandbox:
  enabled: true  # default; use --unsafe flag to disable at runtime
```

---

## `permissions` block

Opt-in permissions for additional capabilities.

| Field    | Type | Default | Description |
|----------|------|---------|-------------|
| `docker` | bool | `false` | Mount Docker socket into sandbox (for tools that need Docker access). |

```yaml
permissions:
  docker: false
```

---

## `host_mounts`

Additional host paths to bind-mount into the sandbox. Use `mode: rw` to grant writable access to paths outside the workspace (all host paths are already readable through the root bind).

Each entry has:

| Field  | Type   | Description |
|--------|--------|-------------|
| `path` | string | Host path to mount (supports `~` expansion). |
| `mode` | string | Mount mode: `rw` for writable access (host is already read-only by default) or `ro`. |

```yaml
host_mounts:
  - path: ~/.kube
    mode: rw
  - path: /opt/tools
    mode: rw
```

---

## `limits` block

Runtime limits for turns, tokens, and tool execution.

| Field                | Type                      | Default  | Description |
|----------------------|---------------------------|----------|-------------|
| `max_turns`          | int                       | `50`     | Maximum agent loop turns before the run is stopped. |
| `max_tokens`         | int                       | `500000` | Maximum total tokens (input + output) consumed before the run is stopped. |
| `tool_timeout_default`| duration string          | `"30s"`  | Default timeout applied to any tool not listed in `tool_timeouts`. |
| `tool_timeouts`      | map[string]duration string| see below | Per-tool timeout overrides. |
| `tool_output_max_bytes`| int                     | `65536`  | Maximum bytes of output captured from a single tool call. Output is truncated to this limit. |

Default `tool_timeouts`:

| Tool   | Timeout |
|--------|---------|
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
```

---

## `sub_agent` block

Controls delegated child-agent execution. For details on what sub-agents can
do and tool allowlists for each specialised agent type, see
[docs/SUBAGENT_DELEGATION.md](SUBAGENT_DELEGATION.md).

| Field          | Type                       | Default                                  | Description |
|----------------|----------------------------|------------------------------------------|-------------|
| `enabled`      | bool                       | `true`                                   | Master switch. Set to `false` to remove all delegation tools from the model. |
| `max_turns`    | int                        | `30`                                     | Maximum turns allowed for each child agent run. A floor of 15 turns is enforced internally. |
| `max_tokens`   | int                        | `100000`                                 | Maximum tokens a child agent may consume. |
| `allowed_tools`| []string                   | `["read","glob","grep","ls","bash"]`     | Tools available to the generic `delegate` sub-agent. Specialised agents (explore/research/code/plan/verify) use their own hardcoded allowlists and ignore this field. |
| `agents`       | map[string]AgentConfig     | —                                        | Per-agent-type configuration. Key is the agent type (e.g. `code`, `research`, `explore`). |

### `AgentConfig` fields

| Field   | Type   | Default | Description |
|---------|--------|---------|-------------|
| `model` | string | —       | Optional model alias override for this agent type. When set, sub-agents of this type use this model instead of the parent's active model. Must reference a key in `models`. |

```yaml
sub_agent:
  enabled: true
  max_turns: 30
  max_tokens: 100000
  allowed_tools:
    - read
    - glob
    - grep
    - ls
    - bash
  agents:
    code:
      model: gpt-4o
    research:
      model: claude-sonnet-4
```

---

## `tools` block

A map of externally configured tools registered alongside the built-in tools.
Each key becomes the tool name the model uses.

```yaml
tools:
  <tool_name>:
    exec: <binary_path>
    ...
```

### `ToolConfig` fields

| Field         | Type            | Default | Description |
|---------------|-----------------|---------|-------------|
| `exec`        | string          | —       | Path to the executable to run when this tool is called. |
| `subcommand`  | string          | —       | Subcommand or argument passed as the first positional argument to `exec`. |
| `description` | string          | —       | Human-readable description shown to the model in the tool schema. |
| `parameters`  | map[string]any  | —       | JSON Schema fragment describing the tool's input parameters. |
| `timeout`     | duration string | —       | Per-tool timeout. Overrides `limits.tool_timeout_default` for this tool. |
| `constraints` | map[string]any  | —       | Arbitrary constraint metadata passed to the tool executor. |

```yaml
tools:
  fmt:
    exec: gofmt
    subcommand: -w
    description: Format Go source files in place.
    timeout: 10s
```

---

## `project_context` block

Configures extra files and token budget for project-level context injected
into the system prompt.

| Field         | Type     | Default | Description |
|---------------|----------|---------|-------------|
| `max_tokens`  | int      | `2000`  | Maximum tokens allocated to project context files. The prompt assembler will truncate or skip files to stay within this budget. |
| `extra_files` | []string | —       | Additional files to include in project context. Paths are relative to the project root. |
| `ignore_files`| []string | —       | Files to exclude from automatic project context discovery. |

```yaml
project_context:
  max_tokens: 4000
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

| Field              | Type     | Default | Description |
|--------------------|----------|---------|-------------|
| `project_root_only`| bool     | `true`  | When `true`, tools are restricted to paths within the detected project root. |
| `writable_paths`   | []string | `[]`    | Additional paths outside the project root that mutation tools may write to. |
| `blocked_paths`    | []string | `[]`    | Paths that are always denied, even if they fall within the project root. |
| `exclude_paths`    | []string | —       | Paths excluded from directory listings and glob results. |
| `exclude_patterns` | []string | —       | Glob patterns excluded from directory listings and glob results. |

Note: the TUI file picker always shows `.steiner/` and its contents, regardless of the rules above. The same exclusion rules still apply to `glob` and `grep` tools.

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

| Field                | Type   | Default                                  | Description |
|----------------------|--------|------------------------------------------|-------------|
| `enabled`            | bool   | `false`                                  | Whether file logging is active. |
| `level`              | string | `"info"`                                 | Minimum log level. One of `debug`, `info`, `warn`, `error`. |
| `file`               | string | `"~/.local/share/steiner/steiner.log"`  | Path to the log file. Tilde expansion is supported. Treat as sensitive — it may capture prompts and tool output. |
| `thinking_chunk`     | bool   | `false`                                  | When `true`, reasoning/thinking tokens from the model are included in the log. |
| `compaction_log_file`| string | —                                        | Separate log file for context compaction events. Useful for debugging compaction behaviour. |

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

| Field             | Type | Default | Description |
|-------------------|------|---------|-------------|
| `read_annotations`| bool | `true`  | When `true`, file reads are annotated in the conversation with metadata (path, line range) to help the model track context provenance. Disable if annotations add unwanted noise for your model. |

```yaml
context_management:
  read_annotations: true
```

---

## `search` block

Configures web search integration.

| Field           | Type   | Default | Description |
|-----------------|--------|---------|-------------|
| `backend`       | string | —       | Search backend to use. One of `searxng`, `google`, `kagi`, `brave`. |
| `searxng_url`   | string | —       | Base URL of the SearXNG instance. Required when `backend: searxng`. |
| `google_cx`     | string | —       | Google Custom Search Engine ID. Required when `backend: google`. |
| `google_api_key`| string | —       | Google API key for Custom Search. Required when `backend: google`. Prefer an environment variable via `STEINER_` prefix. |
| `kagi_api_key`  | string | —       | Kagi API key. Required when `backend: kagi`. |
| `brave_api_key` | string | —       | Brave Search API key. Required when `backend: brave`. |

**Per-backend required fields:**

| Backend    | Required fields |
|------------|-----------------|
| `searxng`  | `searxng_url` |
| `google`   | `google_cx`, `google_api_key` |
| `kagi`     | `kagi_api_key` |
| `brave`    | `brave_api_key` |

```yaml
search:
  backend: searxng
  searxng_url: http://localhost:8080
```

---

## Examples

### Example 1: minimal local LLM (Ollama or LM Studio)

```yaml
default_model: local

providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1

models:
  local:
    provider: local
    id: qwen3:14b
```

Use `http://127.0.0.1:1234/v1` as `base_url` for LM Studio. Use `type: ollama`
with `base_url: http://localhost:11434` (no `/v1`) when targeting the Ollama
native endpoint.

---

### Example 2: cloud provider (Anthropic)

```yaml
default_model: sonnet

providers:
  anthropic:
    type: anthropic
    api_key_env: ANTHROPIC_API_KEY

models:
  sonnet:
    provider: anthropic
    id: claude-sonnet-4-5
  opus:
    provider: anthropic
    id: claude-opus-4-5
```

For OpenAI, replace `type: anthropic` with `type: openai` and set
`api_key_env: OPENAI_API_KEY`.

---

### Example 3: multi-provider with fallback model selection

```yaml
default_model: local-fast

providers:
  local:
    type: ollama
    base_url: http://localhost:11434
  router:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY

models:
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
```

Switch models at runtime with `--model sonnet` without changing config.

---

### Example 4: advanced limits with per-tool timeouts

```yaml
default_model: local

providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
    timeout: 60s

models:
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
default_model: local-fast

scheduler:
  parallelism: 2

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

default_model: local-fast

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
  allowed_tools:
    - read
    - glob
    - grep
    - ls
    - bash
  agents:
    code:
      model: sonnet
    research:
      model: mini

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
  max_tokens: 4000
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

permissions:
  docker: false

host_mounts:
  - path: ~/.kube
    mode: rw
  - path: /opt/tools
    mode: rw

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

caveman_mode: false
```
