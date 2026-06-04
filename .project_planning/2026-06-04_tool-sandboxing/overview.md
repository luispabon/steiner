## Request

Implement OS-level tool sandboxing for steiner using bubblewrap on Linux. The sandbox enforces "no writes outside the workspace" as a hard invariant. Standard mode is always sandboxed; `--unsafe` bypasses all restrictions. Zero configuration for most workflows. Replace the existing tool approval system with sandbox-boundary-violation prompts.

## Overview

### Scope (v1)

- **Platform**: Linux only (bubblewrap)
- **Sandboxed tools**: bash tool and external subprocess tools
- **Not sandboxed**: Built-in Go tools (read, mutate, glob, grep, ls) — these already enforce PathPolicy in-process
- **Approval overhaul**: Remove existing `ApprovalConfig`/`ApprovalMode` system, replace with sandbox boundary prompts
- **Deferred to v2**: Session path grants, audit logging, macOS seatbelt, Windows/WSL2

### Architecture

**New package `internal/sandbox`** — owns sandbox lifecycle, bubblewrap invocation, mount assembly, environment filtering, and prerequisite checks.

**Key type: `Sandbox`** — configured once at startup, provides `WrapCommand(cmd *exec.Cmd) *exec.Cmd` to wrap any process execution with bubblewrap. In unsafe mode, `WrapCommand` is a no-op passthrough.

**Bash tool fork** — Fork Dive's `BashSession` (persistent bash process with marker-based output capture) into `internal/tool/builtin/`. Drop the Dive `BashTool` wrapper; steiner already has its own in `builtin/bash.go`. The forked session accepts a `CommandWrapper` function so sandbox can intercept process creation. Other Dive dependencies (read, ls, fetch_url) stay on Dive — only bash is forked.

**Config additions** — New `SandboxConfig` struct in `internal/config/`:
```yaml
sandbox:
  enabled: true          # default true, --unsafe sets false
permissions:
  docker: false          # default false
host_mounts:
  - path: ~/.kube
    mode: ro
```

The existing `approval` config section is removed entirely.

**CLI** — `--unsafe` flag disables sandbox. Standard mode requires `bwrap` on PATH; hard error if missing.

**Sandbox home** — `.steiner/home/` directory, persistent across sessions, auto-gitignored. Inside sandbox: `HOME=/home/steiner`. Developer credentials (`~/.ssh`, `~/.gitconfig`, `~/.aws`, etc.) bind-mounted read-only onto sandbox home.

### Approval system replacement

The existing per-tool approval system (`ApprovalConfig`, `ApprovalMode`, `ApprovalResolver`, per-tool overrides) is fully removed. In its place:

**New model: sandbox boundary prompts**

Approval prompts appear ONLY when an operation hits a sandbox boundary. There are no per-tool approval configurations.

**For built-in tools (mutate, read, etc.):**
1. PathPolicy pre-checks paths as today
2. If path is outside sandbox boundary → prompt user "this path is outside the sandbox, allow?"
3. If approved → execute with relaxed policy for this operation
4. If denied → return steiner-aware error

**For bash commands:**
1. Execute command inside bwrap sandbox
2. If command fails AND output indicates sandbox denial (permission denied on paths outside mounts) → detect and prompt user "this command was blocked by the sandbox, re-run without sandbox?"
3. If approved → re-execute the same command without bwrap
4. If denied → return the sandbox error with "how to grant" instructions

**For `--unsafe` mode:**
- No sandbox, no prompts. Everything executes freely with full user permissions.

**Sandbox violation prompt UI** — Reuses the existing `ApprovalResponder` interface and TUI approval flow. The `ApprovalRequest` type is adapted to carry sandbox violation context (denied path, reason, grant instructions) instead of per-tool approval metadata.

### Mount layout (standard mode)

```
workspace           → /workspace              rw
.steiner/home/      → /home/steiner           rw
/tmp (tmpfs)        → /tmp                    rw
/usr, /bin, /lib*   → same paths              ro
~/.ssh              → /home/steiner/.ssh      ro
~/.gitconfig        → /home/steiner/.gitconfig ro
~/.git-credentials  → /home/steiner/.git-credentials ro
~/.config/git       → /home/steiner/.config/git ro
~/.aws              → /home/steiner/.aws      ro
~/.kube             → /home/steiner/.kube     ro
~/.docker/config.json → /home/steiner/.docker/config.json ro
SSH_AUTH_SOCK       → bind socket             ro
/etc/resolv.conf    → /etc/resolv.conf        ro
/etc/hosts          → /etc/hosts              ro
/etc/ssl/certs      → /etc/ssl/certs          ro
/proc               → fresh proc mount
/dev                → minimal devtmpfs
```

Host mounts from `.steiner/config.yaml` are layered as additional ro binds.

### Environment variable allowlist

Only pass through:
```
PATH, HOME (overridden), TERM, LANG, LC_*, TZ, SSH_AUTH_SOCK,
EDITOR, VISUAL, SHELL, USER, LOGNAME, XDG_RUNTIME_DIR
```

Credential-bearing vars (`AWS_SECRET_ACCESS_KEY`, `GH_TOKEN`, etc.) blocked. Auth via mounted credential files and SSH agent socket.

### Error reporting

Sandbox denials return steiner-aware errors:
```
Access denied by steiner sandbox

Path: /home/user/.ssh/config
Reason: Outside workspace
How to grant: Add a host_mount in .steiner/config.yaml
```

### Integration points

1. `internal/sandbox/` — new package, sandbox construction and bwrap invocation
2. `internal/config/` — `SandboxConfig`, `PermissionsConfig`, `HostMount` structs; remove `ApprovalConfig`, `ApprovalMode`
3. `internal/tool/builtin/bash.go` — fork Dive BashSession, wire sandbox command wrapper
4. `internal/tool/builtin/bash_session.go` — new file, forked persistent session
5. `internal/tool/execution_pipeline.go` — wire sandbox for subprocess tools; replace approval authorization with sandbox boundary prompts
6. `internal/tool/approval.go` — remove `ApprovalResolver`, `ApprovalMode` resolution; replace with `SandboxViolationResolver`
7. `internal/tool/preview.go` — adapt preview to show sandbox violation context instead of per-tool approval metadata
8. `cmd/steiner/` — `--unsafe` flag, sandbox initialization, prerequisite check
9. `docs/TOOL_SANDBOXING.md` — documentation with v2 roadmap

### Invariants

1. Standard mode always sandboxed
2. `--unsafe` always unsandboxed — no sandbox, no prompts
3. Only writable regions: workspace, `.steiner/home/`, `/tmp`
4. Path readable only if mounted
5. Child permissions ≤ parent permissions
6. All tools pass through same policy layer
7. Docker opt-in per project
8. Project config cannot disable sandbox or enable outside-workspace writes
9. `bwrap` required on PATH in standard mode — hard error if missing
10. Approval prompts appear ONLY on sandbox boundary violations, never per-tool

### What is NOT changing

- PathPolicy (existing path validation stays, sandbox adds OS-level enforcement; PathPolicy becomes the pre-check that triggers sandbox violation prompts for built-in tools)
- Built-in tool handlers (read, mutate, glob, grep, ls, display_file) — already in-process, already policy-checked
- Dive dependencies for read, ls, fetch_url — only bash is forked
- Provider/agent/prompt packages — untouched
- `ApprovalResponder` interface and TUI prompt flow — repurposed for sandbox violations

### What IS being removed

- `ApprovalConfig` struct and `approval:` config section
- `ApprovalMode` type and constants (`auto`, `prompt`, `deny`)
- `ApprovalResolver` and `ResolveApprovalMode`
- Per-tool approval overrides (`tool_overrides`)
- `ToolDef.Approval` field
- All call sites that resolve, check, or branch on `ApprovalMode`

## Verification Strategy

From CLAUDE.md and Makefile:

| Command | Purpose | Cost |
|---------|---------|------|
| `gofmt -w <files>` | Format | Cheap |
| `goimports -w <files>` | Imports | Cheap |
| `go vet ./...` | Static analysis | Cheap |
| `go build ./...` | Compile check | Cheap |
| `go test ./path/to/pkg -run TestName` | Targeted tests | Cheap |
| `go test ./...` | Full test suite | Medium |
| `go test -race ./...` | Race detection | Medium |
| `golangci-lint run ./...` | Lint | Medium |
| `govulncheck ./...` | Vulnerability scan | Medium |
| `make check` | All of the above | Expensive |
| `make build-binaries` | Binary build | Cheap |

Prefer targeted tests during implementation, `make check` at the end.

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Linux-only v1 | Fastest path to working sandbox; macOS/Windows follow |
| 2 | Bash + subprocess only | Built-in tools already enforce PathPolicy in-process |
| 3 | Fork Dive BashSession | Own the process execution path for sandbox wrapping; user requested fork with tests |
| 4 | Hard require bwrap | Clear error > silent degradation. --unsafe always available |
| 5 | Defer session path grants | Touches agent-level message parsing, separate concern |
| 6 | Defer audit logging | Layer on top once sandbox boundary is solid |
| 7 | Sandbox home in .steiner/home/ | Persistent, gitignored, dev credentials overlaid ro |
| 8 | New internal/sandbox package | Clean boundary; doesn't pollute tool or config packages |
| 9 | Remove approval system | Sandbox replaces approval as enforcement layer; prompts only on boundary violations |
| 10 | Bash sandbox retry | On sandbox denial, prompt user, re-execute unsandboxed if approved |
| 11 | Built-in tools pre-check prompt | PathPolicy detects boundary violation before execution, prompts user |
| 12 | --unsafe = no prompts ever | Full user-equivalent access, zero friction |
