# Tool Sandboxing

Steiner uses Linux container technology (`bubblewrap`) to sandbox tool execution and protect the host filesystem and credentials from model-driven code.

## Overview

By default, `bash` and subprocess tools run inside a **sandbox** — a restricted execution environment with limited filesystem access, a fresh process tree, and no access to host credentials outside the workspace.

The sandbox isolates model-driven code from:
- Host credentials (SSH keys, AWS tokens, GitHub tokens, Kubernetes config, Docker credentials)
- Files outside the workspace
- The full host filesystem

The model can read and write files within the workspace and access specifically-mounted developer tools (SSH, Git, AWS CLI, kubectl, Docker), but cannot access or exfiltrate host credentials or files outside the project boundary.

## Goals and non-goals

### What sandboxing protects

- **Workspace isolation**: Code run by the model cannot access or modify files outside the workspace root
- **Credential protection**: API keys, SSH keys, and other secrets are not exposed to sandboxed code unless explicitly allowed
- **Accidental exfiltration prevention**: Accidentally writing credentials to logs or temp files inside the sandbox cannot affect the host
- **Controlled tool access**: Developer tools (git, ssh, aws, kubectl) are available in the sandbox but only if explicitly mounted

### What sandboxing does NOT protect against

- **Deliberate attacks**: Sandboxing is not a security boundary against intentional malicious code
- **Supply chain attacks**: If you intentionally run untrusted code with steiner, the sandbox cannot prevent all exfiltration
- **Exploits in sandboxing itself**: Bugs in `bubblewrap` or kernel namespace isolation could allow escape (we rely on battle-tested Linux tooling)
- **Malicious model behavior**: Sandboxing assumes the model follows tool schemas; if the model is deliberately trying to exfiltrate data, the sandbox is a speed bump, not a barrier

In short: **sandboxing protects against accidental information leaks and model mistakes, not deliberate attacks.**

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

## Mount layout

The sandbox uses a simplified mount strategy:

1. **Root filesystem**: The entire host filesystem is mounted read-only at `/` using `--ro-bind / /`
2. **Writable overlay**: Only the workspace and sandbox home are bind-mounted at their original host paths as read-write
3. **Working directory**: The initial process working directory is set to the workspace root

This strategy ensures:
- Host paths are preserved inside the sandbox (no path remapping like `/workspace` or `/home/steiner`)
- All system binaries, libraries, and standard utilities are accessible by default
- Only explicitly-mounted paths are writable (workspace, sandbox home)

| Host path | Sandbox path | Mode | Purpose |
|-----------|--------------|------|---------|
| `/` | `/` | ro | Full filesystem read-only base |
| Workspace root | Workspace root | rw | Project files and artifacts |
| `.steiner/home/` | Workspace `/.steiner/home/` | rw | Sandbox-specific home directory |
| `~/.ssh` | `~/.ssh` | ro | SSH keys (auto-mounted if exists) |
| `~/.gitconfig` | `~/.gitconfig` | ro | Git configuration (auto-mounted if exists) |
| `~/.git-credentials` | `~/.git-credentials` | ro | Git credentials (auto-mounted if exists) |
| `~/.config/git` | `~/.config/git` | ro | Git config directory (auto-mounted if exists) |
| `~/.aws` | `~/.aws` | ro | AWS credentials (auto-mounted if `permissions.aws: true`) |
| `~/.kube` | `~/.kube` | ro | Kubernetes config (auto-mounted if `permissions.kube: true`) |
| `~/.docker/config.json` | `~/.docker/config.json` | ro | Docker config (auto-mounted if `permissions.docker: true`) |
| `host_mounts:` | mounted at configured path | ro | Additional paths from config |
| SSH_AUTH_SOCK | SSH_AUTH_SOCK path | ro | SSH agent socket (if set) |
| `/tmp` | `/tmp` | rw | Temporary files (tmpfs) |
| `/proc` | `/proc` | fresh | Process information (ProcFS) |
| `/dev` | `/dev` | minimal | Device nodes (devtmpfs) |

## Developer environment auto-mounts

Steiner auto-mounts common developer credentials and configs from the host home directory so the model can use standard tools. These are mounted at their original paths inside the sandbox (no path remapping):

- **SSH** (`~/.ssh`) — Always mounted. Enables `git clone`, SSH port forwarding, and SSH-based tools.
- **Git config** (`~/.gitconfig`, `~/.git-credentials`, `~/.config/git`) — Always mounted. Enables `git commit`, `git push`, and authenticated Git operations.
- **AWS** (`~/.aws`) — Mounted only if `permissions.aws: true` in config.
- **Kubernetes** (`~/.kube`) — Mounted only if `permissions.kube: true` in config.
- **Docker** (`~/.docker/config.json`) — Mounted only if `permissions.docker: true` in config. Also requires the Docker socket mount (see below).

## Sandbox home

The sandbox workspace includes a `.steiner/home/` directory that serves as an isolated working directory for tool caches and session state within the project. The host `$HOME` remains accessible inside the sandbox at its original path (e.g., `/home/username`), but credentials are mounted read-only.

**Why a separate sandbox home?**
- Standard tools like `git`, `ssh`, and package managers store config and cache in `$HOME`
- Using a workspace-scoped home directory isolates sandbox-generated config (caches, session files) from the host home
- The sandbox home persists across sessions, allowing the model to maintain tool state within the project

**Gitignore**:
`.steiner/home/` is auto-added to `.gitignore` so it doesn't pollute version control.

**What goes in `.steiner/home/`**:
- `.git/` — Git metadata if the model runs `git init`
- `.npm/` — npm cache and config if the model installs packages
- `.ssh/` — SSH config and session keys
- `.bash_history`, `.zsh_history` — Shell history
- Tool caches (`pip`, `cargo`, etc.)

**Clearing the sandbox home**:
If you want to reset the sandbox environment:
```bash
rm -rf .steiner/home/
```

## Environment variable allowlist

Inside the sandbox, only specific environment variables are passed through from the host. This prevents credential leakage via environment variables.

### Allowed variables

| Variable | Purpose |
|----------|---------|
| `PATH` | Command search path |
| `HOME` | Host home directory (passed through unchanged) |
| `TERM` | Terminal type (for ANSI colors) |
| `LANG`, `LC_*` | Locale settings |
| `TZ` | Timezone |
| `SSH_AUTH_SOCK` | SSH agent socket path |
| `EDITOR`, `VISUAL` | Preferred editor |
| `SHELL` | User's shell |
| `USER`, `LOGNAME` | Username |
| `XDG_RUNTIME_DIR` | XDG runtime directory |

### Blocked variables

The following credential and sensitive variables are **never** passed to the sandbox:

- `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, and other AWS secret tokens
- `GH_TOKEN`, `GITHUB_TOKEN` — GitHub personal access tokens
- `GITLAB_PRIVATE_TOKEN` — GitLab tokens
- `DOCKER_CONFIG` — Docker credentials path
- `KUBECONFIG` — Kubernetes config path
- `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` — API keys
- Any variable matching `*_SECRET`, `*_PASSWORD`, `*_TOKEN` (case-insensitive)

If you need to pass a credential to the model, use a config file in the mounted developer environment (e.g., `~/.aws/config`) rather than environment variables.

## Sandbox boundary prompts

When a sandboxed tool attempts to access a file or directory outside the workspace, the user is prompted to decide how to proceed.

### Violation scenario

```
Sandbox boundary violation:
  Tool: bash
  Attempted access: /var/log/app.log (read)
  
Options:
  [A] Allow for this session: add /var/log to host_mounts and continue
  [U] Use --unsafe: disable sandboxing and re-run the command
  [C] Cancel: abort the command
```

### User decisions

**[A] Allow for this session**:
- Adds the path to the sandbox's mount list for the current session
- The path is mounted read-only
- The command retries inside the sandbox with access to the path
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

To permanently allow access to paths outside the workspace, add them to the `host_mounts` config:

```yaml
# .steiner/config.yaml
host_mounts:
  - /var/log
  - /etc/config
  - /opt/tools
```

Mounted paths are:
- Mounted **read-only** inside the sandbox
- Available at the same path (e.g., `/var/log` → `/var/log`)
- Mounted at startup (no runtime prompts for configured paths)

**Use cases**:
- CI/CD logs: `host_mounts: [/var/log/ci]`
- System configuration: `host_mounts: [/etc/app.conf]`
- Shared tools: `host_mounts: [/opt/company-tools]`

## Docker opt-in

To allow the model to run Docker commands inside the sandbox, enable Docker permissions:

```yaml
# .steiner/config.yaml
permissions:
  docker: true
```

When enabled:
- The Docker socket (`/var/run/docker.sock` or equivalent) is bind-mounted into the sandbox
- The `docker` CLI inside the sandbox can communicate with the host Docker daemon
- Containers started by the model run on the host (not nested)

**Security note**: Docker socket access gives the model full control over host containers. Use only if you trust the model completely.

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

1. **Check workspace bounds**: Verify the file is inside the workspace root (or a configured host mount)
2. **Check mount layout**: Run `ls` inside the sandbox to see what's mounted
3. **Check permissions**: The sandbox runs as the same user; file permissions still apply
4. **Use --unsafe**: Temporarily disable sandboxing to isolate whether the issue is sandbox-related
5. **Check bwrap**: Verify `bwrap` is installed and functional:
   ```bash
   which bwrap
   bwrap --version
   ```

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

For configuration details, see [docs/CONFIGURATION.md](CONFIGURATION.md).

For context management and approval policy, see [docs/SUBAGENT_DELEGATION.md](SUBAGENT_DELEGATION.md).
