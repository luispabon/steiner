# Tool sandboxing internals

User-facing documentation: [Tool sandboxing](../user/sandboxing.md).

## Resolution

`Executor.runPipeline` (`internal/tool/execution_pipeline.go`) resolves a `ResolvedSandbox` once per call: the wrapper and whether the project is read-only. The project is read-only for `BashReadOnlyProjectKey` or plan mode. The builtin bash handler and subprocess execution read it with `ResolvedSandboxFrom` (`internal/tool/sandbox_keys.go`) and fail closed when it is missing; MCP and LSP handlers do not read it. `NewExecutor` defaults a nil sandbox to `Unsandboxed{}`.

Subprocess tools are rebuilt with `exec.CommandContext` after wrapping, since wrapping discards `SysProcAttr`, `Cancel`, and `WaitDelay`. `applySubprocessGroup` (`internal/tool/subprocess_unix.go`) runs the child as a process-group leader and sends SIGKILL to the group on cancel. An approved retry after a sandbox denial runs `Unsandboxed{}`, or keeps the read-only wrapper when `readOnlyProjectFor` is true (`execution_denial.go`).

## Mounts and environment

`internal/sandbox` builds bubblewrap arguments with `--unshare-all --share-net --die-with-parent --new-session --ro-bind / / --dev /dev --proc /proc`. `/tmp` is a per-session directory under `.steiner/tmp/sandbox-tmp`, or tmpfs when the read-only project is unsafe. Orphaned session directories are cleaned at startup.

The project is bound read-write, or read-only with writable binds for plan mode (`.steiner/plans`, `.steiner/security`, and real non-symlink directories under the root, from `planModeWritableBinds`) and for git (`.git`; linked worktrees also get gitdir and commondir; a symlinked `.git` is rejected). The sandbox home `<root>/.steiner/home` is writable, and host `~/.cache` is bound at `<home>/cache` unless `bind_host_cache` is set.

`sandbox.host_mounts` paths are used verbatim on both sides; only `~` expands. `mode: rw` gives `--bind`, and other modes give `--ro-bind`. When `permissions.docker` is false, `dockerDenyArgs` binds `/dev/null` over each existing socket among `/run/docker.sock`, `/var/run/docker.sock`, and `$XDG_RUNTIME_DIR/docker.sock`, and unsets `DOCKER_HOST`. It is applied last.

The environment is the `allowedEnvVars` allowlist plus `env_passthrough`; `env_passthrough_all` skips filtering and emits a warning (`emitSandboxWarning`). A bare `*` in `env_passthrough` is rejected (`validate_runtime.go`). `ssh_overlay.go` builds an in-memory client-config overlay bound with `--ro-bind-data`.

## Status

`PrereqCheck` checks the platform, then bwrap on `PATH`, then runs the probe `bwrap --ro-bind / / true` (`internal/sandbox/probe.go`). A failed check gives status `unavailable`; a nested `bwrap` that cannot create namespaces fails the probe the same way. The other status values are `active` and `bypassed` (`sandbox.enabled` false, for example `--unsafe`).
