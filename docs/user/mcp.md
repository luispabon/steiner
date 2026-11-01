# MCP servers

Steiner is a Model Context Protocol (MCP) client. It launches local servers over `stdio` or connects to remote servers over Streamable HTTP, lists their tools, and offers them to the model next to the built-in tools.

## Setup

MCP is on by default (`mcp.enabled: true`), but each server is off until you set `enabled: true` on it. Field reference: [`mcp` block](configuration.md#mcp-block).

```yaml
mcp:
  servers:
    context-mode:
      enabled: true
      command: npx
      args: ["-y", "context-mode"]
      env:
        npm_config_cache: /tmp/npm-cache
    microsoft-learn:
      enabled: true
      transport: http
      url: https://learn.microsoft.com/api/mcp
```

The transport defaults to `stdio` and approval to `ask`. The `env` map is passed to a stdio server as given, without the sandbox environment filter, so use it for the server's API keys. HTTP servers take `headers` instead.

Tools register as `mcp__<server>__<tool>`. Use `steiner tools` or `/mcp` to inspect available tools.

## Choosing which tools appear

- `allowed_tools` keeps only the listed tools; `blocked_tools` then removes tools. Both use the tool names the server advertises, not the `mcp__` names. Leaving `allowed_tools` unset allows all tools; an empty list allows none.
- `sub_agents` lists the sub-agent types that may use the server's tools. Unset or empty means sub-agents get none.
- Tools whose input schema is larger than 64 KiB are skipped with a warning.

## Approval

Each server has an `approval` mode:

| Mode | Behaviour |
|------|-----------|
| `ask` | Calls prompt for approval unless trusted annotations permit them. |
| `allow` | Calls run without prompting. In plan mode `allow` behaves as `ask`. |
| `deny` | The server connects but none of its tools are offered. |

The prompt shows the server, the tool, and a preview of the arguments. **Allowed for session** covers that server's tool until Steiner exits; grants are never saved. See [Sandboxing](sandboxing.md) for how approvals work across tools.

With `trust_annotations: true`, an `ask` server's tool skips the prompt when the server marks it `readOnlyHint: true`, or marks it as both non-destructive (`destructiveHint: false`) and closed-world (`openWorldHint: false`). Missing hints count as destructive and open-world, so unannotated tools still prompt. Annotations come from the server, so only trust them for servers you trust.

### Non-interactive runs

In `--exec`, calls that need approval prompt through the terminal or stdin. [Oneshot](oneshot.md) denies MCP approval requests. See [Sandboxing](sandboxing.md) for approval input.

## Sandboxing

When the sandbox is active, stdio servers run inside it with network access, a read-only project, and the read-only host filesystem. Remote HTTP servers run elsewhere and are not sandboxed. See [Sandboxing](sandboxing.md).

## Startup and failures

Enabled servers connect in parallel, each bounded by `connect_timeout`. A server that fails does not block the others. Non-interactive runs wait for every server before starting; the interactive TUI opens straight away and waits before the first model call.

If a stdio server exits or an HTTP session is lost, Steiner reconnects on the next call. It never replays the call that failed. A server that keeps failing to reconnect becomes unavailable until you restart Steiner.

Each call uses `limits.tool_timeout_default`, or a `limits.tool_timeouts` entry keyed by the full `mcp__<server>__<tool>` name. Text output is capped by `limits.tool_output_max_bytes`; non-text content is named but not passed through. See the [`limits` block](configuration.md#limits-block).

## `/mcp`

`/mcp` in the TUI lists declared servers and their tools.

## Security

- Tool descriptions and results are third-party content and can contain prompt injection. Treat servers as untrusted code.
- Steiner does not inspect MCP arguments for paths. The approval prompt is the boundary.
- Sandboxed stdio servers can still read host files, including credentials, and reach the network.
- HTTP redirects to a different scheme, host, or port are refused, so configured headers are never sent to another origin.

## Troubleshooting

- **Server failed**: check the command, transport, URL, and credentials in `/mcp`.
- **Tool missing**: check `allowed_tools`, `blocked_tools`, and `approval` in the server config.
- **Call fails with `approval_denied`**: check that approval input is available and the call was approved.
- **Prompts in plan mode for an `allow` server**: expected.
- **Call failed after a disconnect**: the server may have applied it before the connection dropped. Check the state before retrying.
