# steiner

![Steiner screenshot](docs/screenshot.png)

A minimal, local-first Go coding agent with bounded context and sandboxed execution.

## Quick start

Install Go `1.25+`, start a local model such as Ollama, then run:

```bash
ollama run qwen2.5-coder:14b
go run ./cmd/steiner
```

See [Getting started](docs/user/getting-started.md) for setup and a minimal configuration. [CLI reference](docs/user/cli.md) covers one-shot requests, commands, and interactive controls.

## Features

- Local and cloud providers with one configuration shape. See [Configuration](docs/user/configuration.md).
- Bounded context through delegation, budgets, and compaction. See [Context management](docs/user/context-management.md).
- Structured tools, optional language-server and MCP integrations. See [Tools](docs/user/tools.md), [LSP](docs/user/lsp.md), and [MCP](docs/user/mcp.md).
- Plan/build execution modes and resumable autonomous runs. See [Execution modes](docs/user/execution-modes.md) and [Oneshot](docs/user/oneshot.md).
- Sandboxed commands by default. See [Sandboxing](docs/user/sandboxing.md).
- Optional advisor, model discovery, image input, notifications, and cache statistics. See [Optional features](docs/user/optional-features.md).

## Safety

Review configuration before real work. Commands are sandboxed by default on Linux, but sandboxing is not a confidentiality boundary. `--unsafe` disables the sandbox. See [Sandboxing](docs/user/sandboxing.md).

## Documentation map

- [Documentation home](docs/index.md)
- [User documentation](docs/index.md#user-documentation)
- [Internals](docs/internals/index.md)
- [Research](docs/index.md#research)

## Contributing

Run `make check` before submitting changes. Contributor rules and architecture constraints are in [AGENTS.md](AGENTS.md).
