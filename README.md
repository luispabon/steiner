# steiner

`steiner` is a minimal, local-first coding agent in Go. It is built to work against real local repositories with bounded context, explicit approvals, and a split provider/model configuration that supports local OpenAI-compatible servers plus native provider integrations.

## Today in brief

* Single-agent loop with interactive terminal mode and `--exec`
* Config, tools, skills, and version commands are available now
* Default provider targets a local OpenAI-compatible endpoint
* Mutating tools are approval-gated by default; reads are auto-approved

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

Example 4: tuned model with `params`, `extra_params`, and thinking disabled:

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
    thinking_enabled: false
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
        output_reserve_tokens: 4096
        safety_margin_tokens: 4096
        summary_max_tokens: 1536

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
    thinking_scaffold_inference: true
    thinking_params:
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
* `thinking_enabled`, `thinking_disable_marker`, `thinking_scaffold_inference`, `thinking_params`: reasoning controls
* `retry`: retry policy for model requests
* `prompts`: per-model prompt overrides
* `advanced.limits`: prompt budgeting and output token limits

Approval defaults are conservative: `read`, `glob`, `grep`, and `ls` are auto-approved; mutating actions like `write`, `edit`, and `bash` prompt first. For most installs, the minimum useful config is `default_model`, one provider entry, one model entry that points at that provider, and any overrides you actually need in `limits`, `approval`, `tools`, `project_context`, `paths`, or `logging`.

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

Fast local loop:

```bash
make quick-check
```

Full local/CI gate:

```bash
make ci-check
```

Formatting:

```bash
make fmt
```

The full linter configuration is in `.golangci.yml` at the repo root.

## Development notes

Repo layout is compact: `cmd/` for entrypoints, `internal/` for agent/provider/tool/config code, `docs/` for product docs, and `testdata/` for fixtures. Conventions and deeper repo rules live in [AGENTS.md](AGENTS.md).

## Further reading

* [AGENTS.md](AGENTS.md)
* [docs/PRD.md](docs/PRD.md)
* [docs/ROADMAP.md](docs/ROADMAP.md)
* [docs/INITIAL_IMPLEMENTATION_PLAN.md](docs/INITIAL_IMPLEMENTATION_PLAN.md)
