# Tool sandboxing internals

User guide: [Tool Sandboxing](../user/sandboxing.md).

## Resolution

`Executor.runPipeline` (`internal/tool/execution_pipeline.go`) resolves a `ResolvedSandbox` once per call: the wrapper and `ReadOnlyProject`. `readOnlyProjectFor` is true for `BashReadOnlyProjectKey` or plan mode. The builtin bash handler and subprocess execution read the decision with `ResolvedSandboxFrom` (`internal/tool/sandbox_keys.go`) and fail closed when it is missing; MCP and LSP handlers do not read it. `NewExecutor` defaults a nil sandbox to `Unsandboxed{}`.

Subprocess tools are rebuilt with `exec.CommandContext` after wrapping, since wrapping discards `SysProcAttr`, `Cancel`, and `WaitDelay`. `applySubprocessGroup` (`internal/tool/subprocess_unix.go`) runs the child as a process-group leader and sends SIGKILL to the group on cancel. An approved retry after a sandbox denial runs `Unsandboxed{}`, or keeps the read-only wrapper when `readOnlyProjectFor` is true (`execution_denial.go`).

## Mounts and environment

`internal/sandbox` builds bubblewrap arguments with `--unshare-all --share-net --die-with-parent --new-session --ro-bind / / --dev /dev --proc /proc`. `/tmp` is a per-session directory under `.steiner/tmp/sandbox-tmp`, or tmpfs when the read-only project is unsafe. Its creation, locking, and startup cleanup are described in [Sandbox tmp directory lifecycle](#sandbox-tmp-directory-lifecycle).

`planModeWritableBinds` binds the `config.PlanModeWritableDirs()` entries only when each is a non-symlink directory whose resolved path stays under the root. `gitWritableBinds` binds `.git` writable; for linked worktrees it also binds gitdir and commondir, and a symlinked `.git` is rejected.

`appendSandboxCacheArgs` (`internal/sandbox/mounts.go`) binds `privateCacheDir` (`<root>/.steiner/home/cache`) at the host cache path (`~/.cache`, symlinks resolved) by default. With `bind_host_cache`, the host directory is bound there instead. Either bind is emitted only when `cacheMountPath` finds an existing cache directory. The sandbox home `<root>/.steiner/home` is bound writable when it is safe.

`sandbox.host_mounts` paths pass through verbatim on both sides; only `~` is expanded at config load.

`dockerDenyArgs` binds `/dev/null` over each existing socket among `/run/docker.sock`, `/var/run/docker.sock`, and `$XDG_RUNTIME_DIR/docker.sock` when `permissions.docker` is false, and unsets `DOCKER_HOST`. It is applied last.

The environment is the `allowedEnvVars` allowlist plus `env_passthrough`; `env_passthrough_all` skips filtering and emits a warning (`emitSandboxWarning`). A bare `*` in `env_passthrough` is rejected (`validate_runtime.go`). `ssh_overlay.go` builds an in-memory client-config overlay bound with `--ro-bind-data`.

## Sandbox tmp directory lifecycle

`buildRuntimeSandbox` (`cmd/steiner/runtime_build_sandbox.go`) creates a random hex subdirectory of `.steiner/tmp/sandbox-tmp`, and `Sandbox.LockTmpDir` (`internal/sandbox/sandbox_tmp.go`) takes a lifetime advisory `flock` on the directory inode itself. No lock file is created. The kernel releases the lock when the process exits, including on a crash, so a dead runtime cannot pin its directory.

`CleanupOrphans` (`internal/sandbox/cleanup.go`) runs once when the runtime is built, via `cleanupSandboxTmpOrphans`, with a 48h age threshold. `removeDirIfUnlocked` (`internal/sandbox/tmpdir_lock_linux.go`) decides each removal: it acquires the directory lock, re-checks the directory mtime against the cutoff, rejects a symlink or a path whose inode differs from the one just locked (`os.SameFile`), then removes the tree. A busy lock means a live runtime; an unprobeable lock means liveness cannot be ruled out. Both leave the directory alone. A directory that no runtime holds a lock on, including one written by a runtime that does not lock, is removed on age alone. On non-Linux builds `removeDirIfUnlocked` removes nothing.

A lost session tmp directory is recovered before the next wrapped command and by `ResetTmp` (`/clear`, `/resume`, `/fork`, and workflow handoff, driven by `resetSandboxTmp` in `cmd/steiner/interactive_session.go`). A missing path is recreated empty; a real replacement directory, such as one a tool recreated with `MkdirAll`, is adopted when its lock is free. Contents written before the loss are not restored. The first recovery noticed by `ensureTmpDir` reports `tmpReprovisionMessage` once per session as a `session_health` warning scoped `output.SessionHealthScopeSandboxTmp`, emitted through `wireSandboxTmpWarning` (`cmd/steiner/runtime_build_sandbox.go`), which `buildInteractiveRuntime` (`cmd/steiner/interactive_session.go`) and `newPhaseRunner` (`cmd/steiner/cmd_oneshot.go`) re-point at their live sink. A symlink or non-directory path is `errTmpDirNotOwned`: an error in build mode and a tmpfs `/tmp` fallback when the project is read-only. A replacement whose lock is held is handled the same way.

## Status

`PrereqCheck` checks the platform, then bwrap on `PATH`, then runs the probe `bwrap --ro-bind / / true` (`internal/sandbox/probe.go`). A failed check gives status `unavailable`; a nested `bwrap` that cannot create namespaces fails the probe the same way. The other status values are `active` and `bypassed` (`sandbox.enabled` false, for example `--unsafe`).
