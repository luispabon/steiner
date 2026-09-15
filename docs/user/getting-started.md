# Getting started

This guide assumes `steiner` is installed. If it is not, follow [Installation](installation.md).

## First run with a local model

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

Inspect resolved configuration before doing real work:

```bash
steiner config
```

For provider-specific connections, raw model references, optional aliases, profiles, and runtime selection, see [Provider and model setup](provider-and-model-setup.md). See [Configuration](configuration.md) for every field. Contributors and unsupported platforms can use the [source-build fallback](installation.md#build-from-source).
