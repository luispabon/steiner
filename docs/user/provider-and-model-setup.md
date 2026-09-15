# Provider and model setup

## The model reference contract

`providers.<name>` configures a connection. A model reference selects a model through that configured provider:

- `providers.<name>` must exist. The provider name is the key you chose under `providers`, not a display name.
- `<name>/<model-id>` is the normal model reference. Model IDs may contain slashes, for example `openrouter/openai/gpt-4o`.
- `models.definitions` is optional. Its keys are aliases for model definitions that need a shorter or stable name, or persistent `ModelConfig` settings.

Raw references work in `models.profiles` role assignments (`default_model`, `advisor`, `sub_agents`, `oneshot`, and `workflow_handoff`), `--model`, `STEINER_MODEL`, and `/model`. Raw references carry only provider and model ID. They cannot persist settings such as reasoning, request parameters, retry policy, prompts, transport, or limits. Use an alias when one of those settings belongs to a model.

An exact configured alias takes precedence over raw parsing. Otherwise Steiner chooses the longest configured provider prefix, so model IDs can contain slashes.

## Local providers

### Ollama

Start a model in Ollama:

```bash
ollama run qwen2.5-coder:14b
```

Configure the provider and raw model reference in `.steiner/config.yaml`:

```yaml
providers:
  ollama:
    type: ollama
    base_url: http://localhost:11434/v1

models:
  profiles:
    default:
      default_model: ollama/qwen2.5-coder:14b
```

The Ollama provider uses the OpenAI-compatible API at the configured `/v1` base URL. Replace the model ID with one available in your Ollama installation.

### LM Studio

Start LM Studio's local server, load a model, and use the model ID reported by that server:

```yaml
providers:
  lmstudio:
    type: lmstudio
    base_url: http://127.0.0.1:1234/v1

models:
  profiles:
    default:
      default_model: lmstudio/<model-id>
```

## Cloud providers

Keep API keys out of the config file. `api_key_env` is the name of an environment variable. Export that variable before running Steiner.

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
  profiles:
    default:
      default_model: openai/<model-id>
```

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
  profiles:
    default:
      default_model: anthropic/<model-id>
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
  profiles:
    default:
      default_model: openrouter/<provider-model-id>
```

OpenRouter model IDs commonly contain a slash, such as `openai/gpt-4o`, so the complete reference can be `openrouter/openai/gpt-4o`.

## OpenAI-compatible gateways

Use `openai_compat` for a server that exposes the OpenAI-compatible chat-completions shape. Set the URL and key requirements for that server:

```yaml
providers:
  gateway:
    type: openai_compat
    base_url: <gateway-base-url>
    api_key_env: GATEWAY_API_KEY

models:
  profiles:
    default:
      default_model: gateway/<model-id>
```

Omit `api_key_env` when the endpoint needs no key. A user-provided Gemini-compatible endpoint, if it exposes the supported OpenAI-compatible shape, is configured the same way with `type: openai_compat`; this does not make every Gemini endpoint compatible. Native `type: gemini` is not runtime-supported.

### LiteLLM

LiteLLM has its own provider type:

```yaml
providers:
  litellm:
    type: litellm
    base_url: <litellm-base-url>
    api_key_env: LITELLM_API_KEY

models:
  profiles:
    default:
      default_model: litellm/<model-id>
```

## OpenCode

OpenCode provides two runtime-supported provider types:

```yaml
providers:
  go:
    type: opencode_go
    api_key_env: OPENCODE_API_KEY
  zen:
    type: opencode_zen
    api_key_env: OPENCODE_API_KEY

models:
  profiles:
    default:
      default_model: go/<model-id>
```

For OpenCode Zen, set the default to `zen/<model-id>` instead. Both are raw references; use the provider key chosen in your config. OpenCode's own TUI provides `/connect`; that command belongs to OpenCode, not Steiner. Use the key or environment variable required by the OpenCode service.

## Codex OAuth

Codex uses your OpenAI account through OAuth and does not use an API key.

Log in and check the login status:

```bash
steiner login codex
steiner login codex status
```

Configure the provider with `type: codex` and select the model with its raw reference. Replace `<codex-model-id>` with a model ID available to your account:

```yaml
providers:
  codex:
    type: codex

models:
  profiles:
    default:
      default_model: codex/<codex-model-id>
```

Do not add `api_key` or `api_key_env` for Codex. See [Optional features](optional-features.md#codex-oauth) for the short feature pointer.

## Optional aliases

Use an alias when it gives a model a shorter or stable name, or when it stores settings that a raw reference cannot carry. This is a valid reasoning configuration example:

```yaml
providers:
  openai:
    type: openai
    api_key_env: OPENAI_API_KEY

models:
  definitions:
    careful:
      provider: openai
      id: <model-id>
      advanced:
        reasoning:
          effort: high
          supported_efforts: [low, medium, high]
  profiles:
    default:
      default_model: careful
```

Here `careful` is useful because it persists the reasoning setting. Without that customization, use `openai/<model-id>` directly. Aliases are optional, not required for ordinary provider setup.

## Profiles and selection precedence

The `default` profile must define `default_model`. Named profiles are overlays on that profile. A local-only profile should clear inherited role assignments explicitly when needed:

```yaml
models:
  profiles:
    default:
      default_model: ollama/qwen2.5-coder:14b
      sub_agents:
        code: openrouter/openai/gpt-4o
    local-only:
      default_model: ollama/qwen2.5-coder:14b
      sub_agents:
        code: ""
```

Select a profile at startup with `--profile local-only`.

For the active orchestrator at startup, selection precedence is:

1. The selected profile's `default_model`.
2. `STEINER_MODEL`, when set to a valid raw reference or configured alias.
3. `--model`, which overrides `STEINER_MODEL`.

The environment and CLI overrides affect the active orchestrator only. Advisor, sub-agent, oneshot, and workflow-handoff assignments still come from the selected profile. In interactive mode, `/model <provider>/<model-id>` changes the active orchestrator for the session. `/profile <name>` changes the profile used for future role assignments and fallback; it does not replace the current active orchestrator selection.

## Verify a setup

From the project containing `.steiner/config.yaml`:

```bash
steiner config
steiner --model ollama/qwen2.5-coder:14b --exec "summarize this repository in one sentence"
```

Start `steiner` for interactive mode, then use `/model ollama/qwen2.5-coder:14b` to switch the active orchestrator. Use `steiner model inspect <alias>` only for a configured alias.
