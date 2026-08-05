# Tool Sandboxing

Steiner uses Linux container technology (`bubblewrap`) to sandbox tool execution and protect the host from uncontrolled writes by model-driven code.

## Overview

By default, `bash` and subprocess tools run inside a **sandbox** — a restricted execution environment with a read-only view of the host filesystem, a fresh process tree, and writable access limited to the workspace.

The sandbox isolates model-driven code from:
- Writing to any path outside the workspace
- Leaking credentials via environment variables (env var allowlist)
- Polluting the host process tree
- OpenSSH rejecting sandbox-visible system config when client-only commands run inside the sandbox

The entire host filesystem is visible inside the sandbox as read-only. This means all installed toolchains (Go, Rust, Python, Node, etc.), system libraries, and user config files are accessible without per-path mount configuration. Only the workspace and the sandbox state directory are writable.

For `ssh` commands, Steiner can create an ephemeral in-memory OpenSSH client-config overlay for the system config and static drop-ins. Nothing is copied into the workspace, and the overlay only covers the system SSH client config, not private keys or other user SSH state. Dynamic include paths may be skipped when they cannot be resolved safely ahead of time. If OpenSSH still rejects the config inside the sandbox, Steiner can ask for approval to rerun the command outside the sandbox.

## Goals and non-goals

### What sandboxing protects

- **Write isolation**: Code run by the model cannot write to any path outside the workspace root
- **Env var filtering**: Credential-bearing environment variables are blocked by an allowlist
- **Process isolation**: Fresh PID namespace prevents interference with host processes
- **Temp file isolation**: `/tmp` inside the sandbox is bind-mounted from a session-scoped directory under `.steiner/tmp/sandbox-tmp/<id>/`. Sandbox writes cannot affect the host's `/tmp`, and files persist across tool invocations within a single session (cleared on `/clear`, `/resume`, `/fork`, or process exit).

### What sandboxing does NOT protect against

- **Reading host files**: The entire host filesystem is visible read-only — credentials on disk (SSH keys, cloud configs) are readable inside the sandbox
- **Network exfiltration**: The sandbox shares the host network (`--share-net`); sandboxed code can make outbound connections
- **Deliberate attacks**: Sandboxing is not a security boundary against intentional malicious code
- **Exploits in sandboxing itself**: Bugs in `bubblewrap` or kernel namespace isolation could allow escape

In short: **sandboxing prevents accidental writes and env var leakage, not deliberate exfiltration.** The host filesystem is readable; only writes are constrained.

## Standard vs unsafe mode

### Standard mode (default)

```bash
go run ./cmd/steiner
# or
./bin/steiner
```

- All `bash` commands run inside the sandbox
- Subprocess tools run inside the sandbox
- Sandbox boundary violations (attempting to access files outside workspace) prompt the user to decide:
  - **Allow for this session**: add a host mount for the path and continue
  - **Use --unsafe**: re-run without sandboxing
- Built-in Go tools (`read`, `mutate`, `glob`, `grep`, `ls`) are NOT sandboxed; they enforce `PathPolicy` in-process

### Unsafe mode

```bash
go run ./cmd/steiner --unsafe
# or
./bin/steiner --unsafe
```

- All sandboxing is disabled
- `bash` commands run directly on the host
- No boundary violation prompts
- Use when:
  - You need access to paths outside the workspace
  - You're running tools that require full filesystem access
  - You're debugging sandbox issues
  - You trust the current model input completely

**Warning**: Unsafe mode disables the primary protection mechanism. Use it sparingly and only when you understand the consequences.

## Platform requirements

### Linux (supported)

- **OS**: Linux kernel 3.8+ (supports user namespaces)
- **Tool**: `bubblewrap` (`bwrap`) must be installed and in `$PATH`
- **Installation**:
  ```bash
  # Ubuntu/Debian
  sudo apt-get install bubblewrap

  # Fedora/RHEL
  sudo dnf install bubblewrap

  # Alpine
  apk add bubblewrap

  # macOS (via Homebrew) — not supported, see below
  brew install bubblewrap  # installs but not functional on macOS
  ```

### macOS, Windows (not supported)

- Sandboxing is **not available** on macOS or Windows
- Steiner detects the platform at startup and disables sandboxing automatically
- `--unsafe` flag is ignored (sandboxing is already disabled)
- **Workaround**: Use WSL2 on Windows or a Linux VM
- **Status reporting**: When sandboxing is unavailable, `sandbox.status` is set to `unavailable` and the UI shows a warning banner (unless `sandbox.warning_on_unsupported_platform` is disabled). Bash and subprocess tools run unsandboxed with a graceful fallback — no hard failure.

## Mount layout

The sandbox uses a simplified mount strategy:

1. **Root filesystem**: The entire host filesystem is mounted read-only at `/` using `--ro-bind / /`
2. **Writable overlay**: Only the workspace and sandbox home are bind-mounted at their original host paths as read-write
3. **Working directory**: The initial process working directory is set to the workspace root

This strategy ensures:
- Host paths are preserved inside the sandbox (no path remapping like `/workspace` or `/home/steiner`)
- All system binaries, libraries, and standard utilities are accessible by default
- Only explicitly-mounted paths are writable (workspace, sandbox home)

| Mount | Sandbox path | Mode | Purpose |
|-------|--------------|------|---------|
| `--ro-bind / /` | `/` | ro | Entire host filesystem as read-only base |
| `--bind <workspace>` | same as host | rw | Project files and artifacts |
| `--bind <sandbox-home>` | same as host | rw | `.steiner/home/` — sandbox state directory |
| `--dev /dev` | `/dev` | minimal | Device nodes (devtmpfs) |
| `--proc /proc` | `/proc` | fresh | Process information (ProcFS) |
| `--bind .steiner/tmp/sandbox-tmp/<id>` | `/tmp` | rw | Session-scoped temporary files (persists across tool invocations) |
| `--chdir <workspace>` | — | — | Sets initial working directory |
| `host_mounts` (config) | same as host | ro/rw | Additional paths from `host_mounts:` config |

All host paths (toolchains, credentials, system libraries) are accessible inside the sandbox at their original locations through the read-only root bind. No per-path auto-mounting is needed — `~/.ssh`, `~/.gitconfig`, `~/.aws`, `~/go`, `~/.rustup`, `~/.nvm`, etc. are all visible by default.

To grant **writable** access to a path outside the workspace, use `host_mounts` config with `mode: rw`.

## Sandbox home

The sandbox workspace includes a `.steiner/home/` directory that serves as an isolated working directory for tool caches and session state within the project. The host `$HOME` remains accessible inside the sandbox at its original path (e.g., `/home/username`) as read-only through the root bind.

**Why a separate sandbox home?**
- Standard tools like `git`, `ssh`, and package managers store config and cache in `$HOME`
- Using a workspace-scoped home directory isolates sandbox-generated config (caches, session files) from the host home
- The sandbox home persists across sessions, allowing the model to maintain tool state within the project

**Gitignore**:
`.steiner/home/` is covered by `.steiner/.gitignore`, which ignores all files under `.steiner/` so they don't pollute version control.

**What goes in `.steiner/home/`**:
- `.git/` — Git metadata if the model runs `git init`
- `.npm/` — npm cache and config if the model installs packages
- `.ssh/` — sandbox-local SSH session state and generated client files, not copied host SSH config
- `.bash_history`, `.zsh_history` — Shell history
- Tool caches (`pip`, `cargo`, etc.)

**Clearing the sandbox home**:
If you want to reset the sandbox environment:
```bash
rm -rf .steiner/home/
```

## Environment variable allowlist

Inside the sandbox, only environment variables on a built-in allowlist are passed through from the host; everything else is dropped. This is an allowlist, not a denylist — there is no pattern matching against credential-shaped names. Names like `ANTHROPIC_API_KEY`, `GH_TOKEN`, `GITLAB_PRIVATE_TOKEN`, and `KUBECONFIG` are blocked simply because they are not on the list, the same as any other unrecognised variable.

### Allowed variables

| Group | Variables |
|-------|-----------|
| Core | `PATH`, `HOME` (passed through unchanged), `TERM`, `LANG`, `LC_*`, `TZ`, `SSH_AUTH_SOCK`, `EDITOR`, `VISUAL`, `SHELL`, `USER`, `LOGNAME`, `XDG_RUNTIME_DIR` |
| Proxy | `HTTP_PROXY`, `HTTPS_PROXY`, `FTP_PROXY`, `NO_PROXY`, and lowercase `http_proxy`, `https_proxy`, `ftp_proxy`, `no_proxy` |
| TLS trust | `SSL_CERT_FILE`, `SSL_CERT_DIR`, `CURL_CA_BUNDLE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `GIT_SSL_CAINFO` |
| Go | `GOFLAGS`, `GOPROXY`, `GOPRIVATE`, `GOSUMDB`, `GONOSUMDB`, `GOTOOLCHAIN`, `GOPATH`, `GOCACHE`, `GOMODCACHE` |
| Rust | `CARGO_HOME`, `RUSTUP_HOME` |
| Node | `NODE_OPTIONS` |
| Python | `PYTHONPATH`, `VIRTUAL_ENV`, `PYENV_ROOT` |
| Java | `JAVA_HOME` |
| XDG | `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_STATE_HOME` |
| Terminal | `COLORTERM`, `NO_COLOR`, `TERM_PROGRAM` |

Nothing credential-shaped (`*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `*_KEY`, `*_CREDENTIALS`) is on the built-in list. Examples of variables blocked simply for not being on it: `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, `GITLAB_PRIVATE_TOKEN`, `DOCKER_CONFIG`, `KUBECONFIG`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`.

### Extending the allowlist

Two `sandbox` config fields adjust this behaviour:

- `env_passthrough` (`[]string`, default `[]`) — additional variable names allowed through, on top of the built-in list. An entry ending in `*` matches by prefix (e.g. `MYAPP_*` matches `MYAPP_FOO`); no other wildcard forms are supported.
- `env_passthrough_all` (`bool`, default `false`) — disables filtering entirely and passes the full host environment through verbatim, credentials included. **This removes the credential barrier described above.** When enabled alongside `sandbox.enabled: true`, Steiner emits a startup warning making that explicit.

```yaml
sandbox:
  env_passthrough: ["MYAPP_*", "SOME_TOOL_TOKEN"]
  env_passthrough_all: false
```

See [docs/configuration.md](configuration.md) for the full field reference.

Credential config files on disk (e.g., `~/.aws/config`) are readable inside the sandbox through the root bind. The env var allowlist blocks only the environment variable path — it does not prevent the model from reading credential files directly.

## Sandbox boundary prompts

When a sandboxed tool attempts to write to a path outside the workspace, the user is prompted to decide how to proceed. (Reading outside the workspace always succeeds through the read-only root bind.)

### Violation scenario

```
Sandbox boundary violation:
  Tool: bash
  Attempted write: /var/log/app.log
  
Options:
  [A] Allow for this session: add /var/log to host_mounts (rw) and continue
  [U] Use --unsafe: disable sandboxing and re-run the command
  [C] Cancel: abort the command
```

### User decisions

**[A] Allow for this session**:
- Adds the path as a writable mount for the current session
- The command retries inside the sandbox with write access to the path
- The decision is **not** persisted; next session requires a new prompt

**[U] Use --unsafe**:
- Disables sandboxing for the remainder of the session
- The command runs directly on the host without containment
- Future tools also run unsandboxed
- Shown as a banner at the top of the TUI for the session

**[C] Cancel**:
- Aborts the current tool execution
- Returns to the model for a new request

### No prompts in --unsafe mode

If steiner is started with `--unsafe`, boundary violation prompts never appear. All tools run directly on the host.

## Host mounts configuration

All host paths are already readable inside the sandbox through the root bind. Use `host_mounts` to grant **writable** access to paths outside the workspace:

```yaml
# .steiner/config.yaml
host_mounts:
  - path: /var/log
    mode: rw
  - path: /opt/tools
    mode: rw
```

Mounted paths are:
- Available at the same path (e.g., `/var/log` → `/var/log`)
- Mounted at startup (no runtime prompts for configured paths)
- Default mode is `ro` (read-only), which is redundant with the root bind but harmless

**Use cases**:
- CI/CD logs (writable): `host_mounts: [{path: /var/log/ci, mode: rw}]`
- Build output dir: `host_mounts: [{path: /opt/build, mode: rw}]`

## Docker permission

```yaml
# .steiner/config.yaml
permissions:
  docker: true
```

Default is `false`, which **denies** sandboxed access to the Docker daemon. The sandbox binds the host root filesystem read-only (`--ro-bind / /`), but bubblewrap's read-only enforcement does not cover unix sockets, so a plain read-only bind cannot deny Docker access on its own. Instead, when `permissions.docker` is `false`, the sandbox masks every reachable Docker socket (`/run/docker.sock` and, when set, `$XDG_RUNTIME_DIR/docker.sock`) with a bind over `/dev/null`, so `connect()` against the socket fails, and unsets `DOCKER_HOST` so a TCP-endpoint daemon can't be reached either. A socket that doesn't exist on the host is skipped rather than masked — masking a nonexistent destination would abort sandbox startup for every tool, not just Docker ones.

Set `permissions.docker: true` to leave the socket reachable and let sandboxed tools run `docker` against the host daemon.

**Security note**: Docker daemon access is host-root-equivalent — a container can bind-mount `/` and read or write anywhere on the host. Setting `permissions.docker: true` is an explicit opt-in to giving the model that level of access via the `docker` CLI; do not enable it unless you intend the model to have host-root-equivalent control.

**Not covered**: a `docker context` pointing at `ssh://` bypasses this control, since `SSH_AUTH_SOCK` is allowlisted through to the sandbox independently of this setting.

## Error reporting

When a sandbox error occurs, the output includes the denial reason and remediation steps.

### Example: Permission denied

```
$ bash: /opt/tools/script.sh: Permission denied (sandbox boundary)

This tool attempted to access a path outside the workspace:
  Path: /opt/tools/script.sh
  
To allow this access:
  1. Add to host_mounts in .steiner/config.yaml:
     host_mounts:
       - /opt/tools
  2. Re-run the command
  
Or disable sandboxing:
  steiner --unsafe
```

### Debugging sandbox issues

If tools are failing unexpectedly in the sandbox:

1. **Check write target**: Write failures mean the target path is outside the workspace and not in `host_mounts` with `mode: rw`
2. **Check permissions**: The sandbox runs as the same user; file permissions still apply
3. **Check mount layout**: Run `mount` or `ls` inside the sandbox to verify the root bind is active
4. **Use --unsafe**: Temporarily disable sandboxing to isolate whether the issue is sandbox-related
5. **Check bwrap**: Verify `bwrap` is installed and functional:
   ```bash
   which bwrap
   bwrap --version
   ```

### Troubleshooting SSH config ownership failures

If `ssh -G` or another client-only SSH command fails with:

```text
Bad owner or permissions on /etc/ssh/ssh_config.d/...
```

OpenSSH rejected a sandbox-visible include before it could load the overlay safely. Steiner now treats that as a sandbox compatibility failure and can prompt to rerun the command outside the sandbox when needed.

## V2 roadmap

The following features are deferred to V2:

### Session-scoped path grants

**What**: Allow runtime prompts to grant temporary path access for the current session without modifying config.

**Why deferred**: Adds complexity to decision tracking; V1 focuses on static configuration. Runtime session state is orthogonal to the core sandboxing MVP.

**Expected in**: V2 or later

### Audit logging

**What**: Log all tool executions with details about which paths were accessed, what files were read/written, and which operations were denied.

**Why deferred**: Audit logging adds overhead and complexity to error handling. V1 focuses on enforcing boundaries; logging is a follow-on improvement for security teams.

**Expected in**: V2 or later

### macOS seatbelt support

**What**: Bring sandboxing to macOS using Apple's Seatbelt (Security Framework).

**Why deferred**: Seatbelt has a different syntax and capability model than bubblewrap. Supporting both requires duplicated logic. V1 focuses on Linux; macOS support is a platform expansion.

**Expected in**: V2 or later, requires platform-specific implementation

### Windows / WSL2 support

**What**: Enable sandboxing on Windows via WSL2's built-in namespace isolation.

**Why deferred**: Windows/WSL2 has its own container/namespace model. Requires tooling and testing on Windows. V1 is Linux-first.

**Expected in**: V2 or later, requires platform-specific implementation

---

For configuration details, see [docs/configuration.md](configuration.md).

For context management and approval policy, see [docs/sub-agent-delegation.md](sub-agent-delegation.md).
