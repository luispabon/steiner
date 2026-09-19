# MCP servers

Steiner can connect external tools via the Model Context Protocol (MCP). A local stdio subprocess or remote HTTP endpoint advertises tools the agent can call alongside built-in tools. This page covers configuration, naming, approval, sandboxing, lifecycle, output limits, TUI surfaces, security, and troubleshooting.

## What MCP is

Steiner implements an MCP client. It launches or dials MCP servers, lists their tools, and calls them with the server's argument schema. Supported transports are `stdio` and Streamable HTTP.

## Configuration

MCP is configured under the `mcp` block; see the [mcp config block](configuration.md#mcp-block) for the full field reference. `mcp.enabled` defaults to `true`, while each server defaults to `enabled: false` and must be explicitly enabled.

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

Per-server defaults are `stdio`, approval `ask`, and `connect_timeout` `15s`. The configured `env` map is passed to that server and is not subject to the inherited-host environment allowlist. Use it for server credentials or API keys.

## Naming

MCP tools register as `mcp__<server>__<tool>`. Clean names no longer than 64 characters are used as-is. Names needing sanitisation or truncation receive an 8-hex-character suffix derived from the original server and tool names. This keeps names unique and stable. `steiner tools` prints registry names.

## Approval model

Each server has an approval mode:

- `ask`: every call prompts for approval.
- `allow`: calls run without prompting in build mode. Plan mode downgrades this to `ask`.
- `deny`: the server connects but advertises no callable tools.

Approval previews show sorted arguments, truncate long strings, and collapse nested structures. `Allowed for session` stores an in-memory grant for that server/tool until the run ends. Grants are not persisted.

`trust_annotations: true` can skip approval for read-only tools whose annotations explicitly say `readOnlyHint: true`. Destructive and open-world hints default to true when omitted, so missing annotations are not trusted. A call needing approval fails closed if no approver exists. This means `ask` tools fail in exec and oneshot runs; only allowed build calls and trusted read-only tools can run there.

Plan mode is read live, so mode switches apply without rebuilding tool definitions.

## Sandboxing and tool filtering

Local stdio servers are sandbox-wrapped when sandboxing is enabled. They retain network access, have a read-only project except for the normal sandbox writable areas, and are not subject to this protection when remote HTTP is used. See [Sandboxing](sandboxing.md) for host visibility and limits.

`allowed_tools` filters first and `blocked_tools` filters second. Entries are advertised MCP names, not registry names. Missing `allowed_tools` means no allowlist; an explicit empty list filters every tool. Unknown filter entries produce sorted, non-fatal warnings. `sub_agents` controls which child agent types may call a server; missing and explicit empty both expose no tools to children.

## Lifecycle

Enabled servers connect in parallel, each bounded by `connect_timeout`. A failed server does not block others. Non-interactive runs wait for initialization before building the tool registry. Interactive runs paint immediately, then finish initialization before the first model call.

A dead stdio process or lost HTTP session triggers a fresh reconnect, but the failed call is never replayed. Calls during reconnect wait for its result, bounded by their own timeout. After three consecutive failed reconnects the server becomes `unavailable`. Tool definitions stay registered while a server is reconnecting, keeping the prompt prefix stable. Statuses are `connecting`, `connected`, `failed`, `reconnecting`, and `unavailable`.

Server stderr goes to a derived `-mcp` log file when session logging is configured. Otherwise it is discarded in interactive mode and sent to process stderr in non-interactive modes.

## Output bounds

Flattened text is capped by `limits.tool_output_max_bytes` (default `65536`) and gets a marker reporting shown and total bytes. Non-text content is named but not decoded. Calls use `limits.tool_timeout_default` (default `30s`) or a per-tool override keyed by the full registered name. Timeouts do not trigger reconnect.

## TUI surfaces

`/mcp` lists every declared server, its status and transport, errors, and advertised tool outcomes (`registered`, `filtered`, or `denied`). The sidebar shows a compact connected/total row with a spinner during connection and an error state for failed servers. MCP calls in the transcript show server and tool provenance. Warnings are emitted once per server failure generation.

## Security posture

MCP descriptions and results are third-party content and can carry prompt injection. Treat servers as untrusted code. Path policy does not inspect opaque MCP arguments; approval is the boundary. Sandboxed stdio retains network access and read access to the host filesystem, including credential files, and remote HTTP is not sandboxed. Annotation trust is off by default. HTTP servers that redirect to a different origin (scheme, host, or port) are refused so configured headers are never forwarded to another host; same-origin redirects still carry them. Tools whose input schema exceeds 64 KiB when marshalled are skipped with a warning.

## Troubleshooting

- **`failed`**: check the executable, transport, URL, and credentials.
- **`unavailable`**: three reconnects failed; fix the server and restart Steiner.
- **Missing tool**: inspect `/mcp`; `filtered`, `denied`, and `no tools advertised` identify the cause.
- **No approver**: expected for `ask` tools in non-interactive runs.
- **`disconnected, verify state`**: the call may have been applied before transport loss; verify before retrying.
- **Unexpected name**: use `steiner tools` to find the hashed form.
- **Plan mode prompts**: `allow` is downgraded to `ask` there.

For source-level naming, lifecycle wiring, deferred features, and verification fixtures, see [MCP internals](../internals/mcp.md).
