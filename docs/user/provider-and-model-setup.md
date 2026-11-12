# Provider and model setup

A **provider** is a service that runs models for you, together with the details Steiner needs to reach it: what kind of service it is, where it lives, and how to sign in. OpenAI's API, an OpenRouter account, and the Ollama server on your laptop are all providers. A **model** is one specific LLM that a provider serves, such as `gpt-4o` or `qwen2.5-coder:14b`. You set up providers once, then choose models from them by name.

For example, in `.steiner/config.yaml`:

```yaml
# Provider: how to reach the service
providers:
  openai:
    type: openai
    api_key_env: OPENAI_API_KEY

# Model: which model the default profile uses
models:
  profiles:
    default:
      default_model: openai/<model-id>
```

Set `OPENAI_API_KEY` in your environment and replace `<model-id>` with a model available to you. For all fields, see the [`providers` block](configuration.md#providers-block) and the [`models` block](configuration.md#models-block).

## Providers

### What a provider is

The key under `providers` is a name you choose and use in model references. It is not the provider type. The `type` selects the kind of service, its API protocol, authentication, and defaults. Connection details can include `base_url`, credentials, and optional `headers` or `timeout`.

You can declare several providers of the same type, such as two `openai_compat` gateways, or `openai-work` and `openai-personal` with different keys.

### Provider types

| Type                                          | Connects to                               | `base_url`                              | Authentication    |
| --------------------------------------------- | ----------------------------------------- | --------------------------------------- | ----------------- |
| **Hosted APIs**                               |                                           |                                         |                   |
| `openai`                                      | OpenAI API                                | Default `https://api.openai.com/v1`     | Required API key  |
| `anthropic`                                   | Anthropic API                             | Default `https://api.anthropic.com/v1`  | Required API key  |
| `openrouter`                                  | OpenRouter gateway                        | Default `https://openrouter.ai/api/v1`  | Required API key  |
| `opencode_go`                                 | OpenCode Go gateway                       | Default `https://opencode.ai/zen/go/v1` | Required API key  |
| `opencode_zen`                                | OpenCode Zen gateway                      | Default `https://opencode.ai/zen/v1`    | Required API key  |
| **Subscriptions**                             |                                           |                                         |                   |
| [`codex`](#codex-chatgpt-sign-in)             | ChatGPT backend                           | Optional, ignored for ChatGPT sign-in   | OAuth login       |
| [`claude_subscription`](#claude-subscription) | Signed-in local `claude` CLI              | Rejected                                | Claude Code login |
| **Self-hosted and gateways**                  |                                           |                                         |                   |
| `ollama`                                      | Ollama's OpenAI-compatible API            | Required                                | Optional API key  |
| `lmstudio`                                    | LM Studio's local server                  | Required                                | Optional API key  |
| `litellm`                                     | LiteLLM gateway                           | Required                                | Optional API key  |
| `openai_compat`                               | OpenAI chat-completions compatible server | Required                                | Optional API key  |

`gemini` passes validation but cannot run, so don't use it.

- `openai`, `openrouter`, `ollama`, `lmstudio`, `litellm`, and `openai_compat` use the OpenAI chat-completions API by default. `anthropic` uses the Anthropic Messages API by default.
- `opencode_go` and `opencode_zen` select the OpenAI chat-completions or Anthropic Messages API per model.
- `codex` uses the OpenAI Responses API over HTTP or an experimental WebSocket.

Model transport settings can override the default for non-subscription types; see the [`models` block](configuration.md#models-block).

### When you need base_url

Hosted types `openai`, `anthropic`, `openrouter`, `opencode_go`, and `opencode_zen` know their addresses, so `base_url` is optional. A non-empty `base_url` overrides the default, for example to send traffic through a proxy. You can use `headers` for extra headers the proxy needs. For Anthropic, Steiner adds `/messages` for requests and `/models` for discovery.

For `ollama`, `lmstudio`, `litellm`, and `openai_compat`, set `base_url` to where you run the service. Validation fails without it. Use the API root, usually ending in `/v1`; Steiner adds paths such as `/chat/completions`.

Codex ignores `base_url` for ChatGPT sign-in and sends requests to the ChatGPT backend. `claude_subscription` rejects `base_url`, `api_key`, `api_key_env`, `headers`, and `timeout` because it runs the local CLI.

### API keys

`api_key_env` names an environment variable holding the key; `api_key` holds the key itself and expands `${VAR}`. `api_key` wins if both are set. Prefer `api_key_env` or `${VAR}` to keep secrets out of files. Choose any variable name and export it before running Steiner. `export` lasts for the current shell only, so use your normal secret management to persist it.

Validation requires `api_key` or `api_key_env` for the hosted types, but checks only that the variable name is set, not that it has a value. If the variable is unset, requests go out without a key and the provider rejects them. Check with `steiner models refresh`. Keys are optional for self-hosted and gateway types. Most types send a set key as `Authorization: Bearer <key>`; Anthropic uses `x-api-key`.

### Setup recipes

Each recipe configures a provider in `.steiner/config.yaml`. Set a model separately, as in the introduction or [Profiles](#profiles).

#### OpenAI

```bash
export OPENAI_API_KEY='your-key'
```

```yaml
providers:
  openai:
    type: openai
    api_key_env: OPENAI_API_KEY
```

Reference its models as `openai/<model-id>`.

#### Anthropic

```bash
export ANTHROPIC_API_KEY='your-key'
```

```yaml
providers:
  anthropic:
    type: anthropic
    api_key_env: ANTHROPIC_API_KEY
```

Reference its models as `anthropic/<model-id>`.

#### OpenRouter

```bash
export OPENROUTER_API_KEY='your-key'
```

```yaml
providers:
  openrouter:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY
```

Reference its models as `openrouter/<provider-model-id>`. OpenRouter model IDs usually contain a slash, for example `openrouter/openai/gpt-4o`.

#### OpenCode Go and Zen

Sign in at https://opencode.ai, generate an API key, and export it:

```bash
export OPENCODE_API_KEY='your-opencode-key'
```

```yaml
providers:
  opencode-go:
    type: opencode_go
    api_key_env: OPENCODE_API_KEY
```

OpenCode Zen uses the same shape with `type: opencode_zen`, for example a provider key `opencode-zen`. Model IDs come from OpenCode: run `steiner models refresh`, then pick one with `/model`. Reference models as `opencode-go/<model-id>` or `opencode-zen/<model-id>`.

#### Ollama

Start a model in Ollama:

```bash
ollama run qwen2.5-coder:14b
```

```yaml
providers:
  ollama:
    type: ollama
    base_url: http://localhost:11434/v1
```

Replace the model ID with one installed in your Ollama. Reference its models as `ollama/<model-id>`.

#### LM Studio

Start LM Studio's local server, load a model, and use the model ID the server reports:

```yaml
providers:
  lmstudio:
    type: lmstudio
    base_url: http://127.0.0.1:1234/v1
```

Reference its models as `lmstudio/<model-id>`.

#### LiteLLM

```yaml
providers:
  litellm:
    type: litellm
    base_url: <litellm-base-url>
    api_key_env: LITELLM_API_KEY
```

Omit `api_key_env` when the gateway needs no key. Reference its models as `litellm/<model-id>`.

#### OpenAI-compatible endpoints

Use `openai_compat` for any server that exposes the OpenAI-compatible chat-completions API. Omit `api_key_env` when the endpoint needs no key.

```yaml
providers:
  gateway:
    type: openai_compat
    base_url: <gateway-base-url>
    api_key_env: GATEWAY_API_KEY
```

A Gemini-compatible endpoint works this way only if it exposes that API shape. Reference its models as `gateway/<model-id>`.

### Subscription providers

These types use an account sign-in and count usage against that subscription. Codex accepts an optional `base_url` but ignores it for a ChatGPT sign-in. Claude subscription rejects both `base_url` and API keys.

#### Codex (ChatGPT sign-in)

Codex uses OAuth to sign in with your ChatGPT account. For a ChatGPT sign-in, requests go to the ChatGPT backend over the OpenAI Responses API; `base_url` is ignored. If the stored sign-in carries an OpenAI API key (API mode), requests use the configured `base_url` or the default `https://api.openai.com/v1` instead. Codex always uses the stored sign-in, never the configured `api_key` or `api_key_env`; without a sign-in, Steiner stops and tells you to run `steiner login codex`.

Sign in, then check the stored login:

```bash
steiner login codex
steiner login codex status
```

`steiner login codex` opens your browser and saves the token to `steiner/codex_auth.json` in your user config directory (`~/.config/steiner/codex_auth.json` on Linux by default); the command prints the path. It waits up to `--timeout` (default `120s`) for browser sign-in. Use `--debug-url` to print the authorisation URL before opening the browser, which helps if the browser does not open. The status command prints token expiry and `valid` or `needs refresh`, or tells you to sign in. Steiner refreshes the token automatically when it is close to expiry; a login save takes the same token-file lock as that refresh, so signing in while a refresh is running does not lose the login. If saving a refreshed token fails (for example the config directory is read-only), Steiner keeps using the refreshed token and retries the save a few times over the next minutes. If refresh fails or there is no login, run `steiner login codex` again.

Configure the provider:

```yaml
providers:
  codex:
    type: codex
```

Optional settings:

- `codex.transport` defaults to `http`. `websocket` is experimental and does not fall back to HTTP on failure. Each WebSocket dial reads the stored sign-in again, so a redial after a release does not reuse an expired token; after signing in to a different account or auth mode the redial fails instead of sending the new credentials with the old account. The `http` transport keeps the credential resolved when its provider was built.
- `codex.min_request_interval` sets the minimum gap between requests and defaults to `0s` (disabled).
- `compaction_fallback_after` defaults to `30m`.

See the [`providers` block](configuration.md#providers-block) for details.

Available model IDs depend on your account. Run `steiner models refresh`, then use `/model` to see them. Reference models as `codex/<codex-model-id>`.

See the [usage dashboard](usage.md#usage-dashboard) for Codex quota information.

#### Claude subscription

Steiner runs your signed-in `claude` CLI as a subprocess, using your Claude Code login rather than an API key. Install Claude Code with `claude` version `2.1.294` or later on `PATH`, and sign in with `claude auth login --claudeai`. Steiner accepts a first-party claude.ai login.

Configure the provider:

```yaml
providers:
  claude:
    type: claude_subscription
```

Configuration rejects `base_url`, `api_key`, `api_key_env`, `headers`, and `timeout`. `compaction_fallback_after` defaults to `60m`. Model discovery asks the signed-in CLI for available models, with a built-in fallback list if the CLI is missing, signed out, or returns no usable models within 20 seconds. Reference models as `claude/<model-id>` using a full model ID reported by the CLI.

See [Claude subscription](claude-subscription.md) for the extra usage safety gate, privacy, and limitations.

### Check a provider works

From the project that contains `.steiner/config.yaml`, use `steiner config` to check the configuration. With discovery on, `steiner models refresh` prints `ok` or `failed` per provider, providing a quick connection check. Use `steiner model inspect <reference>` to inspect a model resolution. Run a smoke test with, for example:

```bash
steiner --model ollama/qwen2.5-coder:14b --exec "summarize this repository in one sentence"
```

Start `steiner` and use `/model` to confirm the models you expect are listed.

## Models

### Model references

- `<provider>/<model-id>` is the normal reference. `<provider>` is the key you chose under `providers`, not a display name or the provider type.
- Model IDs may contain slashes, for example `openrouter/openai/gpt-4o`. Steiner matches the longest configured provider key that prefixes the reference.
- An exact alias from `models.definitions` wins over prefix parsing.

Raw references work everywhere a model is chosen: profile roles, `--model`, `STEINER_MODEL`, `/model`, and `steiner model inspect`. A raw reference carries only the provider and model ID. Use an [alias](#aliases) when a model needs stored settings such as reasoning effort, request parameters, retry policy, prompts, transport, or limits.

### Aliases

Steiner does model discovery - it will query the provider API to provide it with a list of available models. You do not strictly need to add any model configuration at all on the config file to use and reference models.

An alias under `models.definitions` gives a model a short or stable name, or stores settings a raw reference cannot carry, like a thinking level, or a compaction threshold.


```yaml
models:
  definitions:
    careful:
      provider: openai
      id: my-model
      advanced:
        reasoning:
          effort: high
          supported_efforts: [low, medium, high]
```

Without the reasoning setting, `openai/my-model` would do the same job. For every alias field, see the [`models` block](configuration.md#models-block).

### Profiles

A profile under `models.profiles` assigns models to roles: `default_model`, `advisor`, `compaction_fallback`, `sub_agents`, `oneshot`, and `workflow_handoff`. Each role accepts an alias or a raw reference.

- Every declared profile must set `default_model`. A profile without one fails to load.
- Profiles are independent. A named profile does not inherit from `default`; a role it omits falls back to its own `default_model`.
- Without a `default` profile, no roles are assigned unless you pick a profile, and the orchestrator model must come from `--model`, `STEINER_MODEL`, or `/model`.
- Selecting an undeclared profile, including `default`, is an error.
- `default_model` is mandatory and is the fallback model for everything undeclared on that profile.

For a complete configuration with several profiles, see the [full example](#full-example-local-codex-claude-subscription-and-opencode-go). Its `cheaper` profile sets no `sub_agents`, so every sub-agent uses its `default_model`, `deepseek-flash`. Select a profile at startup with `--profile <name>`.

### Choosing the active model

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

### Model discovery

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

### Model metadata

Steiner needs each model's context window, output limit, and reasoning support. For context window and output limit, it uses alias settings first, then discovered provider data, then [models.dev](https://models.dev) metadata, then conservative defaults. For context window only, it also tries a live Ollama probe after discovered provider data. Reasoning efforts follow the same order without the probe. See the [`models` block](configuration.md#models-block) for the exact precedence.

The models.dev data is cached for seven days. When the cache is stale, Steiner refreshes it as needed and keeps using the old copy if the refresh fails.

Generic types (`openai_compat`, `ollama`, `litellm`) front arbitrary backends, so Steiner cannot always tell which model sits behind an ID. When it cannot, it uses conservative context and output limits. If you know the real limits, set them on an [alias](#aliases) with `advanced.limits`.

## Cache commands and files

| Command                             | What it does                                                                                                                                  |
| ----------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `steiner models refresh`            | Refresh discovered models for every configured provider. Prints `ok` or `failed` per provider; exits non-zero only when every provider fails. |
| `steiner models status`             | Print `fresh`, `stale`, or `missing` for each provider's discovery cache, without refreshing.                                                 |
| `steiner model-metadata status`     | Show the models.dev cache path, size, age, freshness, and model count.                                                                        |
| `steiner model-metadata refresh`    | Download fresh models.dev metadata.                                                                                                           |
| `steiner model-metadata clear`      | Delete the models.dev cache files.                                                                                                            |
| `steiner cache refresh`             | Run `model-metadata refresh` and `models refresh` together.                                                                                   |
| `steiner model inspect <reference>` | Show how an alias or raw reference resolves: provider, backend ID, transport, limits, and where each fact came from.                          |

When discovery is off, `steiner models refresh` and `steiner models status` report that discovery is disabled. See the [CLI reference](cli.md) for flags.

### Cache and state files

These files live under `$XDG_CACHE_HOME` or `$XDG_STATE_HOME` when set, otherwise under the defaults shown. They can be deleted safely. A refresh rebuilds the cache directories; deleting `model-popularity.json` resets picker ranking history, which cannot be recovered by a refresh.

| Path                                           | Contents                                     |
| ---------------------------------------------- | -------------------------------------------- |
| `~/.cache/steiner/provider-models/`            | One discovery cache file per provider.       |
| `~/.cache/steiner/model-metadata/`             | models.dev metadata.                         |
| `~/.local/state/steiner/model-popularity.json` | Model switch counts used for picker ranking. |


## Full example: Local, Codex, Claude subscription and OpenCode Go

```yaml
providers:
  local:
    type: openai_compat
    base_url: http://localhost:9105/v1
    timeout: 240s

  opencode-go:
    type: opencode_go
    api_key_env: OPENCODE_API_KEY

  codex:
    type: codex

  claude:
    type: claude_subscription

models:
  definitions:
    qwen-35b:
      provider: local
      id: qwen3.6-35b-a3b
      prompt_suffix: "<|think_off|>"

      advanced:
        limits:
          context_window: 65536
          max_output_tokens: 16384

    haiku:
      provider: claude
      id: claude-haiku-5-5
      advanced:
        reasoning:
          effort: high
        limits:
          # Over 100k, haiku costs double, but it needs room to avoid frequent compactions
          compaction_threshold_tokens: 262144

    sonnet:
      provider: claude
      id: claude-sonnet-5-5
      advanced:
        reasoning:
          effort: medium

    opus:
      provider: claude
      id: claude-opus-5-5
      advanced:
        reasoning:
          effort: high

    deepseek-flash:
      provider: opencode-go
      id: deepseek-v4.1-flash
      advanced:
        reasoning:
          effort: high

    luna:
      provider: codex
      id: gpt-6-luna
      advanced:
        reasoning:
          effort: high

    sol:
      provider: codex
      id: gpt-6.1-sol
      advanced:
        reasoning:
          effort: high

    terra:
      provider: codex
      id: gpt-6.1-terra
      advanced:
        reasoning:
          effort: high

    astra:
      provider: codex
      id: gpt-6-astra
      advanced:
        reasoning:
          effort: high

  profiles:
    default:
      default_model: terra
      advisor: opus

      compaction_fallback: deepseek-flash

      sub_agents:
        code: deepseek-flash
        evaluate: sonnet
        explore: deepseek-flash
        research: luna
        review: haiku
        sanity_check: haiku
        vision: luna
        documentor: terra

      oneshot:
        plan: terra
        implement: terra
        review: terra

      workflow_handoff:
        implement: terra
        review: terra

    cheaper:
      default_model: deepseek-flash
      advisor: sol

    smarter:
      default_model: opus
      advisor: astra

      compaction_fallback: sonnet

      sub_agents:
        code: sonnet
        evaluate: sol
        explore: deepseek-flash

        # A raw provider/model-id reference with default settings; no alias needed
        research: opencode-go/minimax-m3

        review: terra
        sanity_check: haiku
        vision: luna
        documentor: terra

    local:
      default_model: qwen-35b
```
