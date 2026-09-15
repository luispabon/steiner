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

## Provider and model setup

For provider-specific configuration, reusable model aliases, profiles, and runtime model selection, see [Provider and model setup](provider-and-model-setup.md). See [Configuration](configuration.md) for all fields and providers, and [CLI reference](cli.md) for commands and flags.
