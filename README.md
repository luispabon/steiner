# steiner

`steiner` is a minimal, local-first coding agent in Go. It is built to work against real local repositories with bounded context, explicit approvals, and a split provider/model configuration that supports local OpenAI-compatible servers plus native provider integrations.

## Today in brief

* Single-agent loop with interactive terminal mode and `--exec`
* Config, tools, skills, and version commands are available now
* Default provider targets a local OpenAI-compatible endpoint
* Mutating tools are approval-gated by default; reads are auto-approved
* Context management via delegation, per-source budgets, and compaction for lean local LLM usage

## Context management

Local LLMs have limited context windows — often measured in thousands of tokens, not millions. Every turn accumulates tokens from model output and tool results, and long contexts cost more while degrading reasoning quality. Steiner keeps the context window lean through three lines of defense, ordered by effectiveness:

1. **Delegation** — sub-agents isolate work from the parent conversation. The full turn-by-turn transcript of exploration, code changes, or research never enters the parent context at all.
2. **Per-source byte budgets** — when context does accumulate, budgets cap each source (preamble, agents, skills, tool results, etc.) so no single category dominates the window.
3. **Compaction** — when estimated prompt tokens reach 70% of the context window, older turns are summarised by the model into a compact durable prefix.

See [docs/CONTEXT_MANAGEMENT.md](docs/CONTEXT_MANAGEMENT.md) for the full reference.

## Quickstart

Requirements: Go `1.25+`.

1. Start from source in this repo.
2. Make sure an OpenAI-compatible server is running at `http://localhost:11434/v1`, or override the provider/model settings in config.
3. Run a first request from source:

```bash
go run ./cmd/steiner --exec "summarize this repository in one sentence"
```

4. Optional: inspect the resolved provider/model configuration before you do real work:

```bash
go run ./cmd/steiner config
```

If you want a local binary:

```bash
make build-binaries
./bin/steiner
```

## Common commands

* `go run ./cmd/steiner` or `./bin/steiner` - interactive mode
* `go run ./cmd/steiner --exec "..."` - run one request and exit
* `go run ./cmd/steiner version` - print the build version
* `go run ./cmd/steiner config` - print the resolved configuration, including provider and model definitions
* `go run ./cmd/steiner tools` - list configured tools
* `go run ./cmd/steiner skills` - list discovered skills

## Configuration

Configuration precedence is:

1. compiled defaults
2. `~/.config/steiner/config.yaml`
3. `.steiner/config.yaml`
4. environment variables with the `STEINER_` prefix
5. CLI flags

Key environment variables:

* `STEINER_MODEL`
* `STEINER_SCHEDULER_PARALLELISM`
* `STEINER_MAX_TURNS`
* `STEINER_MAX_TOKENS`
* `STEINER_CAVEMAN_MODE`
* `STEINER_LOG_LEVEL`
* `STEINER_LOG_FILE`
* `STEINER_TOOL_OUTPUT_MAX_BYTES`

### Config format

Configuration is split between `providers` and `models`:

* `providers.<name>` defines how Steiner connects to an API endpoint or native provider.
* `models.<name>` defines a selectable model alias, references one provider with `provider`, and sets model-specific request behavior.
* `default_model` selects the model alias used by default.

The compiled defaults in `internal/config/defaults.go` are equivalent to one local provider plus one default model alias.

Example 1: minimal local config for LM Studio or Ollama:

```yaml
default_model: local
providers: {local: {type: openai_compat, base_url: http://localhost:11434/v1}}
models: {local: {provider: local, id: qwen3-35b-a3b}}
```

Use `http://127.0.0.1:1234/v1` as the `base_url` when pointing the same shape at LM Studio.

Example 2: OpenRouter with `api_key_env`:

```yaml
default_model: sonnet

providers:
  openrouter:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY

models:
  sonnet:
    provider: openrouter
    id: anthropic/claude-3.7-sonnet
```

Example 3: multiple providers with multiple models:

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
  gpt-4.1-mini:
    provider: router
    id: openai/gpt-4.1-mini
```

Example 4: tuned model with `params`, `extra_params`, and a prompt suffix:

```yaml
default_model: precise

providers:
  local:
    type: lmstudio
    base_url: http://127.0.0.1:1234/v1

models:
  precise:
    provider: local
    id: qwen/qwen3-coder-30b
    params:
      temperature: 0.1
      top_p: 0.9
    extra_params:
      frequency_penalty: 0
      metadata:
        profile: precise
    prompt_suffix: <|think_off|>
```

Example 5: advanced overrides with explicit limits:

```yaml
default_model: long-context

providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
    timeout: 45s

models:
  long-context:
    provider: local
    id: qwen3-32b
    advanced:
      limits:
        context_window: 131072
        max_output_tokens: 8192

limits:
  max_turns: 80
  max_tokens: 900000
  tool_timeout_default: 45s
  tool_output_max_bytes: 131072
```

Example 6: multi-model setup with `default_model`:

```yaml
default_model: everyday

providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1

models:
  everyday:
    provider: local
    id: qwen3-14b
  review:
    provider: local
    id: qwen3-32b
    extra_params:
      thinking:
        type: enabled
        budget_tokens: 16000
```

Provider fields:

* `type`: `openai_compat`, `ollama`, `lmstudio`, `openrouter`, `openai`, `anthropic`, `gemini`, or `litellm`
* `base_url`: endpoint override when the provider type uses one
* `api_key` or `api_key_env`: credential source
* `headers`: optional extra HTTP headers
* `timeout`: provider request timeout

Model fields:

* `provider`: provider name from `providers`
* `id`: backend model identifier sent to the provider
* `params`: normalized generation params
* `extra_params`: provider-specific request fields merged on top of `params`
* `prompt_suffix`: optional text appended to the last user message for each model request
* `retry`: retry policy for model requests
* `prompts`: per-model prompt overrides
* `advanced.limits`: prompt budgeting and output token limits

Approval defaults are conservative: `read`, `glob`, `grep`, and `ls` are auto-approved; mutating actions like `write`, `edit`, and `bash` prompt first. For most installs, the minimum useful config is `default_model`, one provider entry, one model entry that points at that provider, and any overrides you actually need in `limits`, `approval`, `tools`, `project_context`, `paths`, or `logging`.

## Web search

The `web_search` tool lets the model search the web and return URL, title, and description results. It is not available by default — it must be enabled by setting `search.backend` in the config.

### Supported backends

| Backend | Config key | Auth |
|---------|------------|------|
| **Google** | `google` | `GOOGLE_SEARCH_CX` + `GOOGLE_SEARCH_API_KEY` env vars |
| **Kagi** | `kagi` | `KAGI_API_KEY` env var |
| **Brave** | `brave` | `BRAVE_API_KEY` env var |
| **SearXNG** | `searxng` | `search.searxng_url` config key (self-hosted, no API key) |

Setting `search.backend` to `""` (or omitting the `search` block entirely) disables the `web_search` tool. The model will not have access to web search.

### Config example

```yaml
search:
  backend: google          # one of: google, kagi, brave, searxng
  # searxng_url: http://localhost:8888   # required when backend is "searxng"
```

#### Per-backend environment variables

**Google:**
```bash
export GOOGLE_SEARCH_CX=your_custom_search_engine_id
export GOOGLE_SEARCH_API_KEY=your_google_api_key
```

**Kagi:**
```bash
export KAGI_API_KEY=your_kagi_api_key
```

**Brave:**
```bash
export BRAVE_API_KEY=your_brave_api_key
```

**SearXNG:** no API key required. Point `search.searxng_url` at your instance.

### How it works

The search config is loaded from the `search` block in `config.yaml` (or the equivalent `STEINER_SEARCH_*` env vars). At startup `NewSearchBackend` dispatches on `search.backend` and creates the corresponding `web.Searcher` implementation.

When a searcher is available:
- The `web_search` tool is registered in the active tool registry (visible to the main model).
- The `research` sub-agent also gains `web_search` in its allowlist.

When no searcher is available (`search.backend` is unset):
- The `web_search` tool is not registered at all — the model never sees it.
- The `research` sub-agent exclude `web_search` from its available tools.

### All providers config example

This example shows all four backends configured across different config layers (only one can be active at a time — set `search.backend` to pick):

```yaml
# Only one backend is active at a time, selected by search.backend.
# Environment variables must be set for the selected backend (see above).
search:
  backend: brave            # change to google, kagi, or searxng as needed
  searxng_url: http://localhost:8888   # only used when backend is "searxng"
```

### Notes

- The `web_search` tool is separate from the model provider — it uses its own HTTP client and does not route through the model provider configuration.
- SearXNG is self-hosted and has no API key; any public or private instance URL works.
- Brave caps results at 20. All other backends cap at 30 (the global limit is adjustable per invocation via the `limit` parameter, up to 30).

## Sub-agent delegation

Delegation is steiner's primary context management strategy — the best turn is the one that never enters the parent conversation. `steiner` exposes seven sub-agent-as-tool operations that delegate bounded tasks to isolated child agents. Sub-agent delegation is **enabled by default** — the model sees the following tools:

- **`explore`** — navigate the codebase to find files, symbols, call sites, and patterns
- **`research`** — gather and synthesise information from the codebase or web (only available with a `web_search` backend configured)
- **`code`** — implement a scoped change, run tests, report results
- **`plan`** — analyse a sub-problem and produce a structured recommendation
- **`verify`** — run checks (tests, linters, builds) and report pass/fail
- **`delegate`** — generic sub-agent with custom system prompt, context, and per-invocation overrides
- **`follow_up`** — resume an existing sub-agent session by ID with a new user message; preserves conversation history while resetting the child's budget to fresh defaults

### Configuration

Sub-agents are configured under the `sub_agent` key in `config.yaml`:

```yaml
sub_agent:
  enabled: true                  # set to false to remove sub-agent tools
  max_turns: 30                  # default turn limit
  max_tokens: 100000             # default output token budget
  allowed_tools:                 # tools for generic `delegate` only
    - read
    - glob
    - grep
    - ls
    - write
    - edit
    - bash
  agents:                        # per-type model overrides (optional)
    code:
      model: gpt-4o
    research:
      model: claude-sonnet-4
```

See [docs/SUBAGENT_DELEGATION.md](docs/SUBAGENT_DELEGATION.md) for full documentation — including agent-specific tool allowlists, safety restrictions, and per-invocation overrides for the `delegate` tool.

## Caveman mode

Caveman mode makes the model speak tersely, stripping filler, articles, pleasantries, and hedging. Reduces tokens and response length while keeping technical content intact.

**Disabled by default.** Enable explicitly via config, env var, CLI flag, or `/caveman` slash command in interactive mode.

```yaml
# config.yaml
caveman_mode: true
```

```bash
# environment
STEINER_CAVEMAN_MODE=true

# CLI flag
--caveman
```

In interactive TUI, toggle on/off with `/caveman`. The toggle persists for the session.

When enabled, caveman-style instructions are injected into:

- **System preamble** — the main agent prompt instructs the model to respond tersely
- **Compaction prompts** — compaction summaries are written in caveman style to maximise information per token
- **Sub-agent prompts** — delegated agents inherit the terseness instruction

Caveman mode is complementary to delegation and compaction — it reduces *output* token growth while the other mechanisms handle *input*-side growth. It is purely a prompt-layer transformation and does not change tool behavior, approval gates, or any other config.

## Development

### Build and test

```bash
go build ./...
go test ./...
make build-binaries
go vet ./...
```

### Development checks

Install local check tools:

```bash
make install-check-tools
```

Run all checks:

```bash
make check
```

Formatting:

```bash
make fmt
```

The full linter configuration is in `.golangci.yml` at the repo root.

## Development notes

Repo layout is compact: `cmd/` for entrypoints, `internal/` for agent/provider/tool/config code, `docs/` for product docs, and `testdata/` for fixtures. Conventions and deeper repo rules live in [AGENTS.md](AGENTS.md).
