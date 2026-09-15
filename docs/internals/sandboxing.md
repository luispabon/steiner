# Tool Sandboxing internals

User-facing documentation: [Tool Sandboxing](../user/sandboxing.md).

## Executor and wrapper resolution

Every tool call is resolved once by `internal/tool.Executor.runPipeline` into a `ResolvedSandbox`: the active `SandboxWrapper` and whether the project must be mounted read-only. Both tool handlers and subprocess execution consume this decision from context. A handler invoked outside the pipeline without a resolved decision fails closed. Unsafe mode uses explicit `internal/tool.Unsandboxed{}` rather than a nil wrapper; parent and child executors always carry a wrapper.

The same pipeline keeps bash and config-defined subprocess tools consistent. In plan mode both receive a read-only project mount. MCP stdio wrapping is wired from runtime construction and applies only to locally launched processes.

## Mount resolution

`internal/sandbox` constructs bubblewrap arguments with the host root bound read-only, workspace and sandbox home writable where policy allows, a minimal `/dev`, fresh `/proc`, session-scoped `.steiner/tmp/sandbox-tmp/<id>` at `/tmp`, and the workspace as the initial directory. Host mounts are resolved at startup, preserve host paths, default to read-only, and can grant writes outside the workspace. Docker-denial mode masks existing `/run/docker.sock` and `$XDG_RUNTIME_DIR/docker.sock` with `/dev/null` and unsets `DOCKER_HOST`; nonexistent sockets are skipped so startup does not fail.

The environment builder passes only the built-in allowlist plus configured passthrough values. `env_passthrough_all` deliberately bypasses filtering. MCP server env is appended after wrapping because it is declared config, not inherited host state.

SSH handling can build an in-memory client-config overlay for system config and static drop-ins. Private keys and other user SSH state are not copied. Dynamic includes are skipped when they cannot be safely resolved.

## Nested namespace diagnosis

A nested `bwrap` command can report:

```text
bwrap: No permissions to create a new namespace, likely because the kernel does not allow non-privileged user namespaces. See <https://deb.li/bubblewrap> or <file:///usr/share/doc/bubblewrap/README.Debian.gz>.
```

The message can be misleading. A sandboxed process has no capabilities, and creating a mount namespace together with a user namespace requires `CAP_SYS_ADMIN` in the current namespace. `kernel.unprivileged_userns_clone` mainly relaxes creation from the initial namespace. `PrereqCheck` probes `bwrap --ro-bind / / true` at startup; a failed probe reports sandbox status `unavailable`, the same path used when bwrap is absent.

## Package map

| Area | Responsibility |
|---|---|
| `internal/sandbox` | Bubblewrap command, mounts, environment, prerequisite checks, SSH overlay |
| `internal/tool` | Per-call sandbox resolution and executor pipeline |
| `cmd/steiner` | Runtime sandbox construction and MCP stdio wiring |
| `internal/mcp` | Local server process wrapping and configured server environment |

## V2 roadmap

These decisions are deferred:

- **Session-scoped path grants**: runtime grants without config changes. V1 uses static configuration; temporary decision tracking is deferred.
- **Audit logging**: path and operation records for tool executions. V1 focuses on enforcement; detailed logging adds error-path overhead.
- **macOS Seatbelt**: a platform-specific implementation, deferred while V1 remains Linux-first.
- **Windows/WSL2**: a platform-specific namespace implementation requiring dedicated tooling and tests.
