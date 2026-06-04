## Request

Fix sandbox bash tool bugs:
1. CWD mismatch → `cd` error on every command + first stdout line swallowed
2. Dev toolchains (Go, Rust, Python, Node, etc.) inaccessible inside sandbox

## Overview

Two fixes in `internal/sandbox/`, with cascading simplifications.

### Fix A — CWD mismatch (Issues 1 & 2 from investigation)

Root cause: `WrapCommand` copies `Dir: cmd.Dir` (empty string). Bwrap inherits
host CWD which doesn't exist in the sandbox namespace. Bash emits a `cd`
diagnostic on startup that corrupts marker-based output capture, swallowing
the first line of every command.

Fix: Add `--chdir` with the workspace path to bwrap args in `BuildArgs()`.
With Fix B's mount strategy (paths stay at original locations), `--chdir`
uses the host workspace path directly.

### Fix B — Full root ro-bind (replaces per-toolchain and credential mounts)

Current approach cherry-picks individual mounts (`/usr`, `/bin`, `/lib`,
`/lib64`, `~/.ssh`, `~/.aws`, etc.). This misses dev toolchains entirely —
`~/go`, `~/.rustup`, `~/.nvm`, `~/.pyenv` are all inaccessible.

New approach, matching Codex CLI's strategy:

1. `--ro-bind / /` — entire root filesystem read-only
2. `--bind <workspace> <workspace>` — project dir writable at original path
3. `--bind <sandbox-home> <sandbox-home>` — `.steiner/home/` writable at original path
4. `--dev /dev`, `--proc /proc`, `--tmpfs /tmp` — standard namespace devices
5. Host mounts from config layered on top

This eliminates all conditional `pathExists` mount logic, all credential
mount code, and all env var path remapping. Every toolchain, every config
file, every system path — all accessible through the base ro mount.

**Path remapping goes away.** Paths inside the sandbox are identical to host
paths. HOME stays at the real user home (ro through base mount). The sandbox
home (`.steiner/home/`) stays writable at its real absolute path for tool
state, but is no longer aliased to `/home/steiner`.

### Scope of changes

**`internal/sandbox/mounts.go`** — `BuildArgs` rewritten. Signature changes:
drops `sandboxHome` param (no longer remapped), adds workspace path for
`--chdir`. New body is much shorter: base ro-bind, workspace rw-bind,
sandbox-home rw-bind, dev/proc/tmp, host mounts. No more conditional
pathExists checks for system dirs or credentials.

**`internal/sandbox/mounts_test.go`** — Tests rewritten for new mount
structure. Verify: `--ro-bind / /` present, workspace bound rw at original
path, `--chdir` present, host mounts still work, no `--setenv HOME` override.

**`internal/sandbox/sandbox.go`** — `WrapCommand` updated: `Dir` field no
longer copied from original cmd (not needed — bwrap uses `--chdir`).
`EnsureHome` unchanged (still creates `.steiner/home/`).

**`internal/sandbox/sandbox_test.go`** — Update `WrapCommand` tests. `Dir`
inheritance test changes (Dir no longer propagated).

**`internal/sandbox/env.go`** — `FilterEnv` simplified: remove HOME override
to `/home/steiner`. HOME passes through from host unchanged.

**`internal/sandbox/env_test.go`** — Update HOME override test expectations.

**`docs/TOOL_SANDBOXING.md`** — Update mount layout section to reflect new
`--ro-bind / /` strategy.

### What does NOT change

- `WrapCommand` function signature and wiring in `cmd/steiner/tools.go`
- `Sandbox` struct fields and `New()` constructor
- Sandbox enable/disable/prereq logic
- Approval flow in `execution_pipeline.go`
- `HostMount` config support
- `bash.go` / `bash_session.go` — no changes needed

### Risks

- Some paths under `/` may be special filesystems (sysfs, cgroup, etc.) —
  `--ro-bind / /` binds the root mount, not recursive submounts. bwrap
  only binds the root filesystem itself; submounts like `/sys`, `/run` are
  not automatically included. This is fine — we explicitly mount what we need
  (`/dev`, `/proc`, `/tmp`).
- Broader file exposure vs current cherry-pick — acceptable given sandbox
  already mounts credentials and shares network.

## Verification Strategy

### Repo-mandated checks
- `make check` — runs fmt, vet, lint, test, build (medium cost)
- `go test ./internal/sandbox/... -run TestName` — targeted (cheap)
- `gofmt -w <files>` — formatter (cheap)
- `goimports -w <files>` — import organizer (cheap)

### Manual verification
- Run steiner with sandbox enabled, execute bash commands, verify:
  - No `cd` error on stderr
  - First line of stdout not swallowed
  - `go version`, `go build ./...`, `go test ./...` work inside sandbox
  - `python --version`, `node --version` work if installed
  - Host mounts from config still work

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | `--ro-bind / /` instead of cherry-picked mounts | Covers all toolchains generically, matches Codex CLI approach, eliminates per-language maintenance |
| 2 | Paths stay at original absolute locations | No remapping needed, env vars pass through unmodified, simpler mental model |
| 3 | `--chdir <workspace>` for CWD | bwrap-native, sets CWD after namespace setup, fixes Issues 1 & 2 |
| 4 | Drop HOME override to /home/steiner | HOME stays at real path, readable through base ro mount |
| 5 | Keep .steiner/home/ writable at original path | Sandbox state dir still works, just not aliased |
| 6 | Drop /etc/hosts sanitization from scope | Not a real security boundary — sandbox shares network, Codex doesn't bother either |
