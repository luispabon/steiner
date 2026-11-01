# Sandboxing and approvals

Steiner runs subprocesses inside a Linux `bubblewrap` (`bwrap`) sandbox and checks file paths before tools touch them. When a call crosses one of these boundaries, Steiner asks you before going further. This page covers what the sandbox does, what triggers an approval prompt, and what approving does. Prompt keys are listed in [Interactive mode](interactive.md).

## What the sandbox covers

Sandboxed:

- `bash` commands
- custom tools ([Tools](tools.md))
- locally launched MCP stdio servers ([MCP](mcp.md))
- language servers ([LSP](lsp.md))

Not sandboxed:

- the built-in file tools (`read`, `mutate`, `glob`, `grep`, `ls`, `display_file`), which enforce path policy inside Steiner
- `fetch_url` and `web_search`
- remote HTTP MCP servers, which run on someone else's infrastructure

## What the sandbox is not

The sandbox limits accidental and model-driven writes. It is not a confidentiality boundary:

- The whole host filesystem stays readable, including credential files such as `~/.ssh` and `~/.aws`.
- Network access is shared with the host, so a sandboxed command can send anything it can read.
- The environment allowlist keeps credential variables out, but not credential files on disk.

Treat it as protection against mistakes, not against deliberately malicious code.

## Platform support and fallback

The sandbox works on Linux with `bwrap` on `PATH`. Install it with `sudo apt-get install bubblewrap`, `sudo dnf install bubblewrap`, or `apk add bubblewrap`.

**If the sandbox is unavailable, Steiner does not refuse to run.** On macOS or Windows, or on Linux when `bwrap` is missing or cannot create a namespace, Steiner starts with the sandbox unavailable and runs `bash`, custom tools, MCP stdio servers, and language servers directly on the host. It shows a warning that tools run unsandboxed. Setting `sandbox.warning_on_unsupported_platform: false` hides that warning. On Windows, use WSL2 or a Linux VM to get the sandbox.

Path policy for the built-in file tools, plan-mode `mutate` limits, and MCP approval still apply without the sandbox.

## Turning the sandbox off

```bash
steiner --unsafe
```

`--unsafe`, or `sandbox.enabled: false` in config, runs subprocesses directly on the host for the whole session. Unless `sandbox.warning_on_unsupported_platform` is false, Steiner shows a warning naming the source: `--unsafe`, the project config, or the global config. Without the sandbox there are no sandbox denial prompts. `--unsafe` is independent of project trust (see [Configuration](configuration.md)).

## Filesystem inside the sandbox

- The host root is mounted read-only. Paths keep their host locations.
- The project root is writable in build mode.
- `/tmp` is a session directory under `.steiner/tmp/sandbox-tmp/`. It persists across tool calls and is cleared on `/clear`, `/resume`, `/fork`, and exit.
- The sandbox home, `.steiner/home/`, holds tool state and persists across sessions. Delete it to reset.
- When `~/.cache` exists, a private cache (`.steiner/home/cache/`) is mounted in its place, so sandboxed tools cannot poison the cache used by unsandboxed runs. The first builds are slower. Set `sandbox.bind_host_cache: true` to share the real cache read-write.
- `/dev` is minimal and `/proc` is fresh.
- Docker sockets are masked and `DOCKER_HOST` is unset unless `permissions.docker: true`. Docker access is equivalent to root on the host, so only enable it when you mean to. A `docker context` that uses `ssh://` is not covered.
- MCP stdio servers and language servers always see the project read-only, with the same writable exceptions as plan mode below.

`sandbox.host_mounts` mounts extra paths. Everything is already readable, so use them for writable access outside the project:

```yaml
sandbox:
  host_mounts:
    - path: /var/log
      mode: rw    # default is ro
```

See the [`sandbox` block](configuration.md#sandbox-block) and [`permissions` block](configuration.md#permissions-block).

### Plan mode

In plan mode, sandboxed `bash` and custom tools see the project read-only, except:

- `.steiner/plans/` and `.steiner/security/`, only when they are real directories inside the project and not symlinks
- `.git`, and for a linked worktree its git directory and the shared repository directory, so plan-mode `bash` can stage, commit, and create branches

A symlinked `.steiner`, sandbox home, or sandbox `/tmp` is rejected rather than mounted writable. Steiner still writes saved bash output under `.steiner/tmp/bash-output/` itself, but sandboxed commands cannot write there in plan mode.

Without a working sandbox, plan-mode `bash` and custom tools can write anywhere your user can, so plan mode is not a filesystem guarantee in that state. See [Execution modes](execution-modes.md).

## Environment variables

Sandboxed processes receive only allowlisted host variables:

| Group | Variables |
|-------|-----------|
| Core | `PATH`, `HOME`, `TERM`, `LANG`, `LC_*`, `TZ`, `SSH_AUTH_SOCK`, `EDITOR`, `VISUAL`, `SHELL`, `USER`, `LOGNAME`, `XDG_RUNTIME_DIR` |
| Proxy | `HTTP_PROXY`, `HTTPS_PROXY`, `FTP_PROXY`, `NO_PROXY`, and lowercase `http_proxy`, `https_proxy`, `ftp_proxy`, `no_proxy` |
| TLS trust | `SSL_CERT_FILE`, `SSL_CERT_DIR`, `CURL_CA_BUNDLE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `GIT_SSL_CAINFO` |
| Go | `GOFLAGS`, `GOPROXY`, `GOPRIVATE`, `GOSUMDB`, `GONOSUMDB`, `GOTOOLCHAIN`, `GOPATH`, `GOCACHE`, `GOMODCACHE` |
| Rust | `CARGO_HOME`, `RUSTUP_HOME` |
| Node | `NODE_OPTIONS` |
| Python | `PYTHONPATH`, `VIRTUAL_ENV`, `PYENV_ROOT` |
| Java | `JAVA_HOME` |
| XDG | `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_STATE_HOME` |
| Terminal | `COLORTERM`, `NO_COLOR`, `TERM_PROGRAM` |

Everything else is dropped, including tokens such as `GITHUB_TOKEN`, `AWS_SECRET_ACCESS_KEY`, `ANTHROPIC_API_KEY`, and `OPENAI_API_KEY`. To pass a variable through, add its name to `sandbox.env_passthrough`; a trailing `*` matches a prefix:

```yaml
sandbox:
  env_passthrough: ["MYAPP_*", "SOME_TOOL_TOKEN"]
```

`sandbox.env_passthrough_all: true` passes the entire host environment, credentials included. Steiner warns at startup whenever it is set with the sandbox enabled.

Variables under `mcp.servers.<name>.env` are passed to that MCP server without filtering. Language server `env` values pass through the sandbox filter; see [LSP](lsp.md).

## Approval prompts

Steiner asks for approval in three cases:

| Trigger | What approving does |
|---------|---------------------|
| A sandboxed `bash` command exits nonzero with `Permission denied` or `Operation not permitted` in its output | Runs the same command again **without the sandbox**, directly on the host |
| `mutate` targets a path outside the project root, or `bash` gets a `cwd` outside it, while `paths.project_root_only` is on | Lets that call skip the project-root check; `paths.blocked_paths` and `paths.writable_paths` still apply |
| An MCP tool on a server with approval `ask` | Calls the tool; see [MCP](mcp.md) |

Approving a sandbox denial does not add a writable mount: the retry runs unsandboxed with full host access. In plan mode, and for sub-agents limited to a read-only project, the retry stays in the read-only sandbox instead.

If OpenSSH rejects a config file inside the sandbox (`Bad owner or permissions on ...`), Steiner prompts the same way. Approving reruns the command outside the sandbox.

Denying a sandbox denial returns the original result to the model with a hint to add a `sandbox.host_mounts` entry or use `--unsafe`. Path violations that are not about the project root, such as `paths.blocked_paths`, `paths.writable_paths`, or plan-mode write limits, are denied without a prompt.

### Choices and their scope

The interactive prompt offers **Allow once**, **Allowed for session**, and **Deny**. Approving a path request relaxes the project-root policy, not the sandbox's writable mounts.

- **Allow once** approves this call only.
- **Allowed for session** approves this call and every later prompt of the same kind until Steiner exits. For `bash` and `mutate` the grant covers the whole tool: after allowing a sandbox denial for the session, later `bash` denials retry unsandboxed without asking. For MCP the grant covers one server's tool.
- Grants live in memory only. They are never written to disk and end when Steiner exits.

### Non-interactive runs

- `steiner --exec` reads the answer from the terminal (`/dev/tty`) when one is available, otherwise from stdin. `y` or `yes` approves; anything else, or no input, denies. Each prompt is answered separately.
- [Oneshot](oneshot.md) runs approve `mutate` path prompts inside the run's worktree automatically and deny every other prompt.
- MCP `ask` tools use the same approval input in `--exec`. Oneshot denies calls that require approval. See [MCP](mcp.md).

### Avoiding prompts

There is no per-tool approval setting. To change what prompts, use:

- `sandbox.host_mounts` with `mode: rw` for paths commands need to write
- `paths.project_root_only`, `paths.writable_paths`, and `paths.blocked_paths` for `mutate` and `bash` working directories ([`paths` block](configuration.md#paths-block))
- `permissions.docker` for Docker access
- `mcp.servers.<name>.approval` and `trust_annotations` for MCP tools

## Troubleshooting

- **A write outside the project fails**: add a writable `sandbox.host_mounts` entry, or approve the retry if running it unsandboxed is acceptable.
- **Startup says the sandbox is unavailable on Linux**: check `which bwrap` and `bwrap --version`, then `bwrap --ro-bind / / true` in a host terminal. Some containers and hardened kernels block the user namespaces bwrap needs.
- **Nested sandboxes**: running Steiner, or `bwrap`, inside a sandboxed command can fail even when the host supports it.
- **A tool cannot find a credential**: the variable is probably not on the allowlist. Add it to `sandbox.env_passthrough`.
- **To check whether the sandbox is the cause**, run once with `--unsafe`.
