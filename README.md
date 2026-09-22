# steiner

![Steiner screenshot](docs/screenshot.png)

A minimal, local-first Go coding agent with bounded context and sandboxed execution.

## Quick start

[Install a release binary](docs/user/installation.md), then see [Getting started](docs/user/getting-started.md) for the shortest local-model path. [Provider and model setup](docs/user/provider-and-model-setup.md) covers provider connections and model references. [CLI reference](docs/user/cli.md) covers one-shot requests, commands, and interactive controls.

## Features

- Local and cloud providers with one configuration shape. See [Configuration](docs/user/configuration.md).
- Bounded context through delegation, budgets, and compaction. See [Context management](docs/user/context-management.md).
- Structured tools, optional language-server and MCP integrations. See [Tools](docs/user/tools.md), [LSP](docs/user/lsp.md), and [MCP](docs/user/mcp.md).
- Plan/build execution modes and resumable autonomous runs. See [Execution modes](docs/user/execution-modes.md) and [Oneshot](docs/user/oneshot.md).
- Sandboxed commands by default. See [Sandboxing](docs/user/sandboxing.md).
- Optional advisor, model discovery, image input, notifications, and cache statistics. See [Optional features](docs/user/optional-features.md).

## Safety

Review configuration before real work. Commands are sandboxed by default on Linux, but sandboxing is not a confidentiality boundary. `--unsafe` disables the sandbox. See [Sandboxing](docs/user/sandboxing.md). The first time steiner runs in a project directory, it asks you to trust that project, showing every way its `.steiner/config.yaml` would change your global config. See [Project trust](docs/user/configuration.md#project-trust).

## Documentation map

- [Documentation home](docs/index.md)
- [User documentation](docs/index.md#user-documentation)
- [Internals](docs/internals/index.md)
- [Research](docs/index.md#research)

## Contributing

Run `make check` before submitting changes. Contributor rules and architecture constraints are in [AGENTS.md](AGENTS.md).
