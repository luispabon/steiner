# LSP code intelligence

Steiner can connect to language servers so the model can navigate code and read diagnostics. LSP is off by default. Set `lsp.enabled: true` and configure at least one server under `lsp.servers`. Field reference: [`lsp` block](configuration.md#lsp-block).

## Tools

| Tool | Purpose |
|------|---------|
| `lsp_definitions` | Jump to a symbol's definition. |
| `lsp_implementations` | Find concrete implementations of an interface or interface method. |
| `lsp_type_definitions` | Jump to the type declaration of a variable, field, or parameter. |
| `lsp_references` | Find references to a symbol, including its declaration unless `include_declaration` is `false`. |
| `lsp_diagnostics` | Get errors, warnings, information, and hints for a file. |
| `lsp_hover` | Get a symbol's signature and documentation, cut at 4000 characters. |
| `lsp_symbols` | Search workspace symbols with `query`, outline a file with `file`, or pass both to filter a file's outline. |

The position tools (`lsp_definitions`, `lsp_implementations`, `lsp_type_definitions`, `lsp_references`, `lsp_hover`) take either a 1-based `line` and `column`, or a `symbol` name optionally narrowed by `line`. Without `line`, an ambiguous symbol returns the candidate lines.

The tools are registered whenever `lsp.enabled` is true. Results are capped by `lsp.max_results` and can be incomplete while a server is still indexing. When no server handles a file, a server failed to start, or a server does not support a request, the tool returns an explanatory message instead of failing.

Some sub-agent types also get these tools when at least one server is configured; see [Sub-agent delegation](sub-agent-delegation.md).

## Server setup

Steiner does not install language servers. Install each one yourself, then point `command` at it. Each file extension should belong to one enabled server.

### gopls (Go)

```yaml
lsp:
  enabled: true
  servers:
    gopls:
      enabled: true
      command: gopls
      file_extensions: [".go"]
      root_markers: ["go.mod", ".git"]
```

Install: `go install golang.org/x/tools/gopls@latest`

### typescript-language-server (TypeScript and JavaScript)

```yaml
lsp:
  enabled: true
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
  enabled: true
  servers:
    pyright:
      enabled: true
      command: pyright-langserver
      args: ["--stdio"]
      file_extensions: [".py"]
      root_markers: ["pyproject.toml", "setup.py", ".git"]
```

Install: `pip install pyright`, or use your package manager.

### rust-analyzer (Rust)

```yaml
lsp:
  enabled: true
  servers:
    rust-analyzer:
      enabled: true
      command: rust-analyzer
      file_extensions: [".rs"]
      root_markers: ["Cargo.toml", ".git"]
```

Install: see the [rust-analyzer manual](https://rust-analyzer.github.io/manual.html#installation).

`initialization_options` is passed to the server as given when it starts.

## Environment and sandbox

Steiner builds the server environment from its own environment and the server's `env` map. When the sandbox is active, it filters that environment, including `env` entries, through the sandbox allowlist. Add other variable names to `sandbox.env_passthrough` to pass them through. The sandbox gives the server a read-only project. See [Sandboxing](sandboxing.md).

## Lifecycle

- A server starts on the first tool call that needs it, waits up to `lsp.ready_timeout` to finish starting, and stops after `lsp.idle_timeout` with no requests in progress. A request that is running, waiting for the server to become ready, or queued behind another request keeps the server alive even past that timeout.
- Each request is bounded by `lsp.request_timeout`.
- A server that crashes is started again on the next call. A server that fails to start is retried after 30 seconds.
- Closing steiner shuts down running servers. A server still starting when shutdown begins is shut down instead of being kept, and a server that stops responding is terminated instead of waited on for a graceful reply. Shutdown is not a hard time limit: when a server's transport stalls outright, closing it can still take longer than the shutdown deadline.
- `/lsp` in the TUI lists running and configured servers. The sidebar shows an LSP row once a server is active: green when ready, a spinner while starting, red after a failure.

## Diagnostics after edits

After a successful `mutate`, Steiner appends diagnostics for the changed files when a matching server is already running and ready. It never starts a server for this, and it adds nothing when there is nothing to report. Only a limited number of changed files is checked per call; use `lsp_diagnostics` or a build for the rest.

## Troubleshooting

Messages the tools return:

- **No language server is configured for `.ext`**: no enabled server lists that extension.
- **No language server is enabled**: `lsp_symbols` workspace search has no enabled server.
- **Language server `<name>` failed to start**: the executable is missing, crashed, or failed to initialise. Check `command`, the install, and that `root_markers` match the project.
- **Language server `<name>` exited unexpectedly**: it crashed during a request. The next call restarts it.
- **Language server `<name>` does not support this request**: the server does not implement that LSP method.

In the TUI, server stderr is discarded unless session logging is configured; in non-interactive runs it goes to stderr. Rename and other LSP features not listed above are not available.
