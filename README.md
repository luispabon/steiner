# steiner

![Steiner screenshot](docs/screenshot.png)

Steiner is a lean, opinionated coding agent built with bounded context, sandboxed commands and structured tools. It helps you inspect and change a project while keeping execution and approvals under your control.

## Capabilities

- Connect [local and cloud providers and models](docs/user/provider-and-model-setup.md), including:
  - Codex (subscription and API)
  - Claude Code (subscription and API)
  - Opencode Go
- Delegate work to [sub-agents in worktrees](docs/user/sub-agent-delegation.md).
- Use reusable [skills](docs/user/skills.md).
- Switch between [plan and build modes](docs/user/execution-modes.md).
- Run autonomous tasks with [oneshot](docs/user/oneshot.md).
- Control [sandboxing and approvals](docs/user/sandboxing.md).
- Inspect [usage and cache statistics](docs/user/usage.md).
- Extend structured tools with [MCP](docs/user/mcp.md) and [LSP](docs/user/lsp.md).

## Quick start

Install with Go, or see [installation](docs/user/installation.md) for release binaries:

```bash
go install github.com/luispabon/steiner/cmd/steiner@latest
```

Configure a provider and model using the [quickstart](docs/user/quickstart.md), then start in your project:

```bash
steiner
```

## Safety

On Linux, commands are sandboxed by default when `bwrap` is available. The sandbox is not a confidentiality boundary, and `--unsafe` disables it. Review the [sandbox and approval rules](docs/user/sandboxing.md) and [project trust](docs/user/configuration.md#project-trust) before working on a project.

## Documentation

- [Quickstart](docs/user/quickstart.md)
- [Manual index](docs/index.md)
- [Internals](docs/internals/index.md)
