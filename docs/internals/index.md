# Internals

These pages are the current implementation reference: internal mechanics and design details. User-facing behavior belongs in the [documentation home](../index.md).

Core runtime (start with Architecture):

- [Architecture](architecture.md)
- [Context management](context-management.md)
- [Prompt caching](prompt-caching.md)
- [Sub-agent delegation](sub-agent-delegation.md)
- [Execution modes](execution-modes.md)
- [Sandboxing](sandboxing.md)
- [Configuration](configuration.md)
- [Diagnostics](diagnostics.md)

Features:

- [Oneshot](oneshot.md)
- [Advisor](advisor.md)
- [MCP](mcp.md)
- [LSP](lsp.md)
- [Claude subscription](claude-subscription.md)
- [Usage dashboard](usage-dashboard.md)
- [Desktop notifications](desktop-notifications.md)

See [canon drift checks](../maintenance/canon-drift-checks.md) for the maintainer checks that keep orchestration prose aligned with source and prompt consumers.
