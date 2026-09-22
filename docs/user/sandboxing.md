# Tool Sandboxing

Steiner uses Linux `bubblewrap` to sandbox tool execution and limit model-driven writes.

## Overview and limits

By default, `bash` and subprocess tools run in a sandbox with a read-only view of the host filesystem and writable access limited to the workspace and sandbox state. The host filesystem remains readable, including credential files. Network access is shared with the host, so sandboxing is not a barrier against deliberate exfiltration or malicious code. The environment allowlist blocks inherited credential variables, but it does not block credential files on disk.

Bubblewrap runs with `--die-with-parent`, so sandboxed processes are killed if Steiner exits or crashes, and `--new-session`, so they cannot open the controlling terminal (`/dev/tty`) to inject input. Locally launched MCP stdio servers and LSP servers use the same wrapper and need no terminal.

The sandbox fails closed: `bwrap` is resolved once at startup and executed by absolute path. If sandboxing is enabled and `bwrap` cannot be resolved, commands are refused with an error rather than run unwrapped. Use `--unsafe` to run without a sandbox deliberately.

Sandboxed `/tmp` is a session-scoped directory under `.steiner/tmp/sandbox-tmp/<id>/`. It persists across tool calls and is cleared on `/clear`, `/resume`, `/fork`, or process exit.

For SSH client-only commands, Steiner can create an ephemeral system-config overlay. Dynamic includes may be skipped. If OpenSSH still rejects the config, Steiner can ask to rerun outside the sandbox.

Built-in Go tools (`read`, `mutate`, `glob`, `grep`, `ls`) are not sandboxed; they enforce path policy in-process. Locally launched MCP stdio servers are sandbox-wrapped. Remote HTTP servers run on their operator's infrastructure and are not affected.

## Standard and unsafe mode

Standard mode is the default:

```bash
steiner
```

Bash and subprocess tools run in the sandbox. A boundary violation prompts:

- **Allow for this session**: add a writable host mount and retry.
- **Use --unsafe**: rerun without sandboxing.
- **Cancel**: abort the command.

Unsafe mode disables the sandbox for the session:

```bash
steiner --unsafe
```

Use it when a tool needs paths outside the workspace or to isolate a sandbox issue. It removes the primary protection and runs commands directly on the host. There are no boundary prompts in unsafe mode. `--unsafe` is independent of [project config trust](configuration.md#project-trust): it never affects, and is never affected by, the trust dialog.

The sandbox-bypass startup warning names its source — `--unsafe`, `sandbox.enabled=false` in the project config, or `sandbox.enabled=false` in the global config — rather than a single generic message.

## Platform requirements

Linux with bubblewrap (`bwrap`) in `$PATH` is supported. Install it with `sudo apt-get install bubblewrap`, `sudo dnf install bubblewrap`, or `apk add bubblewrap` as appropriate.

macOS and Windows do not support sandboxing. Steiner disables it automatically and reports sandbox status `unavailable`; tools use a graceful unsandboxed fallback. Use WSL2 or a Linux VM on Windows. The unsupported-platform warning can be disabled with `sandbox.warning_on_unsupported_platform`.

## Configuration

The entire host filesystem is visible read-only. `sandbox.host_mounts` grants extra paths, with `ro` as the default and `rw` for writes:

```yaml
sandbox:
  host_mounts:
    - path: /var/log
      mode: rw
    - path: /opt/tools
      mode: rw
```

Mounts keep their host paths and are present at startup. All paths are already readable; use mounts for writable access outside the workspace.

The sandbox home is `.steiner/home/`, used for isolated tool caches and state. It persists across sessions and is ignored by git. Remove it with `rm -rf .steiner/home/` when a reset is needed.

### Cache directory

When `~/.cache` exists on the host, the sandbox mounts a private cache directory (`.steiner/home/cache/`) at that path instead of the real one. Tools such as Go, pip, and uv still get a writable cache, but anything they write cannot poison the host cache used by later unsandboxed runs. The private cache starts empty, so first builds are slower. To share the real cache read-write, opt in:

```yaml
sandbox:
  bind_host_cache: true
```

## Mount layout

The host root is read-only. The workspace and sandbox home are writable according to policy, `/dev` is minimal, `/proc` is fresh, and session temporary files are mounted at `/tmp`. Host paths keep their original locations. `sandbox.host_mounts` adds paths at their existing locations.

## Environment variables

Only variables on the built-in allowlist pass from the host. It includes common path, locale, proxy, TLS, Go, Rust, Node, Python, Java, XDG, and terminal variables. Credential-shaped variables are not included. `HOME` is passed through, but the sandbox home is available for tool state.

Extend the list with `env_passthrough`; a trailing `*` is a prefix match. Or set `env_passthrough_all: true` to pass the entire host environment, including credentials. This removes the credential barrier and emits a warning when sandboxing is enabled.

```yaml
sandbox:
  env_passthrough: ["MYAPP_*", "SOME_TOOL_TOKEN"]
  env_passthrough_all: false
```

Server variables under `mcp.servers.<name>.env` are declared configuration and bypass inherited-host filtering.

## Sandbox boundary prompts

When a sandboxed tool attempts to write outside the workspace, Steiner prompts for a decision. Reading outside the workspace succeeds through the read-only root.

- **Allow for this session** adds a writable mount for the current session and retries inside the sandbox. It is not persisted.
- **Use --unsafe** disables sandboxing for the rest of the session, runs the command on the host, and shows a session banner.
- **Cancel** aborts the current execution.

Configured host mounts avoid these prompts. Unsafe mode never shows them.

### Allowed variables

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

Nothing credential-shaped is on the built-in list. For example, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, `GITLAB_PRIVATE_TOKEN`, `DOCKER_CONFIG`, `KUBECONFIG`, `ANTHROPIC_API_KEY`, and `OPENAI_API_KEY` are blocked because they are not listed.

## Docker permission

Docker access is denied by default:

```yaml
permissions:
  docker: true
```

With `false`, reachable Docker sockets are masked and `DOCKER_HOST` is unset. Set `true` only when host-root-equivalent Docker daemon access is intended. A `docker context` using `ssh://` is not covered by this control.

## Troubleshooting

A write outside the workspace fails unless a configured or session-approved writable mount covers it. Reading outside the workspace succeeds through the read-only root. To isolate a problem, check the target and permissions, inspect mounts from inside the sandbox, try `--unsafe`, and verify from the host that `which bwrap` and `bwrap --version` work.

Running `bwrap` inside a sandbox is a nested namespace attempt and can fail even when the host supports sandboxing. Verify with `bwrap --ro-bind / / true` in a host terminal. Inside a session, `cat /proc/1/comm` should print `bwrap`, and `cat /proc/self/uid_map` shows a user-namespace mapping. If startup reports `unavailable`, bwrap is missing or its namespace probe failed.

If `ssh -G` reports `Bad owner or permissions on /etc/ssh/ssh_config.d/...`, OpenSSH rejected an included config visible in the sandbox. Steiner treats this as a compatibility failure and can prompt to rerun outside.

For executor and mount resolution, nested namespace diagnosis, and deferred V2 work, see [Sandboxing internals](../internals/sandboxing.md).
