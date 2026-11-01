# Provider and model setup

A provider is a connection you declare under `providers`. A model reference picks a model through one of those providers. This page covers setup recipes, model references, aliases, profiles, model selection, and model discovery. For every provider field, type, and default, see the [`providers` block](configuration.md#providers-block) and the [`models` block](configuration.md#models-block).

## Model references

- `<provider>/<model-id>` is the normal reference. `<provider>` is the key you chose under `providers`, not a display name or the provider type.
- Model IDs may contain slashes, for example `openrouter/openai/gpt-4o`. Steiner matches the longest configured provider key that prefixes the reference.
- An exact alias from `models.definitions` wins over prefix parsing.

Raw references work everywhere a model is chosen: profile roles, `--model`, `STEINER_MODEL`, `/model`, and `steiner model inspect`. A raw reference carries only the provider and model ID. Use an [alias](#aliases) when a model needs stored settings such as reasoning effort, request parameters, retry policy, prompts, transport, or limits.

## Local providers

### Ollama

Start a model in Ollama:

```bash
ollama run qwen2.5-coder:14b
```

Configure the provider in `.steiner/config.yaml`. Ollama has no default base URL, so set it explicitly:

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

Steiner uses Ollama's OpenAI-compatible API at the `/v1` base URL. Replace the model ID with one installed in your Ollama.

### LM Studio

Start LM Studio's local server, load a model, and use the model ID the server reports:

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

Keep API keys out of config files. `api_key_env` names an environment variable that holds the key; export it before running Steiner. The variable name is your choice. `export` lasts for the current shell only, so use your normal secret management to persist it.

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

The `anthropic` type has no default base URL. Set `base_url` to the API root that includes `/v1`; Steiner appends `/messages` for requests and `/models` for discovery. The key is sent as the `x-api-key` header.

```bash
export ANTHROPIC_API_KEY='your-key'
```

```yaml
providers:
  anthropic:
    type: anthropic
    base_url: https://api.anthropic.com/v1
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

OpenRouter model IDs usually contain a slash, so a complete reference looks like `openrouter/openai/gpt-4o`.

### OpenCode Go and Zen

Sign in at https://opencode.ai, generate an API key, and export it:

```bash
export OPENCODE_API_KEY='your-opencode-key'
```

```yaml
providers:
  opencode-go:
    type: opencode_go
    api_key_env: OPENCODE_API_KEY

models:
  profiles:
    default:
      default_model: opencode-go/<model-id>
```

OpenCode Zen uses the same shape with `type: opencode_zen`, for example a provider key `opencode-zen` and references like `opencode-zen/<model-id>`. Both types have built-in default base URLs. Model IDs come from OpenCode: run `steiner models refresh`, then pick one with `/model`.

### Codex (ChatGPT sign-in)

The `codex` type uses your OpenAI account through OAuth instead of an API key. Sign in, then check the stored login:

```bash
steiner login codex
steiner login codex status
```

`steiner login codex` opens your browser and saves the token to `steiner/codex_auth.json` in your user config directory (`~/.config/steiner/codex_auth.json` on Linux by default); the command prints the path. It waits up to `--timeout` (default `120s`) for the browser sign-in. `--debug-url` prints the authorisation URL before opening the browser, which helps when the browser does not open. `steiner login codex status` prints the token expiry and `valid` or `needs refresh`, or tells you to run `steiner login codex`. Steiner refreshes the token automatically when it is close to expiry; if a refresh fails, run `steiner login codex` again.

```yaml
providers:
  codex:
    type: codex

models:
  profiles:
    default:
      default_model: codex/<codex-model-id>
```

Codex ignores `api_key` and `api_key_env`; it always uses the stored sign-in. Without one, Steiner stops and tells you to run `steiner login codex`. The available model IDs depend on your account: run `steiner models refresh`, then use `/model` to see them. For the HTTP or WebSocket transport choice, see the [`providers` block](configuration.md#providers-block).

### Claude subscription

The `claude_subscription` type runs your signed-in `claude` CLI. See [Claude subscription](claude-subscription.md) for requirements, setup, the extra usage safety gate, privacy, and limitations.

## Gateways

### OpenAI-compatible endpoints

Use `openai_compat` for any server that exposes the OpenAI-compatible chat-completions API. `base_url` is required; omit `api_key_env` when the endpoint needs no key.

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

A Gemini-compatible endpoint works this way only if it exposes that API shape. Config validation accepts `type: gemini`, but Steiner cannot run it.

### LiteLLM

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

Generic types (`openai_compat`, `ollama`, `litellm`) front arbitrary backends, so Steiner cannot always tell which model sits behind an ID. When it cannot, it uses conservative context and output limits. If you know the real limits, set them on an [alias](#aliases) with `advanced.limits`.

## Aliases

An alias under `models.definitions` gives a model a short or stable name, or stores settings a raw reference cannot carry. Aliases are optional:

```yaml
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

Without the reasoning setting, `openai/<model-id>` would do the same job. For every alias field, see the [`models` block](configuration.md#models-block).

## Profiles

A profile under `models.profiles` assigns models to roles: `default_model`, `advisor`, `compaction_fallback`, `sub_agents`, `oneshot`, and `workflow_handoff`. Each role accepts an alias or a raw reference.

- Every declared profile must set `default_model`. A profile without one fails to load.
- Profiles are independent. A named profile does not inherit from `default`; a role it omits falls back to its own `default_model`.
- Without a `default` profile, no roles are assigned unless you pick a profile, and the orchestrator model must come from `--model`, `STEINER_MODEL`, or `/model`.
- Selecting an undeclared profile, including `default`, is an error.

```yaml
models:
  profiles:
    default:
      default_model: ollama/qwen2.5-coder:14b
      sub_agents:
        code: openrouter/openai/gpt-4o
    local-only:
      default_model: ollama/qwen2.5-coder:14b
```

`local-only` has no `sub_agents.code` entry, so its `code` sub-agents use `ollama/qwen2.5-coder:14b`, not the OpenRouter model from `default`. Select it at startup with `--profile local-only`.

## Selection precedence

At startup, the active orchestrator model is chosen in this order, with later entries overriding earlier ones:

1. The selected profile's `default_model`.
2. `STEINER_MODEL`, when it is a valid raw reference or configured alias.
3. `--model`.

`STEINER_MODEL` and `--model` change only the active orchestrator. Advisor, sub-agent, compaction fallback, oneshot, and workflow-handoff assignments still come from the selected profile.

During an interactive session:

- `/model <reference>` switches the active orchestrator.
- `/model` with no argument, or `alt+m`, opens the model picker. The picker can also set reasoning effort for models that support it.
- `/profile <name>` changes the profile used for future role assignments. It does not change the active orchestrator.
- `/profile` with no argument, or `alt+p`, opens the profile picker.

Switching the model or its reasoning effort in a conversation that already has messages asks for confirmation first, because the switch invalidates the prompt cache. If a run is in progress, the switch applies to the next run. See [Interactive mode](interactive.md) for the full key and slash command reference.

## Model discovery

Steiner asks configured providers which models they offer and adds them to the model picker. Discovery is on by default. These provider types support it: `openai`, `openai_compat`, `litellm`, `opencode_go`, `opencode_zen`, `ollama`, `lmstudio`, `openrouter`, `anthropic`, `codex`, and `claude_subscription`. Support depends on the provider type, not on the key you gave the provider.

- At startup, cached results load first. Providers with a missing or stale cache refresh in the background, and new entries appear in the picker as each provider answers. Steiner does not refresh again during the session.
- A cached list stays fresh for seven days. Changing a provider's type or base URL invalidates its cache.
- Discovered context windows, output limits, and reasoning efforts also feed model metadata. Settings on an alias still win.
- The picker shows `<provider>/<alias>` for configured aliases and `<provider>/<model-id>` for discovered models. An alias hides the matching raw entry.
- The picker ranks models by how often you have switched to them, then aliases before raw entries, then by name. Switch counts are keyed by provider key and backend model ID, so renaming an alias keeps its count.
- Discovery never writes model definitions back to your config, and API keys are never written to the cache.

To turn discovery off:

```yaml
models:
  discovery_enabled: false
```

With discovery off, the picker shows configured aliases only, and discovered data is not used for model metadata. Raw references and switch counts still work.

## Model metadata

Steiner needs each model's context window, output limit, and reasoning support. It takes these from your alias settings first, then discovered provider data, then a live probe for Ollama, then [models.dev](https://models.dev) metadata, then conservative defaults. See the [`models` block](configuration.md#models-block) for the exact precedence.

The models.dev data is cached for seven days. When the cache is stale, Steiner refreshes it as needed and keeps using the old copy if the refresh fails.

## Cache commands

| Command | What it does |
| --- | --- |
| `steiner models refresh` | Refresh discovered models for every configured provider. Prints `ok` or `failed` per provider; exits non-zero only when every provider fails. |
| `steiner models status` | Print `fresh`, `stale`, or `missing` for each provider's discovery cache, without refreshing. |
| `steiner model-metadata status` | Show the models.dev cache path, size, age, freshness, and model count. |
| `steiner model-metadata refresh` | Download fresh models.dev metadata. |
| `steiner model-metadata clear` | Delete the models.dev cache files. |
| `steiner cache refresh` | Run `model-metadata refresh` and `models refresh` together. |
| `steiner model inspect <reference>` | Show how an alias or raw reference resolves: provider, backend ID, transport, limits, and where each fact came from. |

When discovery is off, `steiner models refresh` and `steiner models status` report that discovery is disabled. See the [CLI reference](cli.md) for flags.

### Cache and state files

These files live under `$XDG_CACHE_HOME` or `$XDG_STATE_HOME` when set, otherwise under the defaults shown. All can be deleted safely; Steiner rebuilds them.

| Path | Contents |
| --- | --- |
| `~/.cache/steiner/provider-models/` | One discovery cache file per provider. |
| `~/.cache/steiner/model-metadata/` | models.dev metadata. |
| `~/.local/state/steiner/model-popularity.json` | Model switch counts used for picker ranking. |

## Verify a setup

From the project that contains `.steiner/config.yaml`:

```bash
steiner config
steiner --model ollama/qwen2.5-coder:14b --exec "summarize this repository in one sentence"
```

Then start `steiner` and use `/model` to confirm the models you expect are listed.
