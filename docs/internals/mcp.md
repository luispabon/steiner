# MCP servers internals

User-facing documentation: [MCP servers](../user/mcp.md).

## Naming, filtering, and approval

`ToolName` (`internal/mcp/naming.go`) replaces each rune outside `[A-Za-z0-9_-]` with `_`. If a segment changed or the name exceeds 64 characters, each segment is truncated within a 47-character budget and an 8-hex SHA-256 suffix over the original `server\x00tool` is appended. A name depends only on its own server and tool.

`filterTools` (`internal/mcp/connect.go`) applies `allowed_tools` (nil means no restriction; an empty list filters everything), then `blocked_tools`. It warns once per configured name the server did not advertise. Tools whose input schema exceeds `maxInputSchemaBytes` (64 KiB) or cannot be marshalled are skipped with a warning (`internal/mcp/manager.go`).

`approval` is `ask` (default), `allow`, or `deny`. `deny` registers no tools, and `mcpHandler` (`internal/mcp/tooldef.go`) fails closed too. `allow` becomes `ask` while the live plan-mode closure reports plan mode. Handler order: deny, allow outside plan mode, `trust_annotations`, nil approver (`approval_denied`), prompt. With `trust_annotations`, `ReadOnlyHint` skips the prompt, as does `DestructiveHint` and `OpenWorldHint` both explicitly false; nil hints count as true. Previews sort top-level keys, truncate strings to 60 runes, and collapse nested values to `{...}`. `always_allow` grants are keyed `server\x00tool` and held in memory by the interactive wiring (`internal/interactive/wiring.go`).

Each call is bounded by the per-tool timeout keyed `mcp__server__tool` in `ToolTimeouts`, else `ToolTimeoutDefault` (`callMCPTool`). Text content is flattened; image, audio, and resource content become placeholders. Output over `ToolOutputMaxBytes` ends with a `<truncated ...>` marker. An `IsError` result returns `mcp_tool_error`. Timeouts are not transport errors and never trigger reconnect.

## Lifecycle

The manager connects enabled servers concurrently, bounding each attempt (connection, initial tool list, and definition creation) by `connect_timeout`, or `DefaultConnectTimeout` when unset (`internal/mcp/client.go`). Non-interactive runtime construction waits on `WaitInit` before freezing the registry. Interactive sessions connect asynchronously; `mcpInitOnce` (`cmd/steiner/interactive_session.go`) waits and registers connected definitions on the first turn. Until then, `mcpStateProducer` (`cmd/steiner/interactive_mcp_state.go`) publishes states-only snapshots.

A classified transport error starts one sequential fresh-connect worker per server. It does not re-list tools, and the failed call is not replayed; the error tells the caller to verify state. Calls wait for the worker outcome under their own context. After three consecutive failures (`maxReconnectAttempts`, `internal/mcp/reconnect.go`), status becomes unavailable. The tool set is not rebuilt, so the prompt prefix stays stable. `Session.Call` holds its mutex only to read the SDK session, not across the RPC, so calls run in parallel and `Close` never waits behind a hung call.

Statuses: connecting, connected, failed, reconnecting, unavailable, disabled. Tool outcomes: registered, filtered, denied.

Stdio stderr goes to the session log with a `-mcp` suffix at mode `0o600` (`internal/mcp/server_log.go`). Without a log it is discarded for interactive runs and sent to `os.Stderr` otherwise. Stdio processes are wrapped with `sb.WrapCommandMode(c, true)` (`cmd/steiner/runtime_build_mcp.go`). `BuildMCPExposure` (`cmd/steiner/mcp_exposure.go`) maps `sub_agents` to MCP tool names; the default is closed.

## Tests

MCP tests are hermetic and CI-safe: loopback HTTP and local subprocesses only, with no live services, credentials, or outbound network. Third-party servers and remote endpoints are not exercised.

- `TestMCPTransportParity` (`transport_parity_integration_test.go`) runs the same contract through the manager for each transport, including an initial connection failure.
- `newTestMCPServer` (`http_test_support_test.go`) is the loopback HTTP server for HTTP tests.
- `TestStdio` (`client_integration_test.go`) runs against `testdata/fixtureserver/main.go`, an SDK-free JSON-RPC peer.
- `TestLifecycle*` (`lifecycle_test.go`) cover stdio child reaping, reconnect and its cap, timeouts without reconnect, and close during reconnect.
