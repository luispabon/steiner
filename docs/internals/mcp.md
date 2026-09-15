# MCP servers internals

User-facing documentation: [MCP servers](../user/mcp.md).

## Package map and naming

`internal/mcp` owns client connections, transport selection, naming, filtering, approval, output bounds, and state. `cmd/steiner` wires the manager, runtime initialization, interactive approver, and child-agent exposure. `internal/sandbox` wraps local stdio processes. `internal/tui` renders status, overlay, and warnings. `internal/config` loads defaults and validation.

`internal/mcp/naming.go` sanitizes every rune outside `[A-Za-z0-9_-]` to `_`. If the composed `mcp__server__tool` exceeds 64 characters or either segment changes, it truncates to the budget and appends an 8-hex SHA-256 suffix over the original `server\x00tool` inputs. The hash is a pure function of those inputs, so a failed server cannot change another server's names. This prevents collisions from lossy sanitization and truncation.

## Approval and filtering wiring

Approval is selected per server. `allow` is downgraded to `ask` when the live plan-mode closure reports plan mode; `deny` prevents registration and the handler also fails closed if a stale definition remains. Approval previews sort top-level keys, truncate strings to 60 runes, and collapse nested values. Session grants use the original `server\x00tool` pair, not the registry name, and are held by the interactive wiring only. A nil approver returns `approval_denied`. Annotation trust is opt-in: `readOnlyHint` can bypass approval only when enabled, while omitted `destructiveHint` and `openWorldHint` default to true.

`allowed_tools` is applied before `blocked_tools`. Nil and explicit-empty allowlists differ, while missing and explicit-empty `sub_agents` both expose no tools to children. Filter warnings are emitted once per unknown advertised name in sorted order.

## Lifecycle wiring

The manager connects enabled servers concurrently. Each attempt covers initialization and the initial tool list under `connect_timeout`. Non-interactive runtime construction waits for `WaitInit` before freezing the registry. Interactive construction starts asynchronous connections; the first turn runs `mcpInitOnce`, waits, registers connected definitions, and arms the state producer before the model call. During the earlier states-only window, the sidebar can show `connected` before definitions are registered; the overlay reads advertised tools from the snapshot rather than the registry.

A classified transport error starts a sequential fresh-connect worker. It does not re-list tools, and the failed call is not replayed. A new session swaps under the existing handle; after three consecutive failures the worker stops and status becomes unavailable. Calls during reconnect wait for the worker outcome under their own timeout. The tool set remains frozen through reconnect to preserve prompt-cache prefixes. Startup warning generations are deduplicated by the TUI.

Stdio stderr is derived from the session log with a `-mcp` suffix and mode `0o600`; without a log it is discarded for interactive sessions and sent to `os.Stderr` for exec and oneshot. HTTP servers have no subprocess stderr.

## Deferred features

| Feature | Decision |
|---|---|
| OAuth for HTTP | Deferred: no test peer was available; static headers support bearer tokens. |
| Resources, prompts, sampling, elicitation, roots, completions | Not implemented; only tool discovery and calls are wired. |
| Persistent approval grants | Deferred; grants remain in memory. Follow-up issues #438 and #439. |
| Per-tool approval overrides | Deferred; approval is per-server. |
| `steiner mcp debug` and `steiner mcp add` | Not built. |
| Live config reload and live enable/disable | Deferred; server set and overlay actions are not dynamic. |
| Retry when provably safe | Deferred; even `idempotentHint` does not permit replay. |
| Per-file `mcp/.yaml` | Not implemented; configuration is in one block. |
| Legacy HTTP+SSE | Not implemented; only Streamable HTTP is supported. |
| Not shipping MCP | Superseded; MCP ships with this release. |

## Verification fixtures

MCP tests are hermetic and CI-safe. They use loopback HTTP and local subprocesses, with no live services, credentials, or outbound network.

- `TestMCPTransportParity` exercises both transports through Manager and tool calls: initialization, discovery, success, MCP `isError`, and initial connection failure (`internal/mcp/transport_parity_integration_test.go`).
- `newTestMCPServer` in `internal/mcp/http_test_support_test.go` backs HTTP parity, integration, and lifecycle tests.
- `TestStdio` uses the independent SDK-free fixture `internal/mcp/testdata/fixtureserver/main.go`; it is the JSON-RPC peer for stdio tests, unlike the SDK-backed HTTP server.
- Lifecycle tests cover stdio reaping/reconnect and HTTP dropped-session/reconnect. HTTP integration tests cover headers, close, and failure states.

These fixtures do not validate third-party `npx` packages or remote endpoints. Manual e2e work is tracked in #438 and #439.
