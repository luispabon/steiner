# Built-in tools

| Tool | Description |
|------|-------------|
| `read` | Read files with offset/limit pagination; line numbers are opt-in with `line_numbers: true`; detects and base64-encodes images. Text files over 1MB are rejected, even when paging, because the whole file is scanned for `file_hash` and `total_lines` (images have a separate 5MB limit). Such files cannot satisfy replace's read-first guard through `read`; replace still accepts an explicit `file_hash` if you compute it yourself (CRC32-IEEE over the file with trailing tabs, spaces and CR trimmed from each line, as 8 uppercase hex digits). |
| `mutate` | Apply structured file mutations atomically: create, write, replace, delete_file, or move |
| `glob` | Find files by pattern |
| `grep` | Search file contents with surrounding context |
| `ls` | List directory contents |
| `bash` | Run shell commands, sandboxed by default |
| `fetch_url` | Fetch a URL and return bounded content or saved image data |
| `display_file` | Show a file in the TUI overlay without adding it to conversation |
| `advisor` | Ask a stronger model for steering guidance; requires `advisor.enabled` |
| `lsp_definitions` | Jump to symbol definitions through a configured language server |
| `lsp_implementations` | Find concrete implementations through a configured language server |
| `lsp_type_definitions` | Jump to a type declaration through a configured language server |
| `lsp_references` | Find references through a configured language server |
| `lsp_diagnostics` | Get diagnostics through a configured language server |
| `lsp_hover` | Get hover information through a configured language server |
| `lsp_symbols` | Search symbols or outline a file through a configured language server |
| `workflow_handoff` | Transition to a workflow with approved artifacts |

MCP tools from connected servers appear with the `mcp__<server>__<tool>` prefix. See [MCP](mcp.md) and [Sub-agent delegation](sub-agent-delegation.md) for availability and approval rules.
