# Tool sandboxing internals

User guide: [Tool Sandboxing](../user/sandboxing.md).

## Resolution

`Executor.runPipeline` (`internal/tool/execution_pipeline.go`) resolves a `ResolvedSandbox` once per call: the wrapper and `ReadOnlyProject`. `readOnlyProjectFor` is true for `BashReadOnlyProjectKey` or plan mode. The builtin bash handler and subprocess execution read the decision with `ResolvedSandboxFrom` (`internal/tool/sandbox_keys.go`) and fail closed when it is missing; MCP and LSP handlers do not read it. `NewExecutor` defaults a nil sandbox to `Unsandboxed{}`.

Subprocess tools are rebuilt with `exec.CommandContext` after wrapping, since wrapping discards `SysProcAttr`, `Cancel`, and `WaitDelay`. `applySubprocessGroup` (`internal/tool/subprocess_unix.go`) runs the child as a process-group leader and sends SIGKILL to the group on cancel. An approved retry after a sandbox denial runs `Unsandboxed{}`, or keeps the read-only wrapper when `readOnlyProjectFor` is true (`execution_denial.go`).

## Mounts and environment

`internal/sandbox` builds bubblewrap arguments with `--unshare-all --share-net --die-with-parent --new-session --ro-bind / / --dev /dev --proc /proc`. `/tmp` is a per-session directory under `.steiner/tmp/sandbox-tmp`, or tmpfs when the read-only project is unsafe. Orphaned session directories are cleaned at startup.

`planModeWritableBinds` binds the `config.PlanModeWritableDirs()` entries only when each is a non-symlink directory whose resolved path stays under the root. `gitWritableBinds` binds `.git` writable; for linked worktrees it also binds gitdir and commondir, and a symlinked `.git` is rejected.

`appendSandboxCacheArgs` (`internal/sandbox/mounts.go`) binds `privateCacheDir` (`<root>/.steiner/home/cache`) at the host cache path (`~/.cache`, symlinks resolved) by default. With `bind_host_cache`, the host directory is bound there instead. Either bind is emitted only when `cacheMountPath` finds an existing cache directory. The sandbox home `<root>/.steiner/home` is bound writable when it is safe.

`sandbox.host_mounts` paths pass through verbatim on both sides; only `~` is expanded at config load.

`dockerDenyArgs` binds `/dev/null` over each existing socket among `/run/docker.sock`, `/var/run/docker.sock`, and `$XDG_RUNTIME_DIR/docker.sock` when `permissions.docker` is false, and unsets `DOCKER_HOST`. It is applied last.

The environment is the `allowedEnvVars` allowlist plus `env_passthrough`; `env_passthrough_all` skips filtering and emits a warning (`emitSandboxWarning`). A bare `*` in `env_passthrough` is rejected (`validate_runtime.go`). `ssh_overlay.go` builds an in-memory client-config overlay bound with `--ro-bind-data`.

## Status

`PrereqCheck` checks the platform, then bwrap on `PATH`, then runs the probe `bwrap --ro-bind / / true` (`internal/sandbox/probe.go`). A failed check gives status `unavailable`; a nested `bwrap` that cannot create namespaces fails the probe the same way. The other status values are `active` and `bypassed` (`sandbox.enabled` false, for example `--unsafe`).
