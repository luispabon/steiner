# MCP servers

Steiner can connect external tools via the Model Context Protocol (MCP). An MCP server — a local subprocess over stdio or a remote endpoint over HTTP — advertises tools that the agent can call alongside the built-in ones. This page is the complete reference: configuration, tool naming, approval, sandboxing, lifecycle, output bounds, TUI surfaces, security posture, and what was deliberately left out.

## What MCP is

The Model Context Protocol is an open standard for connecting AI clients to external tools and data sources. Steiner implements an MCP *client*: it launches or dials MCP servers, lists the tools they advertise, and lets the agent call them like any other tool. Each call carries the server's own argument schema, and results come back as server-rendered content. Two transports are supported: `stdio` (a local subprocess, e.g. `npx` servers) and `http` (a remote Streamable HTTP endpoint).

## Configuration

MCP is configured under the `mcp` block; the full field reference lives in the [mcp config block](configuration.md#mcp-block) in docs/configuration.md. The global switch `mcp.enabled` defaults to `true` (flipped in D22; `internal/config/defaults.go:104-106`), so the client is on unless you disable it. Each server, however, defaults to `enabled: false` (`internal/config/config.go:114-127`) — a server only connects when you explicitly enable it.

Two worked examples — one stdio server launched with `npx`, one remote HTTP server:

```yaml
mcp:
  # enabled is optional: it defaults to true (D22)
  servers:
    context-mode:            # stdio server
      enabled: true
      command: npx
      args: ["-y", "context-mode"]
      env:
        npm_config_cache: /tmp/npm-cache
    microsoft-learn:          # remote HTTP server
      enabled: true
      transport: http
      url: https://learn.microsoft.com/api/mcp
```

Both examples set server-level `enabled: true` because the per-server default is `false`: omitting it leaves the server disabled. Per-server defaults applied when fields are omitted: `transport` falls back to `stdio`, `approval` to `ask`, and `connect_timeout` to `15s` (`internal/config/load.go:90-106`).

The `env` map under each server is passed to the server process verbatim and bypasses the sandbox env allowlist entirely, since it is declared config rather than inherited host state (`internal/mcp/command.go:52-54`). That makes it the correct place for a server's own credentials or API keys, even when `sandbox.enabled: true`.

## Naming

MCP tools register under `mcp__<server>__<tool>` (`internal/mcp/naming.go:9-10, 18-30`). When both segments are clean and the composed name is at most 64 characters, that name is used as-is. Otherwise:

- each segment is sanitised: every rune outside `[A-Za-z0-9_-]` becomes `_` (`internal/mcp/naming.go:20-22, 54-64`);
- the composed name is truncated to fit the 64-char budget;
- an 8-hex-character SHA-256 suffix over the *original, unsanitised* `server\x00tool` inputs is appended, yielding `mcp__<server>__<tool>__<hash>` (`internal/mcp/naming.go:32-34, 36-49`).

Why the hash exists: sanitisation and truncation are lossy. Distinct `(server, tool)` pairs can collapse to the same sanitised or truncated name — `tool.name` and `tool-name` both sanitise to `tool_name`, and two long names can truncate identically — which would collide in the registry. The hash of the original inputs restores uniqueness while staying a pure function of `(server, tool)`: it never depends on which other servers connected, so names stay stable when a server fails to connect (`internal/mcp/naming.go:15-17`).

`steiner tools` prints the resulting registry names.

## Approval model

Each server is gated independently by `approval` (default `ask`, `internal/config/load.go:97-100`):

- `ask` — every tool call prompts the user for approval.
- `allow` — tools run without prompting in build mode. In plan mode the server is downgraded to `ask`, so plan-mode calls still prompt (`internal/mcp/tooldef.go:74-76`).
- `deny` — the server still connects but registers no tools (`internal/mcp/manager.go:221-226`); the handler defends anyway, so a stale definition can never bypass the mode (`internal/mcp/tooldef.go:61-69`).

The approval prompt shows a preview of the call arguments: sorted `key: value` lines, long strings truncated to 60 runes, nested structures collapsed (`internal/mcp/tooldef.go:216-229, 231-253`). Choosing "Allowed for session" records an in-memory grant keyed `server\x00tool` — never the registry name — so the same tool does not prompt again for the rest of the run (`internal/interactive/wiring.go:52-61, 74-76, 92-96`). Grants are session-scoped and never persisted.

`trust_annotations` (default `false`) opts a server into trusting the annotations it advertises. With it on, a tool whose `readOnlyHint` is `true` skips approval entirely (`internal/mcp/tooldef.go:84-87`). `destructiveHint` and `openWorldHint` default to `true` per the MCP spec when unset, so a tool only skips approval when both are explicitly `false` (`internal/mcp/tooldef.go:88-94`) — a server that omits annotations is never silently trusted.

A nil approver fails closed: an MCP tool that needs approval cannot be called without an approver and returns an `approval_denied` error (`internal/mcp/tooldef.go:96-106`). The approver is installed when the interactive session is built (`cmd/steiner/interactive_session.go:49-56`); non-interactive runs (exec, oneshot) install no MCP approver, so `ask`-mode tools fail closed there — only `allow`-mode build calls and trusted read-only annotation tools can run.

Plan mode is read live from the closure, not baked into the registry: `UpdatePlanMode` (`internal/mcp/manager.go:442-447`) and mid-session mode switches (`cmd/steiner/interactive_session.go:57-62`) apply without rebuilding tool definitions.

## Sandboxing

When sandboxing is enabled, locally launched stdio MCP server processes are sandbox-wrapped with the project pinned read-only (`cmd/steiner/runtime_build.go:325-327`; `internal/sandbox/sandbox.go:55-119`). This applies only to stdio servers that steiner itself launches — a remote HTTP server executes on its operator's machine and is not affected by the sandbox (`internal/mcp/client.go:120-128`; `internal/mcp/transport_http.go:19-36`).

What the wrap does: unshare all namespaces except the network (`--share-net`, `internal/sandbox/mounts.go:15`), bind the whole root filesystem read-only (`internal/sandbox/mounts.go:18`), and bind the project read-only with only `.steiner/plans` writable (`internal/sandbox/mounts.go:32-34`); the sandbox home, user cache, and `/tmp` stay writable (`internal/sandbox/mounts.go:25-29, 39-47`). Platform limits: the wrap is bubblewrap/Linux-only — when bwrap is unavailable or the sandbox is disabled, the command runs unwrapped (`internal/sandbox/sandbox.go:56-76`).

## Tool filtering

`allowed_tools` and `blocked_tools` filter the advertised tool list at connect time, allowlist first then denylist (`internal/mcp/manager.go:267-308`). Entries are MCP-native advertised names, not registry names. A nil `allowed_tools` means no allowlist restriction; an explicitly configured empty list (`allowed_tools: []`) filters every tool (`internal/mcp/manager.go:283-288`). References to tools a server never advertised are non-fatal warnings, emitted once each in sorted order (`internal/mcp/manager.go:297-306`).

Sub-agent exposure: `sub_agents` lists the agent types that may call this server's tools. Registered tools are projected per agent type into the child delegation allowlist (`cmd/steiner/mcp_exposure.go:21-40`). The nil vs explicit-empty semantics differ from `allowed_tools`: for `sub_agents`, missing and `[]` are the same — both grant no MCP tools to any child (closed by default).

## Lifecycle

- **Parallel connect.** Every enabled server connects on its own goroutine, so startup latency is bounded by the slowest server rather than the sum (`internal/mcp/manager.go:146-166`). Each attempt is bounded by the server's `connect_timeout` (default `15s`, `internal/config/load.go:85-88`), covering the handshake and the initial tool-list call together (`internal/mcp/client.go:135-139, 169`). A failed server is marked `failed` and never blocks the others (`internal/mcp/manager.go:196-200`).
- **Non-interactive (exec, oneshot).** The run blocks on `WaitInit` before building the registry, so the frozen registry contains every server that connected (`cmd/steiner/runtime_build.go:345-352`; `internal/mcp/manager.go:366-382`).
- **Interactive.** Servers connect asynchronously so the TUI paints immediately; the first agent turn runs `mcpInitOnce` — `WaitInit` → register the connected tool defs → arm the state producer — before any model call (`cmd/steiner/interactive_session.go:415-440`). Until then servers show `connecting`.
- **Reconnect, no replay.** A classified transport error — a dead stdio process or a lost HTTP session (`internal/mcp/client.go:386-391`) — kicks an optimistic reconnect. The failed call is *not* replayed: it returns a "disconnected, verify state" error naming the server (`internal/mcp/client.go:200-222`). A background worker runs sequential fresh-connect attempts, each bounded by `connect_timeout`, and never re-lists tools (`internal/mcp/client.go:273-350`). Success swaps a new session under the same handle and resets the consecutive-failure counter; tool definitions keep working (`internal/mcp/client.go:293-297`). After 3 consecutive failed attempts the server is marked `unavailable` and no further reconnect workers spawn (`internal/mcp/client.go:28, 313-316`). Calls that arrive while a reconnect is in flight block on its outcome, bounded by their own timeout (`internal/mcp/client.go:230-250`).
- **Dead tools stay registered.** The tool set is frozen at connect time and never mutated mid-session, so the prompt prefix — and the prompt cache — stays stable (`internal/mcp/manager.go:388-395`). A call to a dead server blocks during reconnect, then fails with the verify-state error above.
- **Status transitions.** `connecting` → `connected`/`failed` at startup; `connected` → `reconnecting` → `connected`/`unavailable` later (`internal/mcp/state.go:12-25`).
- **Startup warnings.** A failed connection surfaces as a warning naming the server plus an aggregate "MCP startup incomplete" line; nothing is emitted when MCP is off, nothing is configured, or every server connected (`internal/tui/mcp_warnings.go:14-45`). Warnings are deduplicated per failure generation: a server that recovers to `connected` and fails again warns once more (`internal/tui/mcp_warnings.go:48-72`).

## Output bounds

MCP tool output is bounded by `limits.tool_output_max_bytes` (default `65536`, `internal/config/defaults.go:68`): flattened text is truncated with a `<truncated output shown=… total=…>` marker reporting the pre-truncation total (`internal/mcp/tooldef.go:189-196`). Only text content is rendered; other content types are named but not decoded (`internal/mcp/tooldef.go:173-183`).

Each call is bounded by `limits.tool_timeout_default` (default `30s`), with per-tool overrides in `limits.tool_timeouts` keyed by the tool's full registered name — `mcp__<server>__<tool>`, or the hashed form (`internal/mcp/tooldef.go:157-166`). The global MCP tool timeouts and output limits live in the [limits block](configuration.md#limits-block) of docs/configuration.md, shared with the built-in tools. A timed-out call is a context error, not a transport error, so it never triggers reconnect (`internal/mcp/client.go:386-391`).

## TUI surfaces

- **`/mcp` overlay.** A scrollable list of every server declared in `mcp.servers`, regardless of whether it connected (`internal/tui/mcp_overlay.go:55-66`). Each entry shows a status bullet coloured by state (`●` green for `connected`, red for `failed`/`unavailable`, muted otherwise), the server name, state label, and transport; beneath it, the error text for failed/unavailable servers or one line per advertised tool with its access outcome — `name (registered)`, `name (filtered)`, or `name (denied)` — plus an explicit "no tools advertised" note when a connected server advertises none (`internal/tui/mcp_overlay.go:70-111`). Filtered and denied entries are dimmed and display-only. When MCP is disabled in config the overlay says so and lists every declared server as disabled; with no servers configured it says so rather than rendering an empty frame. Scrolling is plain line-offset; `esc` or `enter` closes it.
- **Sidebar.** A compact `MCP <connected>/<total>` row in the status block, error-styled when any server has failed and hidden entirely — not shown as "MCP: off" — when MCP is off or nothing is configured (`internal/tui/sidebar_mcp.go:5-12`; `internal/tui/sidebar_sections.go:109-133`). Disabled servers are excluded from both numbers (`internal/tui/model.go:254-268`). In interactive mode the row updates as status events arrive.
- **Transcript attribution.** MCP tool calls render as `server → tool` with a dedicated tag and border style, distinct from built-in tool calls. Server identity always comes from structured provenance, never from parsing the registry name (`internal/tui/content_tool_mcp.go:5-21`; `internal/tui/content_tool.go:113-115, 159-160`).
- **Startup and transition warnings.** The interactive TUI emits one warning line per failed server plus an aggregate line, at the transition, deduplicated per failure generation (`internal/tui/mcp_warnings.go:14-72`). Initial-connect transitions that land before the first-turn registration are collapsed into one snapshot when the session arms, so the TUI never observes a half-connected server set (`cmd/steiner/interactive_session.go:212-258`).

## Security posture

- **Prompt-injection surface.** MCP tool descriptions are third-party-authored text that enters the system prompt, and tool results enter the conversation (`internal/mcp/tooldef.go:36-44`). A malicious or compromised server can attempt prompt injection through either channel; treat servers as untrusted code.
- **PathPolicy cannot gate MCP calls.** Path validation is keyed to built-in tool names only; an MCP tool's arguments are opaque to steiner and pass through unvalidated (`internal/tool/policy.go:205-216`). Per-server approval is the boundary, not path policy.
- **Sandbox limits.** A sandboxed stdio server retains network access (`--share-net`) and whole-filesystem *read* access — including home credentials — because the root is bound read-only rather than hidden (`internal/sandbox/mounts.go:15, 18`). It loses write access except for the narrow writable binds listed under [Sandboxing](#sandboxing). This is not a network or exfiltration barrier, and remote HTTP servers are not sandboxed at all.
- **Annotations untrusted by default.** Per the MCP spec, `destructiveHint` and `openWorldHint` default to `true` when unset, so a server that omits annotations is not silently trusted; `trust_annotations` is the explicit assertion that opts into trusting them (`internal/mcp/tooldef.go:84-94`).

## Considered and deferred

| Considered | Status and reasoning |
|---|---|
| OAuth for HTTP transport | Deferred. No test peer was available to develop and validate the integration, and the SDK's `StreamableClientTransport.OAuthHandler` is left unset (`internal/mcp/transport_http.go:14-18`). Static headers ship instead for bearer tokens and other immutable credentials. Pending a user with a concrete use case. |
| MCP resources | Not implemented. The client captures the tool list only (`internal/mcp/client.go:169`); no resource API. |
| MCP prompts | Not implemented. No prompts API. |
| Sampling and elicitation | Not implemented. No sampling handler is registered on the client (`internal/mcp/client.go:133`). |
| Roots/completions | Not implemented. |
| Persistent approval grants | Deferred. Session grants are held in memory only (`internal/interactive/wiring.go:33-40`). See follow-up issues #438 and #439. |
| Per-tool approval overrides | Deferred. Approval is per-server only; the `/mcp` overlay entries are display-only. |
| `steiner mcp debug` | Not built. No CLI subcommand exists. |
| `steiner mcp add` CLI | Not built. No CLI subcommand exists. |
| Live config reload | Deferred. The server set is frozen at connect time; only per-server statuses are live (`internal/mcp/manager.go:123-144`). |
| Retry-when-provably-safe | Deferred. A failed call is never replayed, even when the server advertises `idempotentHint`; it fails with the verify-state error (`internal/mcp/client.go:200-222`). |
| Live enable/disable from `/mcp` overlay | Deferred. The overlay is display-only — no selection model, no actions (`internal/tui/mcp_overlay.go`). |
| Per-file `mcp/.yaml` | Not implemented. MCP servers are configured in the single `mcp` config block. |
| Legacy HTTP+SSE | Not implemented. Only Streamable HTTP is supported (`internal/mcp/transport_http.go:28`). |
| Not shipping MCP at all | Superseded: MCP ships with this release. |

## Troubleshooting

- **Server shows `failed`.** Read the error text under its entry in `/mcp`. Common causes: executable not on `$PATH` (stdio), wrong transport, unreachable URL (http), or denied credentials in `headers`.
- **Server shows `unavailable`.** Three consecutive reconnect attempts failed (`internal/mcp/client.go:28`); the server process died or the endpoint went away. Fix the server and restart steiner.
- **A tool is missing from the model.** Check `/mcp`: `filtered` means `allowed_tools`/`blocked_tools` excluded it; `denied` means the server's approval mode is `deny`; `no tools advertised` means the server advertised none.
- **Calls fail with "cannot be called without an approver".** No approver is installed — this is the expected fail-closed behaviour in non-interactive runs, where only `allow`-mode build calls and trusted read-only annotation tools can run (`internal/mcp/tooldef.go:96-106`).
- **Calls fail with "disconnected, verify state".** The transport broke mid-call; the call may or may not have been applied. Verify state before retrying (`internal/mcp/client.go:221`).
- **The registered name does not match your config.** Check `steiner tools` for the hashed form when the server or tool name needed sanitisation or exceeded the length limit (`internal/mcp/naming.go:18-50`).
- **Everything prompts in plan mode.** `allow`-mode servers are downgraded to `ask` in plan mode by design (`internal/mcp/tooldef.go:74-76`).
- **A timed-out call does not reconnect.** Deadlines are context errors, not transport errors, so they never kick a reconnect (`internal/mcp/client.go:386-391`).

## Verification note

MCP behaviour is covered by hermetic, CI-safe integration tests under `internal/mcp/` — loopback HTTP and local subprocesses only, no live services, credentials, or outbound network:

- `TestMCPTransportParity` drives the common user-visible contract through the Manager and tool-call path for **both** transports (stdio and HTTP): connect+initialize, tool discovery, a successful tool call, an MCP `isError` result surfacing as a non-OK envelope, and initial connection failure (`internal/mcp/transport_parity_integration_test.go`).
- The shared loopback HTTP test server (`newTestMCPServer` in `internal/mcp/http_test_support_test.go`) backs the HTTP parity, integration, and lifecycle tests.
- `TestStdio` runs against an independent, hand-written, SDK-free stdio fixture (`internal/mcp/testdata/fixtureserver/main.go`) — the independent JSON-RPC peer; the HTTP test server is SDK-backed and not a protocol-conformance peer.
- Transport-specific lifecycle coverage is retained: stdio reaping/reconnect and HTTP dropped-session/reconnect (`TestLifecycle*`), plus HTTP headers, close, and failure states (`TestHTTPIntegration`).

These tests do not cover live validation against third-party MCP servers — real `npx` packages or remote endpoints. That remains manual work tracked in #438 (manual e2e script) and #439 (e2e integration testing).
