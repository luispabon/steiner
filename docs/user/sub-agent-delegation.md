# Sub-agent delegation

Steiner can hand bounded tasks to isolated child agents. The main agent acts as orchestrator: it writes a brief, dispatches a specialist, then checks and integrates the result. Only the child's compact result returns to the main conversation, which keeps its context small.

Delegation is enabled by default. Turn it off with `sub_agent.enabled: false`; limits such as `max_turns`, `max_tokens`, `max_parallel`, and `max_follow_ups` are listed under [Configuration](configuration.md#sub_agent-block).

The [advisor](advisor.md) is separate: it is a single stronger-model call with no tools, not a child agent.

## Tools

With delegation enabled the main agent gets two tools:

- **`sub_agent`** dispatches a specialist. Its `type` picks the specialist; the brief has `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, and `checks`. `vision` also needs `image_id`. An optional `group` delivers results from the same batch together.
- **`follow_up`** sends a new message to an existing child by `agent_id`, keeping its conversation history.

## Sub-agent types

| Type | Use it to | Edits files |
|---|---|---|
| `explore` | Search the codebase and report files, symbols, and patterns | No |
| `research` | Gather external docs, API details, and examples | No |
| `code` | Implement a scoped change in its own worktree | Yes |
| `documentor` | Write or update documentation in its own worktree | Documentation files only |
| `evaluate` | Analyse a focused sub-problem or compare approaches | No |
| `sanity_check` | Run checks and verify a result | Not intended to |
| `review` | Review implemented changes for bugs, regressions, and missing tests | Not intended to |
| `vision` | Describe or answer questions about a pasted image | No |

`evaluate` is for scoped analysis, not overall task planning. `review` examines changes; it does not plan or implement.

### Tool allowlists

| Type | Tools |
|---|---|
| `explore` | `read`, `glob`, `grep`, `ls`, `bash`, LSP tools† |
| `research` | `read`, `glob`, `grep`, `ls`, `web_search`\*, `fetch_url` |
| `code` | `read`, `glob`, `grep`, `ls`, `mutate`, `bash`, `advisor`, LSP tools† |
| `documentor` | Same as `code`; `mutate` limited to documentation files |
| `evaluate` | `read`, `glob`, `grep`, `ls`, `bash`, `advisor`, LSP tools† |
| `sanity_check` | `read`, `glob`, `grep`, `ls`, `bash` |
| `review` | `read`, `glob`, `grep`, `ls`, `bash`, `advisor`, LSP tools† |
| `vision` | `read` |

\* `web_search` needs a configured search backend (`search.backend`). Without one, the `research` type is not offered to the model.

† `lsp_definitions`, `lsp_implementations`, `lsp_type_definitions`, `lsp_references`, `lsp_diagnostics`, `lsp_hover`, and `lsp_symbols`. They need `lsp.enabled` and a configured server; see [LSP](lsp.md).

`advisor` is present only when the advisor is enabled. MCP tools reach a child only when the server's `sub_agents` list names that type; see [MCP](mcp.md).

Every type except `vision` receives `AGENTS.md`. Project context `extra_files` go only to `code`, `documentor`, `evaluate`, and `review`.

## Safety

- Children cannot delegate: `sub_agent`, `follow_up`, and `workflow_handoff` are never available to them.
- `code` and `documentor` are the only types with `mutate`, and each runs in its own git worktree (see [Worktrees](#worktrees)).
- With the sandbox active, `explore` and `evaluate` run `bash` with the project mounted read-only. `sanity_check` and `review` run `bash` with a writable project in `build` mode; their prompts tell them not to modify files, but that is an instruction, not enforcement. In `plan` mode every child's `bash` gets the read-only project. See [Sandboxing](sandboxing.md).
- In `plan` mode, `code` and `documentor` are denied, and so is `follow_up` to them. See [Execution modes](workflows.md#what-each-mode-allows).
- The child's full transcript is not copied into the main session; only the result is kept.

## Results and follow-ups

A child returns its `output`, a `status`, and a `reason` on failure or cancellation. `code` and `documentor` results also carry `worktree_path`, a project-relative path such as `.steiner/worktrees/<process>/<branch>/<agent-id>`, so the orchestrator can read the worktree directly.

`follow_up` is for continuing the same deliverable in the same live workspace: a related correction by the implementing child, or a narrow re-review by the same reviewer. Use a fresh `sub_agent` when the scope changes, the worktree is gone, or the work needs an independent view.

- Each follow-up runs with a fresh `max_turns` and token budget.
- A single child accepts at most `sub_agent.max_follow_ups` follow-ups.
- A follow-up to a `code` or `documentor` child fails if its worktree has been removed or is on a different branch.
- A follow-up fails while the target is running, queued, or has an undelivered result, and for a child from an earlier Steiner process.
- Each follow-up to a `vision` child re-sends the image, so it costs a full image request.

### Turn budget

`max_turns` is a hard cap per run. The child is reminded at 50%, 75%, and 90% of its budget so it can wrap up; reminders do not extend the budget.

## Background sub-agents

In interactive sessions and oneshot phases, `sub_agent` and `follow_up` return immediately with status `running` (or `queued`) and the `agent_id`. The result arrives later as a separate message, so the main agent can keep working. `--exec` runs wait for each child.

- At most `sub_agent.max_parallel` children run at once (default `3`); further ones queue in order. More than twice that many running plus queued is an error.
- Calls in one response that share a `group` name are delivered together once all have finished. A group name cannot be reused later in the same conversation.
- A failing child does not stop its siblings.
- `workflow_handoff` fails while any child is outstanding.

### In the TUI

- Each child has a card in the transcript; grouped cards share a frame. A "sub-agent(s) finished" row marks each delivery: `✓` complete, `✗` failed, `?` lost on restart.
- The sidebar lists running, queued, and finished children. Click a row to jump to its card.
- `/agents` or `alt+a` opens a filterable picker of all sub-agent cards. `alt+.` and `alt+,` cycle through running children. `alt+/` expands or collapses the card you jumped to for 10 seconds. On macOS, the terminal must send Option as Alt.
- The status bar shows finished and total children, the queued count, and failures.
- A child with no activity for 5 minutes shows `stalled Nm`. This is display-only.
- While waiting, the activity row shows `waiting on N sub-agents`. If results arrive after the main turn failed, a `⚠` row says results are waiting; send a message to continue.

While results are pending, session switching, forking, and `/clear` are refused. The exception: after **Stop current turn**, once every child has finished, `/clear` discards the held results. Model, profile, and mode switches and `/compact` still work.

### Stopping sub-agents

With at least one child active, **Esc**, **Ctrl-C**, or **Ctrl-D** opens the stop dialog instead of interrupting or exiting. It lists each active child by type, ID, and task, and offers:

- **Stop current turn**: shown only while the main agent is generating. Stops the main turn, leaves children running, and holds their results until you send a message.
- **Stop one** (select a child): for `code` and `documentor`, choose **Stop and keep worktree** (default) or **Stop and discard worktree**; for other types, **Stop**.
- **Stop all delegates**: stops every child and keeps all worktrees.
- **Stop entire run**: stops every child and the main turn.
- **Dismiss**: closes the dialog. **Keep working** on a confirmation returns to the list.

Discarding a worktree deletes its directory and branch, including uncommitted and unmerged work, and the child cannot be resumed with `follow_up` afterwards.

## Worktrees

Each `code` and `documentor` child works in its own git worktree, so parallel children never edit the same files. Delegation with these types requires the project to be a git repository.

- **Location**: `.steiner/worktrees/<process-hash>/<branch>/<agent-id>/`, on a branch named `delegate/<process-hash>/<branch>/<agent-id>`.
- **Starting point**: the worktree branches from the project's current `HEAD`. Uncommitted changes in your working tree are not visible to the child; the result includes a warning listing them.
- **Commits**: the child must commit its work in the worktree. The orchestrator then reads the result and integrates it into your branch.
- **Retention**: worktrees stay on disk after the child finishes, fails, or is stopped, so you can inspect them. They are removed only when you ask.

### Cleaning up

When you exit an idle interactive session that created worktrees, Steiner offers `Clean up N worktrees?`. **Exit without cleaning** is the default. **Clean up on exit** removes only this process's worktrees, after the run has stopped; if that takes too long, cleanup is skipped with a warning. **Esc** cancels the exit.

From the command line, run in the project root:

```bash
steiner worktrees --list           # ID, branch, and path of every delegation worktree
steiner worktrees --prune <id>     # remove one worktree by the ID from --list
steiner worktrees --prune-all      # remove every delegation worktree
```

Pass exactly one flag. Pruning force-removes the worktree and deletes its `delegate/...` branch, so uncommitted and unmerged work is lost. Only worktrees on `delegate/` branches are touched; oneshot worktrees are not.

## `documentor` type

`documentor` follows the `code` lifecycle (worktree, commit, `worktree_path`, stop and cleanup options) but its `mutate` writes are limited to:

- files ending in `.md`, `.mdx`, `.markdown`, `.rst`, or `.adoc`
- extensionless files named `README`, `CHANGELOG`, or `CONTRIBUTING`

Matching is case-sensitive on the file name, so `Guide.MD` is denied. The check applies to the path and its symlink target, and a `move` needs both ends in scope. `mutate` does not create parent directories, so the child uses `mkdir -p` first.

The scope covers `mutate` only and is not a security boundary: `bash` and MCP tools are not filtered by it. The child's prompt limits `bash` to read-only inspection, `mkdir -p`, and the git commands `status`, `diff`, `log`, `show`, `add`, `commit`, and `rev-parse`.

Documentation that must ship in the same commit as a code change belongs with the `code` task.

## `vision` type

`vision` is offered only when the selected profile sets `sub_agents.vision` to a vision-capable model. When you paste an image, the TUI shows its ID (for example `img-1`) below your message; the main agent passes it as `image_id`.

Children can also read image files with `read`. The image is visible only on the next model request, so a child re-reads the file if it needs the image again.

## Models

Each type uses the selected profile's `sub_agents.<type>` model, falling back to the profile's `default_model`. See [Provider and model setup](provider-and-model-setup.md).

```yaml
models:
  profiles:
    default:
      default_model: openai/<orchestrator-model-id>
      sub_agents:
        code: openai/<code-model-id>
        evaluate: anthropic/<evaluate-model-id>
        vision: anthropic/<vision-model-id>
```

### Recommended model tiers

These are suggestions; any type can use any model.

| Tier | Types | Why |
|---|---|---|
| Fast | `explore`, `code`, `sanity_check` | Search, scoped changes, and checks; speed matters more than depth. |
| Balanced | `research`, `documentor`, `review`, `vision` | Synthesis and judgement. |
| Strongest | `evaluate` | Deep reasoning on scoped sub-problems. |

## Orchestration level

`sub_agent.orchestration_level` sets how strongly the main agent is steered towards delegating. `standard` (default) tells it to act as orchestrator and delegate by default. `low` drops that steering but keeps the sub-agent roster and briefing guidance, so delegation is at the model's discretion. Tools are the same at both levels.

Change it for the current session with `/orchestration` (picker) or `/orchestration low|standard`. Mid-conversation, Steiner asks for confirmation first because the change invalidates the cached prompt prefix.
