# LSP-backed code intelligence

Steiner can connect to language servers to answer navigation and diagnostics queries. Servers are configured under `lsp.servers` and are off by default (`lsp.enabled: false`). See [Configuration](configuration.md) for the field reference.

## The seven tools

- **lsp_definitions**: jump to a symbol definition.
- **lsp_implementations**: find concrete implementations of an interface or interface method.
- **lsp_type_definitions**: jump to a type declaration.
- **lsp_references**: find symbol references, including the declaration by default.
- **lsp_diagnostics**: get errors, warnings, information, and hints for a file.
- **lsp_hover**: get a symbol's type signature and documentation, truncated to 4000 characters.
- **lsp_symbols**: search symbols by name or outline a file.

The first five position-addressing tools accept either 1-based, rune-counted `line` and `column`, or a `symbol` name, optionally narrowed by `line`. Explicit `column` takes precedence. Symbol matching uses identifier boundaries. Without `line`, ambiguous matches list candidate lines; with `line`, the leftmost match is used. `lsp_symbols` uses `file` for document mode and `query` without `file` for workspace mode.

Results are limited by `lsp.max_results`. Tools return clear messages rather than errors when no server is configured, a server is disabled or fails to start, or an optional method is unsupported. Results can be incomplete while a server is indexing.

## Language server setup

Steiner does not install or manage language servers. Install each server separately, then configure its executable.

### gopls (Go)

```yaml
lsp:
  servers:
    gopls:
      enabled: true
      command: gopls
      file_extensions: [".go"]
      root_markers: ["go.mod", ".git"]
```

Install: `go install github.com/golang/tools/gopls@latest`

### typescript-language-server (TypeScript / JavaScript)

```yaml
lsp:
  servers:
    tsserver:
      enabled: true
      command: typescript-language-server
      args: ["--stdio"]
      file_extensions: [".ts", ".tsx", ".js", ".jsx"]
      root_markers: ["package.json", "tsconfig.json"]
```

Install: `npm install -g typescript-language-server typescript`

### pyright (Python)

```yaml
lsp:
  servers:
    pyright:
      enabled: true
      command: pyright-langserver
      args: ["--stdio"]
      file_extensions: [".py"]
      root_markers: ["pyproject.toml", "setup.py", ".git"]
```

Install: `pip install pyright` (or use your package manager)

### rust-analyzer (Rust)

```yaml
lsp:
  servers:
    rust-analyzer:
      enabled: true
      command: rust-analyzer
      file_extensions: [".rs"]
      root_markers: ["Cargo.toml", ".git"]
```

Install: [rust-analyzer installation](https://rust-analyzer.github.io/manual.html#installation)

The server inherits Steiner's environment. Add an `env` block only to override variables.

## Lifecycle and status

Servers start lazily on first use, wait up to `lsp.ready_timeout` for readiness, use `lsp.request_timeout` per request, and stop after `lsp.idle_timeout` without requests. If readiness is not signaled in time, the request proceeds with an incomplete flag. Servers without progress events get `lsp.ready_grace_period` to send an initial event.

The sidebar shows an LSP row after a server becomes active. `/lsp` lists live server/workspace sessions and configured servers not yet started. Green means all active servers are ready, a spinner means one is starting, and red means one failed. Idle-stopped servers are inactive. Displays refresh during activity.

Server caches are kept under `<lsp.cache_dir or XDG_CACHE_HOME>/steiner/lsp/<16-char-hash-of-workspace-root>/` and are not cleaned automatically.

## Diagnostics and automatic checks

`lsp_diagnostics` opens a file and waits `lsp.diagnostics_window` (default 2s) for published diagnostics. The full window is always waited. After a successful `mutate`, Steiner may append bounded diagnostics for touched files when a matching server is already running and ready. It never cold-starts a server, emits nothing when there is nothing to report, and has no separate enable switch.

## Limits and troubleshooting

Language servers other than gopls are unverified for cache requirements. Rename and other unlisted LSP features are not implemented. Post-mutate checks cover only touched files up to their per-call cap; use `lsp_diagnostics` or a build for other files.

The standard unavailable messages explain the cause:

- `No language server is configured for .ext. Configure one under lsp.servers to enable this tool.` means no enabled server handles the extension.
- `No language server is enabled. Configure one under lsp.servers to enable this tool.` applies to workspace symbol search when no server is enabled.
- `Language server <name> failed to start: <error>.` means the executable was missing, failed to start, or crashed during initialization.
- `Language server <name> exited unexpectedly.` means a running server crashed during a request.
- `Language server <name> does not support this request.` means an optional method is not implemented by an otherwise healthy server.

If a server fails, check that its executable is installed and its root markers match the project. Readiness can leave results incomplete while indexing. Subprocess stderr is discarded in interactive mode unless a session log is configured, and is sent to process stderr in non-interactive mode.

For timeout calibration, protocol lifecycle, cache path derivation, and test mechanics, see [LSP internals](../internals/lsp.md).
