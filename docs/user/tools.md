# Built-in tools

| Tool | Description |
|------|-------------|
| `read` | Read files with offset/limit pagination; line numbers are opt-in with `line_numbers: true`; detects and base64-encodes images. Text files over 1MB are rejected, even when paging, because the whole file is scanned for `file_hash` and `total_lines` (images have a separate 5MB limit). Such files cannot satisfy replace's read-first guard through `read` unless the file was read before it grew past the cap; replace still accepts an explicit `file_hash` if you compute it yourself (CRC32-IEEE over the file with trailing tabs, spaces and CR trimmed from each line, as 8 uppercase hex digits). |
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

## Bash output

`bash` returns the command result in `output`, bounded by `max_output_bytes`. The default is 12288 bytes and the maximum is 32768; larger requests are clamped to the maximum. The limit covers the whole `output` string. A timeout notice that would take more than half the limit is left out of `output` and appears only in `message`.

Cleaned output is the default:

- CRLF becomes LF only where the CR and LF are adjacent. A lone CR is kept.
- ANSI escape sequences are removed. OSC 8 hyperlinks become `text (uri)`, and other OSC sequences are removed.
- Malformed escapes become `[malformed escape: \x1b...]`. The label shows at most 8 raw bytes and states how many were omitted.
- An escape sequence cut off by the capture boundary is dropped.

Set `raw_output: true` to skip this cleaning and pass bytes through without CRLF normalization or escape handling. Raw mode does not guarantee binary fidelity or the interleaving of stdout and stderr.

### Preview shape

- Output that fits is shown whole, with stderr after stdout under a `[stderr]` label.
- When output does not fit, a successful command shows stdout and stderr as one text, cut 30% from the head and 70% from the tail, with a `[... N bytes omitted ...]` marker between them.
- On a nonzero exit, stderr has priority: all of it if it fits, otherwise its own 30%/70% cut. Stdout gets the remaining space.
- `message` states what each stream lost. Omitted rendered bytes and raw source bytes use different units and are never added together.

### Artifacts

When output is cut, the output is saved as a private file at `.steiner/tmp/bash-output/<random>.log` in the working directory. `file_path` gives the path and `artifact_status` gives the outcome:

| Status | Meaning |
|--------|---------|
| `none` | Nothing was cut, so no file was saved. |
| `complete` | The file holds all of the output. |
| `partial` | The file holds some of the output. `message` says what is missing. |
| `failed` | The file could not be saved. The command result is unchanged. |

- The artifact has `[stdout]` and `[stderr]` sections and is capped at 1 MiB (1048576 bytes). Within the cap, the priority stream (stderr on nonzero exit, stdout otherwise) gets 70% of the content space. A stream that is still too large is cut 30% from the head and 70% from the tail.
- Capture keeps the first and last 1 MiB of each stream. Bytes between them are a capture gap and cannot be recovered from the artifact.
- `artifact_limit_hit: true` means the artifact cap dropped bytes that the capture had kept. Steiner then records a nonfatal `bash artifact truncated` warning with the artifact path in the Alt+E dev log. The warning is not added to the conversation view and does not change the command outcome: a nonzero exit stays nonzero, the tool call remains successful, and `artifact_status` still applies.
- Use `read` with `offset` and `limit` to page through an artifact. `read` cannot recover every artifact: it rejects files with a NUL byte in the first 8192 bytes as binary, and it clips lines longer than 2000 characters. For those files, page with `bash`, for example `grep` or `sed -n`.

### Timeouts

A timed-out command returns the output captured so far, with `timed_out: true` and a notice that the output above is partial. If an artifact is saved, its status is `partial` and it notes that the command did not finish normally. A session error is handled the same way and reports `exit_code: -1`.

### Artifact retention and privacy

- `.steiner/tmp/bash-output/` and `.steiner/tmp/fetched/` (fetched URL files) share one cleanup at startup, for the project root and the working directory. Files older than 7 days are removed. If the combined size is still over 250 MiB, the oldest files are removed until it is within that soft budget. Files younger than 1 hour are never removed for size.
- Only regular files directly inside those two directories are considered. Subdirectories and symlinks are ignored.
- An artifact holds the full command output, which may include secrets, tokens, or file contents the command printed. On Unix, the directory is mode 0700 and each file is 0600.
- On platforms without Unix support, artifacts are not saved, so `artifact_status` is `failed`.
- Commands run as your user can read and change artifacts after Steiner writes them. A sandboxed command that can write the workspace can also modify this directory. Do not treat artifacts as tamper-evident records.
- In plan mode, Steiner still writes the artifact cache, but sandboxed commands cannot. See [Execution modes](execution-modes.md#enforcement-matrix) and [Sandboxing](sandboxing.md#bash-output-artifacts).

## Grep output

`grep` pages through logical results. A logical result is a `files_with_matches` path, a `count` row, or a matching line in `content` mode. Context lines do not count.

- `head_limit` sets the page size. It defaults to 100 and is capped at 500.
- Each `content` page renders at most 200 lines. File headers, context lines, separators, and notices count toward that budget, so a content page can hold fewer results than `head_limit`. `files_with_matches` and `count` pages are capped at 200 rows.
- `next_offset` is the first logical result not on this page. To continue, pass it as `offset`. It is omitted on the last page.
- `shown` is the number of results on this page. When the result is paged, `matches` gives the total number of logical results. It is omitted when everything fits on one page.
- An `offset` past the last logical result returns empty `output` with `shown: 0` and `matches` set to the total. It is not an error.
- `files_searched` counts the files read. `skipped_binary`, `skipped_unreadable`, and `skipped_directories` count entries left out of the search.
- If the context for a single match alone exceeds the 200-line budget, the page holds a bounded excerpt around that match, ending with a truncation marker. Pagination still moves forward.
- A rendered line is capped at 400 runes. A clipped line keeps the text around its match column and marks each clipped end with `<truncated>`.

### Matching

- A leading UTF-8 BOM is removed and CRLF becomes LF before matching, so `^`, `$`, and the rendered lines agree on Windows and Unix line endings. A lone CR is kept.
- `multiline: true` compiles the pattern with `(?ms)`: `^` and `$` match line boundaries and `.` matches newlines. Inline flags in the pattern override this, so `(?-m)` restores whole-file anchors and `(?-s)` stops `.` from crossing newlines.
- A zero-width match at end of file reports the final line only when that line holds text. When the file ends with a newline, the match falls on the synthetic empty line after it and no match row is emitted.
- A file with a NUL byte in its first 8192 bytes is treated as binary and skipped.
- An invalid regex, an invalid `glob`, or a `glob` that expands past 256 forms fails the call.

### File selection

`glob` filters paths relative to the search root, written with forward slashes:

- An alternative without a slash matches a file's basename at any depth, so `*.go` matches `pkg/deep/x.go`.
- An alternative with a slash matches that root-relative path only, so `src/*.go` does not match `pkg/src/x.go`.
- A leading `./` anchors the pattern, or just that brace alternative, to the search root: `./*.go` matches top-level files only.
- Every `**/` also matches zero directories, so `pkg/**/x.go` matches `pkg/x.go`.
- Braces, character classes, and escapes use glob syntax. The separate `glob` tool keeps its root-relative `pattern` meaning.
- `type` filters by extension. Values are case-insensitive, `golang` is an alias for `go`, and any other value is rejected. The accepted values are `c`, `cpp`, `css`, `go`, `golang`, `html`, `java`, `js`, `json`, `kotlin`, `md`, `php`, `py`, `rb`, `rust`, `scala`, `sh`, `sql`, `swift`, `ts`, `xml`, and `yaml`.
- A negative `context`, `before_context`, or `after_context` is rejected.
- Entries hidden by `paths.exclude_paths`, `paths.exclude_patterns`, or the built-in directory names are not searched, even when `path` points inside them. See [Configuration](configuration.md#paths-block). Only `paths.blocked_paths` denies access; exclusions are filters.
- A symlinked directory passed as `path` is resolved and validated against the path policy before the walk. Nested directory symlinks are not followed.
