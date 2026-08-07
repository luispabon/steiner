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

## Approval Model

Each server in `mcp.servers` can be gated independently via the `approval`
field. Three modes are supported:

- `ask` — the default. Every tool call prompts the user for approval. The
  prompt offers an "Allowed for session" button, which records a session-scoped
  grant so the same tool does not prompt again for the rest of the run.
- `allow` — tools run without prompting in build mode. In plan mode the server
  is downgraded to `ask`, so plan-mode tool calls still prompt.
- `deny` — the server's tools are not registered at all. The server still
  connects, but exposes no tools to the model.

An unset `approval` defaults to `ask`.

### Annotation trust

`trust_annotations` (default `false`) opts a server into trusting the
annotations it advertises on its tools. When `true`, a tool with
`readOnlyHint: true` skips approval entirely. `destructiveHint` and
`openWorldHint` tools still prompt unless both hints are explicitly `false`;
per the MCP spec they default to `true` when unset, so a server that omits
annotations is not silently trusted.

### Session grants

Grants made via "Allowed for session" are held in memory only, keyed by
`server + tool`, and never persisted. They last for the current session.

### Deferred

- Persistent grants across sessions — tracked in #411.
- Per-tool approval overrides, as a finer-grained alternative to whole-server
  modes.

## Lifecycle

### Startup: parallel connect, TUI paints first

`Manager.Connect` starts every enabled server's connection attempt on its own goroutine and
returns immediately, so startup latency is bounded by the slowest server rather than the sum.
Each attempt is bounded by the server's `connect_timeout` (default `15s`, applied when the
field is absent or `0`), covering the handshake and the initial tool-list call together; a
failed server is marked `failed` and never blocks the others.

Non-interactive runs block on `WaitInit` before building the tool registry, so the frozen
registry contains every server that connected. The interactive TUI does the reverse: it
paints while servers connect, and the first agent turn waits on `WaitInit` and registers
the connected tools before any model call runs. Until then, servers show as `connecting`
in the sidebar and the `/mcp` overlay.

### Reconnect on transport error

A classified transport error — a dead stdio process or a lost HTTP session — triggers an
optimistic reconnect: the failed call is **not replayed**. It returns a
"disconnected, verify state" error naming the server, and a background worker runs
sequential fresh-connect attempts, each bounded by the server's `connect_timeout`. Calls
that arrive while a reconnect is in flight block on its outcome, bounded by the call's own
timeout; if the reconnect succeeded the call proceeds on the fresh session, if it ended
`unavailable` the call fails with that outcome. A successful reconnect swaps a new session
under the same handle — tool definitions keep working and the consecutive-failure counter
resets. After 3 consecutive failed attempts the server is marked `unavailable` and no
further reconnect workers spawn. Reconnects never re-list tools: the tool set is frozen at
connect time.

A dead server's tools stay registered for the whole run. This is a deliberate divergence
from crush (D15): dropping them mid-session would mutate the prompt prefix and invalidate
the prompt cache, so the model keeps seeing the tools and any call to the dead server
blocks during reconnect, then fails with the verify-state error above.

### Output bounds and call timeouts (D19)

MCP tool output is bounded by `limits.tool_output_max_bytes` (default `65536`): flattened
text is truncated with a `<truncated output shown=… total=…>` marker reporting the
pre-truncation total. Each call is also bounded by `limits.tool_timeout_default` (default
`30s`), which now applies to MCP tools; a per-tool override goes in
`limits.tool_timeouts`, keyed by the tool's full registered name — `mcp__<server>__<tool>`,
or the hashed form when the name needed sanitisation or exceeded the length limit (see
[docs/configuration.md](configuration.md)). A timed-out call is not a transport error, so
it never triggers reconnect.

### State events and TUI refresh

Every status transition — `connecting` → `connected`/`failed` at startup, and
`connected` → `reconnecting` → `connected`/`unavailable` later — is recorded in the
manager's live snapshot and broadcast to the interactive TUI, which refreshes the sidebar
indicator and re-reads the snapshot when the `/mcp` overlay opens. Transition warnings are
deduplicated per failure generation: a server that recovers to `connected` and fails again
warns once more. Initial-connect transitions that land before the first-turn registration
are collapsed into one snapshot when the session arms, so the TUI never observes a
half-connected server set.

## TUI surfaces

### `/mcp` overlay

The `/mcp` command opens a scrollable overlay listing every server declared in `mcp.servers`,
regardless of whether it connected. Each entry shows:

- a status bullet, coloured (not shaped) by state — green for `connected`, red for `failed` and
  `unavailable`, muted for `disabled` and the in-flight states `connecting`/`reconnecting`
- the server name, state label, and transport
- for a connected server: its tool count and tool names, or an explicit "no tools advertised" note
  if it exposes none (a real misconfiguration signal, not treated as a blank)
- for a failed or unavailable server: the error text, indented beneath its status line

When MCP is disabled in config, the overlay says so and still lists every declared server as
disabled — useful for a user who forgot to flip the flag. When no servers are configured, it says
so rather than rendering an empty frame.

Scrolling is plain line-offset (arrow keys/`j`/`k`, page up/down, home/end); there is no
selection model or filtering. `esc` or `enter` closes it.

In interactive mode the overlay reflects live state: it re-reads the current snapshot each time it
opens, so it shows the post-connect and post-reconnect picture rather than the startup one.

### Startup failure warnings

A failed connection is surfaced as warning lines in the transcript — not just in a log line. One
line per failed server naming it and its error, plus a single aggregate line if any failed:

```
⚠ MCP server "my_server" failed to connect: exec: "my-server-binary": executable file not found in $PATH
⚠ MCP startup incomplete (failed: my_server)
```

Nothing is emitted when MCP is off, when nothing is configured, or when every server connected.

The interactive TUI starts with async connect, so servers may still be `connecting` when it
paints. Each server that later resolves to a failure (`failed` or `unavailable`) surfaces one
warning line at the transition, deduplicated per failure generation: a server that recovers to
`connected` and fails again warns once more.

### Transcript attribution

MCP tool calls in the transcript are rendered as `server → tool` with a dedicated tag and border
style, distinguishing them at a glance from built-in tool calls (which continue to render with
their existing name and styling, unchanged). Server identity always comes from the tool's
structured provenance, never from parsing the tool's registry name — this holds even for server or
tool names containing underscores.

### Sidebar indicator

A compact `MCP <connected>/<total>` key/value row appears in the sidebar's status block, alongside
`SANDBOX` and `SKILL`, counting connected servers over configured-and-enabled ones (disabled
servers are excluded from both numbers). The value is coloured in the error style when any server
has failed, and the normal row style otherwise. It is hidden entirely — not shown as "MCP: off" —
when MCP is disabled or nothing is configured, to avoid spending sidebar width on users who will
never enable MCP. When sandbox status, skill, and MCP are all absent, the status block does not
render at all. In interactive mode the row updates as status events arrive, so it tracks
connect and reconnect without a restart.

## Remote HTTP servers

Remote MCP servers are supported over HTTP; see `mcp.servers.<name>.transport` and `mcp.servers.<name>.url` in [docs/configuration.md](configuration.md). The same approval model (`ask`, `allow`, `deny`) applies to remote servers as to stdio servers.

### OAuth for remote servers

OAuth 2.1 + PKCE for remote MCP servers was considered and deliberately deferred. The rationale:

- **Test coverage unavailable.** No test peer was available to develop and validate the integration.
- **SDK support exists.** The MCP SDK already provides `StreamableClientTransport.OAuthHandler` for pluggable OAuth handlers; a user who needs it can open an issue with their use case.
- **Alternative shipped instead.** Static headers (via `mcp.servers.<name>.headers`) ship in this round to unblock deployments that need bearer tokens or other immutable credentials.

A future issue (#XYZ, once opened by a user) should implement OAuth by configuring `OAuthHandler` on the client transport.

## Deferred

The following are intentionally not part of this work:

- **`steiner mcp debug`** — a standalone connectivity probe outside the TUI. Not yet built.
- **Live config reload / server reconciliation** — re-reading config changes and enabling
  or disabling servers mid-session. Server state is a live snapshot updated by status
  events, but the server set itself is frozen at startup; tracked in #411.
- **Background health-check watchdog** — proactive liveness probes. Reconnect today is
  driven by transport errors on calls; a watchdog would detect a silently dead server
  without waiting for the next call.
- **Retry-when-provably-safe** — replaying a call that died on the wire when the server
  advertises `idempotentHint`. Today a failed call is never replayed; it fails with the
  "verify state" error described under [Lifecycle](#lifecycle).
- **The approval prompt overlay** for MCP tool calls requiring approval — a separate surface from
  the four described here. Tracked in #407.
- **Surfacing `serverInfo.title` / `instructions`** — no competitor tool surfaces these either, and
  the client currently only captures the negotiated protocol version, not the full initialize
  result. A possible future addition if a concrete need arises.
