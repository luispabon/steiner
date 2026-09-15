# Provider and model setup

This guide shows how to connect Steiner to local or cloud providers, give model IDs reusable aliases, and choose a default model. It is a task-oriented companion to the [configuration reference](configuration.md), which documents every field.

## How the pieces fit

A Steiner setup has four layers:

- A **provider name** is the key under `providers`. It describes how Steiner reaches an API. Names such as `local`, `anthropic`, or `gateway` are yours to choose.
- A **model alias** is a key under `models.definitions`. Its `provider` points to a provider name and its `id` is the model ID expected by that provider.
- `models.profiles.default.default_model` selects the alias used by default. Other profiles can select a different alias.
- Runtime selection can override the active orchestrator model. `--model` is a command-line override, `STEINER_MODEL` is its environment equivalent, and `/model` changes the active model in an interactive session.

Aliases keep provider and model IDs out of commands. Every model selection also accepts a raw `provider/model-id` reference, but aliases are easier to reuse in profiles and scripts.

## Local providers

### Ollama

Start a model in Ollama:

```bash
ollama run qwen2.5-coder:14b
```

Put this in `.steiner/config.yaml`:

```yaml
providers:
  ollama:
    type: ollama

models:
  definitions:
    local:
      provider: ollama
      id: qwen2.5-coder:14b
  profiles:
    default:
      default_model: local
```

The `ollama` provider uses its default local endpoint. If your Ollama server uses another endpoint, set `base_url` under `providers.ollama`.

### LM Studio

Start LM Studio's local server and load a model. Use the model ID reported by that server in the `id` field. For a model using the ID from the example below, use:

```yaml
providers:
  lmstudio:
    type: lmstudio
    base_url: http://127.0.0.1:1234/v1

models:
  definitions:
    local:
      provider: lmstudio
      id: qwen2.5-coder:14b
  profiles:
    default:
      default_model: local
```

If LM Studio reports a different ID for the loaded model, replace only `qwen2.5-coder:14b`. The provider type and URL stay the same unless you changed LM Studio's server settings.

## Cloud providers

Keep API keys out of the config file. `api_key_env` is the **name** of an environment variable. Export that variable before running Steiner.

### Anthropic

```bash
export ANTHROPIC_API_KEY='your-key'
```

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
  profiles:
    default:
      default_model: sonnet
```

### OpenAI

```bash
export OPENAI_API_KEY='your-key'
```

```yaml
providers:
  openai:
    type: openai
    api_key_env: OPENAI_API_KEY

models:
  definitions:
    openai-default:
      provider: openai
      id: gpt-4o
  profiles:
    default:
      default_model: openai-default
```

### OpenRouter

```bash
export OPENROUTER_API_KEY='your-key'
```

```yaml
providers:
  openrouter:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY

models:
  definitions:
    openrouter-default:
      provider: openrouter
      id: openai/gpt-4o
  profiles:
    default:
      default_model: openrouter-default
```

Model IDs for OpenRouter include the upstream model path, such as `openai/gpt-4o`.

## Local and cloud together

A provider name and model alias are separate. That lets one profile use local inference by default while keeping a cloud alias available for selected tasks:

```yaml
providers:
  ollama:
    type: ollama
  anthropic:
    type: anthropic
    api_key_env: ANTHROPIC_API_KEY

models:
  definitions:
    local:
      provider: ollama
      id: qwen2.5-coder:14b
    sonnet:
      provider: anthropic
      id: claude-sonnet-4-5
  profiles:
    default:
      default_model: local
      sub_agents:
        code: sonnet
    local-only:
      default_model: local
```

With `ANTHROPIC_API_KEY` exported, the default profile uses Ollama for the main model and Anthropic for `code` sub-agents. Select the alternate profile for a local-only run:

```bash
steiner --profile local-only
```

Use the cloud alias for one run without changing the file:

```bash
steiner --model sonnet --exec "review this repository"
```

## Gateways and OpenAI-compatible APIs

For a generic OpenAI-compatible server, configure `openai_compat` with its API URL. The provider name and model ID depend on that server:

```yaml
providers:
  gateway:
    type: openai_compat
    base_url: <gateway-base-url>
    api_key_env: GATEWAY_API_KEY

models:
  definitions:
    gateway-model:
      provider: gateway
      id: <gateway-model-id>
  profiles:
    default:
      default_model: gateway-model
```

Set `GATEWAY_API_KEY` only if the gateway requires it. LiteLLM has a dedicated provider type, so use `type: litellm` with the same `base_url`, `api_key_env`, and model-definition pattern. OpenCode gateways use `type: opencode_go` or `type: opencode_zen` instead; those types have their own default URLs and require `api_key` or `api_key_env`. See the [provider types](configuration.md#provider-types) table for the supported details.

## Profiles and runtime overrides

The `default` profile must define `default_model`. A named profile can change the default and role assignments. Select one at startup with `--profile`:

```bash
steiner --profile local-only
```

Model selection precedence for the active orchestrator is:

1. The selected profile's `default_model`.
2. `STEINER_MODEL`, when set to an alias or valid `provider/model-id` reference.
3. `--model`, which takes precedence over `STEINER_MODEL`.

These overrides affect the active orchestrator only. Profile role assignments, such as `sub_agents`, continue to come from the selected profile. In interactive mode, start Steiner and enter `/model sonnet` to change the active orchestrator for that session. Use `/profile local-only` to select a profile for future role assignments.

## Verify a setup

Run these commands from the project containing `.steiner/config.yaml`:

```bash
# Print the merged configuration.
steiner config

# Resolve an alias and inspect its provider, backend ID, and transport.
steiner model inspect local

# Run a one-shot request with a selected alias.
steiner --model local --exec "summarize this repository in one sentence"
```

For an interactive check, run `steiner`, then enter `/model local`. If the alias or provider cannot be resolved, `config` or `model inspect` reports the configuration error before a model request is made. The same commands work with `go run ./cmd/steiner` when running from a source checkout.

## Codex

Codex uses OAuth rather than an API key. Follow the [Codex OAuth setup](optional-features.md#codex-oauth) instead of adding a second Codex recipe here.
