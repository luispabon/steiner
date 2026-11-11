# Tools

The model works through tools. Steiner owns each tool's input schema and result format, keeps output bounded, and enforces path policy and sandboxing around the calls. `steiner tools` lists the built-in, custom, MCP, and LSP tools for your configuration; it does not list `web_search`, `advisor`, `sub_agent`, or `follow_up`, which are added per run.

## Available tools

| Tool | Purpose | Available | Can ask for approval |
|------|---------|-----------|----------------------|
| `read` | Read a file with offset/limit paging; detects images | Always | No |
| `glob` | List files and directories matching a glob pattern. Directories end in `/`; `limit` and `offset` page results | Always | No |
| `grep` | Search file contents with context and paging | Always | No |
| `bash` | Run a shell command, sandboxed by default | Always | Yes: sandbox denial, or `cwd` outside the project root |
| `display_file` | Show a file in the TUI overlay without adding it to the conversation | Always | No |
| `mutate` | Apply file edits atomically: `create`, `write`, `replace`, `delete_file`, `move` | Always | Yes: a path outside the project root |
| `fetch_url` | Fetch a URL and return bounded content or saved image data | Always | No |
| `workflow_handoff` | Propose switching to a workflow with approved artefacts | Always | Asks you to accept the handoff |
| `web_search` | Search the web | `search.backend` is set | No |
| `lsp_definitions`, `lsp_implementations`, `lsp_type_definitions`, `lsp_references`, `lsp_diagnostics`, `lsp_hover`, `lsp_symbols` | Code intelligence through a language server | `lsp.enabled` | No |
| `advisor` | Ask a stronger model for guidance | `advisor.enabled` | No |
| `sub_agent`, `follow_up` | Delegate work to a child agent, or resume one | Sub-agent delegation enabled (`sub_agent.enabled`) | Child tool calls can ask |
| `mcp__<server>__<tool>` | Tools advertised by MCP servers | `mcp.enabled` and the server is enabled | Yes, except in build mode when the server's `approval` is `allow`, or when trusted annotations mark the tool safe |
| Custom tools | Your own executables declared under `tools:` | Declared in config | No |

See [Sandboxing](sandboxing.md) for what triggers an approval prompt and what approving does, [LSP](lsp.md), [MCP](mcp.md), [Advisor](advisor.md), and [Sub-agent delegation](sub-agent-delegation.md) for the conditional tools, and [Execution modes](working-with-steiner.md#what-each-mode-allows) for plan-mode restrictions.

Sub-agents get a per-type subset of these tools. MCP tools reach children only through `mcp.servers.<name>.sub_agents`.

## Paths and exclusions

`read`, `glob`, `grep`, and `display_file` can read anywhere the filesystem allows, except `paths.blocked_paths`. Writes through `mutate` are limited to the project root while `paths.project_root_only` is on (the default), and to `paths.writable_paths` when that list is set. The `bash` working directory must be inside the project root while `project_root_only` is on and must not be blocked. These tools run inside Steiner, not in the sandbox. See the [`paths` block](configuration.md#paths-block).

`glob` and `grep` skip excluded paths. Exclusions are filters, not access control: only `paths.blocked_paths` denies access.

- Built-in exclusions match any path component named `.git`, `node_modules`, `.steiner`, `.claude`, `.git-worktrees`, `vendor`, `.cache`, `dist`, `build`, `out`, `target`, `.next`, or `.nuxt`. They are matched relative to the search root, so a search rooted inside one of these directories, such as `vendor/`, still runs.
- `paths.exclude_paths` and `paths.exclude_patterns` add workspace exclusions. They still apply when the search root is inside an excluded directory, so that search returns nothing.
- `paths.exclude_patterns` entries are globs matched against the workspace-relative slash path, or against individual path components when the entry holds no `/`. Character classes match within one segment, a segment of exactly `**` matches zero or more segments, and braces form alternations (`src/{gen,out}`) expanded before matching; one entry is capped at 256 expanded forms. A malformed entry, or one that expands past the cap, is ignored while the other entries still apply. In the interactive UI the error log (`alt+e`) reports each ignored entry by its 1-based position in `paths.exclude_patterns` with a fixed reason, then a count of any entries beyond the first eight; the same messages go to the `internal` diagnostics stream when it is enabled ([configuration](configuration.md#diagnostics-block)). Valid patterns print nothing, and the warning never appears in the conversation, tool results, or stdout.
- `grep` follows a nested symlink only when its target is a regular file inside the search root or workspace that also passes `paths.blocked_paths` and the exclusions. Nested directory symlinks are not followed. Results show the link name, not the target.
- `glob` lists entry names and never follows or opens an entry. Every nested symlink and every special file such as a device, pipe, or socket that passes `paths.blocked_paths` is listed; exclusions apply to each entry's own path, so a link whose target is excluded is still listed by name. A symlink is shown without a trailing `/`, so `*/` does not match a directory link, and only a link whose target is blocked is hidden. When `paths.blocked_paths` is set, a link whose target cannot be resolved, such as a dangling link, is hidden as well. Passing a symlink as `path` lists its target.

## Glob patterns

- `pattern` is a glob relative to `path`, which defaults to the current directory. The pattern defaults to `*`, which lists one level.
- A leading `./` is ignored. An empty pattern, `.`, or `./` means the default pattern. A leading `/` or a `..` segment is an error: put the directory in `path` instead.
- A `.` segment anywhere in a pattern, including inside a brace alternative such as `{./a,b}`, is dropped. A brace alternative made only of `.`, such as `{./,a}`, matches nothing, the same as an empty alternative. A dot inside a character class such as `a[/.]` is a class member, not a segment.
- `**` recurses. Patterns match both files and directories, and a matching directory is returned with a trailing `/`: a pattern matches a directory when it matches the directory's path, or when the pattern ends in `/` and matches that path plus the slash. So `src/*` lists the children of `src` without returning `src/` itself, `src/*/` returns only the child directories, and `*/` returns only the top-level directories.
- A result page holds 200 entries by default and at most 1000. Pass the `next_offset` value as `offset` to continue.
- Results are in directory walk order: depth-first, each directory's entries in lexical order. They are not globally sorted.
- Treating each `**/` as matching zero directories multiplies a pattern into several forms. The call fails when that expansion exceeds 256 forms.
- Braces form alternations: `{a,b}` matches `a` or `b`. An unescaped brace with no counterpart is an error, so a literal brace is written `\{` or `\}`, and a brace inside a character class such as `[{]` is literal.

## File reading and editing

- `read` returns line numbers only when called with `line_numbers: true`. Text files over 1 MB are rejected, even when paging; use `grep` or `bash` to inspect them. Images are returned as image data, up to 5 MB.
- `read` has no enforced deadline; a `limits.tool_timeouts.read` entry has no effect. A slow or blocked filesystem operation can delay the call until it is cancelled.
- A file with a NUL byte in its first 8192 bytes is treated as binary: `read` rejects it and `grep` skips it.
- `mutate` applies all operations in a call as one batch. `replace` on an existing file requires that the file was read earlier in the session, or that the call passes the `file_hash` from a `read` result. This catches edits to a file that changed since it was read.
- After a successful `mutate`, Steiner may append diagnostics for the changed files from a language server that is already running. See [LSP](lsp.md).

## Grep paging

`grep` pages through logical results: a file path in `files_with_matches` mode, a row in `count` mode, or a matching line in `content` mode.

- `head_limit` sets the page size: 100 by default, at most 500.
- A `content` page renders at most 200 lines, including context lines and headers, so it can hold fewer results than `head_limit`.
- `next_offset` is the offset of the next page. Pass it as `offset` to continue. It is omitted on the last page.
- Rendered lines are capped at 400 characters around the match.
- `glob` filters by path relative to the search root. A pattern without a slash matches a basename at any depth (`*.go`); a pattern with a slash matches the root-relative path (`src/*.go`); a leading `./` anchors to the root. Braces form alternations, an unescaped brace with no counterpart is an error, and a literal brace is written `\{` or `\}`.
- `type` filters by extension. Accepted values: `c`, `cpp`, `css`, `go` (alias `golang`), `html`, `java`, `js`, `json`, `kotlin`, `md`, `php`, `py`, `rb`, `rust`, `scala`, `sh`, `sql`, `swift`, `ts`, `xml`, `yaml`.
- `multiline: true` lets `.` match newlines and `^`/`$` match line boundaries.
- `grep` enforces `limits.tool_timeouts.grep` (30s by default) cooperatively. When the deadline expires, the search stops at its next check and returns an error instead of partial results. Checks happen between directory entries and before each file is scanned, so CPU-bound regex matching on one file or a blocking filesystem operation can run past the deadline. Sub-agent `grep` calls use the same deadline.

## Bash output

`bash` returns at most `max_output_bytes` of output: 12288 bytes by default, at most 32768. Output is cleaned by default: ANSI escape sequences are removed, OSC 8 hyperlinks become `text (uri)`, and adjacent CRLF becomes LF. Set `raw_output: true` to skip cleaning.

When output does not fit, Steiner keeps the start and end and marks the omitted bytes. On a nonzero exit, stderr gets priority. Steiner attempts to save the captured output, capped at 1 MiB, to `.steiner/tmp/bash-output/` in the working directory. When saved, the result's `file_path` lets the model page through it with `read`. A timed-out command returns the output captured so far.

The per-call `bash` timeout is capped by `limits.tool_timeouts.bash` (600s by default). See the [`limits` block](configuration.md#limits-block).

### Saved output and privacy

- Saved output can contain secrets, tokens, or file contents the command printed. On Unix the directory is mode 0700 and each file 0600. On other platforms output is not saved.
- At startup, Steiner prunes files older than 7 days in `.steiner/tmp/bash-output/` and `.steiner/tmp/fetched/`. It also removes older files towards a shared 250 MiB budget, but does not evict files less than one hour old to meet that budget.
- Commands that can write the workspace can also change these files. Do not treat them as tamper-evident records.

## Fetching URLs

`fetch_url` only connects to publicly routable addresses. Loopback, private, CGNAT, link-local (including cloud metadata at `169.254.169.254`), unique-local IPv6, and reserved ranges are refused, including on every redirect hop. Each request times out after 15 seconds and follows at most 9 redirects.

`fetch_url` ignores `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`, because a proxy would bypass the address check. On a network whose only route out is a proxy, `fetch_url` cannot reach the internet. `web_search` does use the proxy variables.

## Web search

`web_search` returns URL, title, and description results. It is off until you set `search.backend`, and it is registered only while sub-agent delegation is enabled (the default). When available, the `research` sub-agent gets it too.

```yaml
search:
  backend: brave
```

Set `search.backend` to `brave`, `kagi`, or `searxng` and supply that backend's credentials. The unprefixed environment variables fill a key only when the config leaves it empty, and config validation fails if the selected backend has no credentials. Setup for each backend, a local SearXNG example, and troubleshooting are in [Web search](web-search.md); the field reference is the [`search` block](configuration.md#search-block).

## Custom tools

Declare your own executables under `tools:`. Each needs `exec` and a `timeout` greater than zero; names of built-in tools are reserved. `timeout` bounds each call; `limits.tool_timeout_default` and `limits.tool_timeouts` do not apply. Field reference: [`tools` block](configuration.md#tools-block).

```yaml
tools:
  my_tool:
    exec: /path/to/my-tool
    description: Run my custom tool.
    timeout: 10s
```

Steiner runs `exec`, with `subcommand` as its first argument when set, as a subprocess in the project root for every call. The subprocess runs in the sandbox like `bash`, and in plan mode the project is read-only to it. Custom tools never trigger an approval prompt.

### Tool contract

- **stdin**: the model's arguments as one JSON object, written once and then closed.
- **stdout**: one JSON object.
  - Success: `{"ok": true, "result": <any JSON>}`. The model sees `result`.
  - Optional `"model_result": <any JSON>` next to `result`: the model sees `model_result` instead of `result`. Use it when the full result carries more than the model needs.
  - Failure: `{"ok": false, "error": {"kind": "<short slug>", "message": "<human-readable>"}}`.
- **exit code**: exit `0` on every call that produced an envelope. A nonzero exit is always a failure, even with `{"ok": true}` on stdout. Use `ok: false` to report a specific failure.
- **failures**: for an `ok: false` envelope, invalid JSON, a nonzero exit, or a process that failed to start, the model receives the `kind`, the `message`, the exit code, and bounded summaries of stdout and stderr. An `error.details` value in your envelope is not passed to the model. A timeout or cancellation returns an execution error instead.
- Raw stdout and stderr are captured up to `limits.tool_output_max_bytes` and are not sent to the model on success.

```json
// stdin
{"path": "main.go"}

// stdout, success
{"ok": true, "result": {"formatted": true}}

// stdout, success with a smaller model-facing result
{"ok": true, "result": {"formatted": true, "diff": "..."}, "model_result": {"formatted": true}}

// stdout, failure
{"ok": false, "error": {"kind": "invalid_input", "message": "path not found"}}
```
