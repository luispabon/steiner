# MCP server status in the TUI

Steiner has a Model Context Protocol (MCP) stdio client behind a feature flag. MCP is **off by
default**; enabling it and announcing it as a supported feature is tracked separately. This
document covers the TUI surfaces that make MCP server state visible and debuggable once the flag
is on.

## Configuration

MCP is configured under the `mcp` block:

```yaml
mcp:
  enabled: true
  servers:
    my_server:
      enabled: true
      transport: stdio
      command: /path/to/server
      args: []
      env: {}
```

`stdio` is currently the only supported transport. See [docs/configuration.md](configuration.md)
for the full config reference.

The `env` map under each server (`mcp.servers.<name>.env`) is passed to the server process verbatim and bypasses the host sandbox allowlist entirely, since it is declared config rather than inherited host state. This makes it the correct place to put a server's own credentials or API keys (e.g., a token the MCP server itself needs), even when `sandbox.enabled: true`.

## TUI surfaces

### `/mcp` overlay

The `/mcp` command opens a scrollable overlay listing every server declared in `mcp.servers`,
regardless of whether it connected. Each entry shows:

- a status bullet, coloured (not shaped) by state — green for `connected`, red for `failed`, muted
  for `disabled`
- the server name, state label, and transport
- for a connected server: its tool count and tool names, or an explicit "no tools advertised" note
  if it exposes none (a real misconfiguration signal, not treated as a blank)
- for a failed server: the error text, indented beneath its status line

When MCP is disabled in config, the overlay says so and still lists every declared server as
disabled — useful for a user who forgot to flip the flag. When no servers are configured, it says
so rather than rendering an empty frame.

Scrolling is plain line-offset (arrow keys/`j`/`k`, page up/down, home/end); there is no
selection model or filtering. `esc` or `enter` closes it.

### Startup failure warnings

Because the MCP handshake happens before the TUI exists, a failed connection is captured in a
snapshot and surfaced as warning lines in the transcript at startup — not just in a log line. One
line per failed server naming it and its error, plus a single aggregate line if any failed:

```
⚠ MCP server "my_server" failed to connect: exec: "my-server-binary": executable file not found in $PATH
⚠ MCP startup incomplete (failed: my_server)
```

Nothing is emitted when MCP is off, when nothing is configured, or when every server connected.

### Transcript attribution

MCP tool calls in the transcript are rendered as `server → tool` with a dedicated tag and border
style, distinguishing them at a glance from built-in tool calls (which continue to render with
their existing name and styling, unchanged). Server identity always comes from the tool's
structured provenance, never from parsing the tool's registry name — this holds even for server or
tool names containing underscores.

### Sidebar indicator

A compact `MCP <connected>/<total>` row appears in the sidebar, counting connected servers over
configured-and-enabled ones (disabled servers are excluded from both numbers). The row is coloured
in the error style when any server has failed, and the normal row style otherwise. It is hidden
entirely — not shown as "MCP: off" — when MCP is disabled or nothing is configured, to avoid
spending sidebar width on users who will never enable MCP.

## Deferred

The following are intentionally not part of this work:

- **`steiner mcp debug`** — a standalone connectivity probe outside the TUI. Not yet built.
- **Live enable/disable toggling** from the `/mcp` overlay. Server state is a startup snapshot;
  toggling servers mid-session is future work, tracked in #411.
- **Reconnect / restart actions** and any push-based state updates after startup — today's surfaces
  are all frozen at the initial connection snapshot. Tracked in #409.
- **The approval prompt overlay** for MCP tool calls requiring approval — a separate surface from
  the four described here. Tracked in #407.
- **Surfacing `serverInfo.title` / `instructions`** — no competitor tool surfaces these either, and
  the client currently only captures the negotiated protocol version, not the full initialize
  result. A possible future addition if a concrete need arises.
