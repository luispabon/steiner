# Getting started

This guide assumes `steiner` is installed. If it is not, follow [Installation](installation.md), which also lists prerequisites (Linux sandboxing needs `bubblewrap`, and `git` is needed for delegation and oneshot runs).

## Before you start

Steiner reads configuration from a project file at `.steiner/config.yaml` and a user file at `~/.config/steiner/config.yaml`. Neither is created for you; create `.steiner/` and the file yourself. See [Configuration](configuration.md#file-locations-and-loading-order) for the precedence rules.

Pick one of the paths below. Both end at a working `steiner` session.

## Path 1: a local model

Start a local model with [Ollama](https://ollama.com) or [LM Studio](https://lmstudio.ai). For Ollama, for example:

```bash
ollama run qwen2.5-coder:14b
```

Configure the local provider and select its raw model reference in `.steiner/config.yaml`:

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

Run a one-shot request:

```bash
steiner --exec "summarize this repository in one sentence"
```

Start interactive mode instead:

```bash
steiner
```

## Path 2: OpenCode Go or Codex

Codex authenticates through your OpenAI account, so log in first:

```bash
steiner login codex
```

OpenCode Go is a gateway from [OpenCode](https://opencode.ai/docs), a separate coding agent; you obtain that key from OpenCode, not Steiner. Both providers fit in one config, and API keys stay out of the file:

```bash
export OPENCODE_API_KEY='your-opencode-key'
```

```yaml
providers:
  opencode-go:
    type: opencode_go
    api_key_env: OPENCODE_API_KEY
  codex:
    type: codex

models:
  profiles:
    default:
      default_model: opencode-go/<model-id>
```

Switch the active model inside a session with `/model`, or per request with `--model` or `STEINER_MODEL`, for example `--model codex/<model-id>`. Full setup, including obtaining the OpenCode key, OpenCode Zen, and finding model IDs, is in [Provider and model setup](provider-and-model-setup.md).

## Inspect and verify

Inspect resolved configuration before doing real work:

```bash
steiner config
```

In the interactive TUI, `/` opens the command and skill list, and `?` toggles help. `steiner --help` lists all flags.

For provider-specific connections, raw model references, optional aliases, profiles, and runtime selection, see [Provider and model setup](provider-and-model-setup.md). See [Configuration](configuration.md) for every field. Contributors and unsupported platforms can use the [source-build fallback](installation.md#build-from-source).
