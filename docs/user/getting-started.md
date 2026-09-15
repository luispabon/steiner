# Getting started

## Prerequisites

You need Go `1.25+`. For local models, install [Ollama](https://ollama.com) or [LM Studio](https://lmstudio.ai).

## Run Steiner

Start a local model, then run a one-shot request:

```bash
ollama run qwen2.5-coder:14b
go run ./cmd/steiner --exec "summarize this repository in one sentence"
```

Start interactive mode instead:

```bash
go run ./cmd/steiner
```

Inspect resolved configuration before doing real work:

```bash
go run ./cmd/steiner config
```

To build a local binary:

```bash
make build-binaries
./bin/steiner
```

## Minimal local configuration

The default local endpoint is Ollama's OpenAI-compatible API. A minimal project configuration is:

```yaml
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  definitions:
    local:
      provider: local
      id: qwen2.5-coder:14b
  profiles:
    default:
      default_model: local
```

See [Configuration](configuration.md) for all fields and providers. See [CLI reference](cli.md) for commands and flags.
