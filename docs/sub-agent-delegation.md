# Sub-agent delegation

`steiner` exposes eight sub-agent-as-tool operations that delegate bounded tasks to isolated child agents. When delegation is enabled, the parent's system prompt casts it as the orchestrator: its job is to orchestrate sub-agents — it plans the work, chooses the right specialist for each piece, dispatches it with a complete brief, and verifies and integrates its output, preserving its context for orchestration rather than doing the implementation itself.

The parent's preamble spells out a numbered workflow: an initial code-local investigation via `explore`, clarifying questions one at a time, any further research via `research` or `explore`, a Goal/Assumptions/Scope/Unknowns summary for user confirmation, a high-level implementation plan (with `evaluate` for harder, scoped sub-problems), breaking the plan into single-logical-unit implementation steps, then one cold `code` sub-agent per new step. Eligible continuation uses `follow_up` first; related corrections can return to the implementing agent, and narrow re-review can return to the original reviewer, while widened or high-risk review is fresh. Follow-ups require a resumable session, the same bounded deliverable and scope, and the original live workspace; otherwise dispatch cold, and use follow-ups sequentially rather than concurrently. The workflow then ends with `sanity_check`. When the advisor is enabled, the workflow includes an inline step to consult `advisor` and incorporate its feedback (step 7, with the later steps renumbered); the detailed advisor guidance lives in its own `## Advisor` preamble section. Local work is reserved for genuinely self-contained actions — a single bounded lookup, a self-contained formatting action such as running `gofmt`, or a tiny user-directed correction whose exact replacement text or source lines are supplied in the request; everything else is delegated by default.

`advisor` is separate from delegation: it is a stronger-model steering pass over the live parent conversation, with no tools and no child loop. The advisor lives alongside the delegation tools in the main loop, but it is not a child agent. When enabled, the delegation workflow renders an inline step to consult it; the step is omitted when the advisor is disabled.

---

## Available tools

Sub-agent delegation is **enabled by default**. When it is, the model sees eight additional tools alongside the built-in ones:

| Tool        | What it does                                                                     | Structured task fields                                     | Can mutate?            |
|-------------|----------------------------------------------------------------------------------|------------------------------------------------------------|------------------------|
| `explore`   | Navigate the codebase to find files, symbols, call sites, and patterns           | `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, `checks` | No                     |
| `research`  | Gather and synthesise information from the codebase or web                       | `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, `checks` | No                     |
| `code`      | Implement a scoped change — read relevant files, write changes, run tests        | `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, `checks` | Yes (`mutate`, `bash`) |
| `evaluate`    | Analyse a sub-problem, evaluate options, and produce a structured recommendation | `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, `checks` | No                     |
| `sanity_check`| Run tests, linters, builds, or other checks and report pass or fail              | `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, `checks` | No                     |
| `review`      | Examine code changes for bugs, regressions, missing tests, or plan adherence     | `objective`, `context`, `deliverable`, `constraints`, `success_criteria`, `checks` | No                     |
| `vision`    | Analyze an image by ID — the sub-agent receives the image directly               | All six task fields + `image_id`                            | No                     |
| `follow_up` | Resume an existing sub-agent session by agent ID with a new user message         | `agent_id`, `message`                                      | No (resumes existing)  |

The seven specialised tools (`explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, `vision`) are hardcoded with purpose-built system prompts and tool allowlists. Delegation results sent to the provider use a compact envelope with exact `output`, optional failure status/reason, and optional persisted-session continuation. The `follow_up` tool resumes a previously delegated child agent while preserving its conversation history. The parent-only `workflow_handoff` tool creates a handoff request for the current session; it is not exposed to child agents yet.

### Advisor

The `advisor` tool is a pure reasoning pass for the parent agent. It reads the live parent conversation, calls a stronger model, and returns concise strategic guidance. It does **not** expose any tools, does **not** start a child loop, and does **not** mutate state. Its per-run cap is enforced in handler state so the tool definition stays stable for prompt-cache integrity.

### When to use each

| Situation                                              | Tool                                                                                                                             |
|--------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| Find DRY/refactoring opportunities across the codebase | `explore` — report files, repeated patterns, risks, next steps                                                                   |
| Fix a bug but location is unknown                      | `explore` — search likely areas and report exact files/code                                                                      |
| Need to understand an external API or library          | `research` — gather docs, usage examples, and constraints                                                                        |
| Implement a small known change in one package          | `code` — implement if ownership and tests are clear                                                                              |
| Understand how a feature works across multiple files   | `explore` — trace the call chain, then reassess before editing                                                                   |
| Evaluate two approaches to a design problem            | `evaluate` — analyse tradeoffs, then `code` the chosen approach                                                                  |
| Close out a free-form implementation phase             | `review` the cumulative change, use eligible `follow_up` for same-scope amendments or narrow re-review, otherwise cold-dispatch `code` or a fresh independent `review`, then `sanity_check` |
| Review implemented changes before merge                | `review` — examine code for bugs, regressions, missing tests                                                                     |
| Describe or query a pasted image                       | `vision` — the sub-agent receives the image and answers                                                                          |

`evaluate` is for focused sub-problem analysis, **not** overall task planning.

`review` is for examining implemented changes — it does not plan, implement, or verify.

### Context delivery per agent type

Every sub-agent receives AGENTS.md (global + project) except `vision`, which cannot read the repo. Project context `extra_files` are delivered only to `code`, `review`, and `evaluate`; the remaining types skip them to keep the child prompt focused.

| Agent          | AGENTS.md | Project context `extra_files` |
|----------------|-----------|-------------------------------|
| `explore`      | Yes       | No                            |
| `research`     | Yes       | No                            |
| `code`         | Yes       | Yes                           |
| `evaluate`     | Yes       | Yes                           |
| `sanity_check` | Yes       | No                            |
| `review`       | Yes       | Yes                           |
| `vision`       | No        | No                            |

### `follow_up`

The `follow_up` tool lets the parent model send a new user message to an existing child session identified by `agent_id`. This is useful when a sub-agent's initial response leads to follow-up questions or iterative refinement.

Reuse `follow_up` before cold delegation only when all conditions hold: the session is resumable, work remains within the same bounded deliverable and scope, and the original workspace is still live. `session_resumable` alone does not validate the workspace. Cold-dispatch after deleted worktree or isolated cleanup, a workspace or branch change, a material lane or scope change, plan/build incompatibility, or workflow handoff. Follow-ups are sequential, never concurrent. Use the implementing owner for related corrections and the original review agent only for narrow re-review; use fresh agents for new implementation steps and widened or high-risk independent review.

Key behaviours:

- **Preserves conversation** — the child's prior message history is retained and the new message is appended.
- **Resets budget** — each follow-up resets the child's turn and token budgets to the configured defaults (not the remaining budget from the prior run).
- **Tracks follow-ups** — the returned result includes a `follow_up_count` field so the parent can see how many follow-ups have occurred.
- **Auto-approved** — the `follow_up` tool is approval mode `auto` (no user gate).
- **No nesting** — `follow_up` is stripped from child agent registries, so sub-agents cannot follow-up on other sub-agents.

### Parallel fan-out

Multiple delegation calls made in one turn execute concurrently. The fan-out width is bounded independently by `sub_agent.max_parallel` (default `3`, minimum `1`), separate from ordinary parallel-safe tool calls (read/grep/glob/ls/fetch_url/web_search), which are bounded by `limits.max_parallel_tools` (default `4`, minimum `1`) — see [docs/configuration.md](configuration.md#limits-block). A value of `1` runs calls serially. Results are applied to conversation state in the original call order, so completion timing does not change the parent's history. A failing child does not abort its siblings.

### Stopping active delegates

When at least one delegate is active, pressing **Esc**, **Ctrl-C**, or **Ctrl-D** opens the stop dialog. This applies while viewing the conversation, an approval prompt, or another input mode where the shortcut is applicable. If no delegate is active, existing behavior is unchanged: during an active parent run these shortcuts interrupt the whole run, and outside a run they keep their existing exit behavior.

The selector lists each active delegate as **bold, tool-box-coloured type · agent ID · truncated task preview**. It offers these choices:

- **Stop one** — confirm stopping the selected delegate. For a code delegate, the default is **stop and keep worktree**; the confirmation also offers **stop and discard worktree** or **keep working**. For other delegate types, choose **stop** or **keep working**.
- **Stop all delegates** — confirm stopping every active delegate. All code worktrees are retained.
- **Stop entire run** — confirm the existing whole-run interrupt, which also stops its delegates through the parent run context.
- **Dismiss** — close the dialog without stopping anything. **Keep working** on a confirmation screen returns to the selector.

Stopping a delegate does not automatically remove its worktree. A targeted code stop keeps its worktree by default, and stop-all keeps every code worktree. Discard is available only through the explicit targeted discard choice. Discarding a code session makes it non-resumable with `follow_up` and removes its delegation worktree and branch; it is not an automatic cleanup path. A code delegate stopped while waiting for cache warm-up never started, so it has no follow-up session; its empty worktree is retained by default and can be discarded from the stop dialog or removed later.

Every `code` sub-agent automatically runs in its own isolated, runtime-provisioned and verified git worktree under `.steiner/worktrees/`. You do not need to arrange isolation manually anymore. Concurrent child agents are now safe from file-edit collisions.

### Exit-time worktree cleanup

When an interactive TUI session is idle and this process has delegate worktrees, exiting through the exit modal, Ctrl-C, Ctrl-D, or `/exit` opens a `Clean up N worktrees?` offer. `Exit without cleaning` is the default; `Clean up on exit` records cleanup intent. The actual prune runs during session teardown, after the agent run has stopped; if a run is still finishing, cleanup waits only briefly, then is skipped with a non-blocking warning so exiting is never blocked. It only targets this process's worktrees under `.steiner/worktrees/<processhash>/...` (branch prefix `delegate/<processhash>/`), never worktrees from another process. A successful cleanup prints `Cleaned up N worktree(s).` to stderr; failures produce a non-blocking warning. Esc cancels the exit. No offer appears during an active run or when no worktrees exist.

### Safety

- A sub-agent **cannot delegate further** — `follow_up` and `workflow_handoff` tools are always stripped from child registries.
- The parent-only `workflow_handoff` tool is not included in child allowlists yet.
- Only the `code` sub-agent has access to file-mutation tools (`mutate`).
- `explore`, `research`, `evaluate`, and `vision` are read-only.
- `sanity_check` and `review` can run commands via `bash` (for tests, `git diff`, `git log`, etc.) but must not modify files.
- MCP tools are registered from third-party servers and are only exposed to sub-agents when the server's `sub_agents` list explicitly includes the agent type. Approval is per-server and controlled by the parent's configuration.
- All sub-agent tools are automatically approval-gated as `auto` — no manual prompt is needed to use them.
- The child's full conversation transcript is not copied into the parent session; only a structured result and bounded summary persist.
- The result the parent model sees never carries a `trace` field, tool-call counts, or internal file paths. Per-tool-call traces and counters are recorded host-side only (debug log and `.steiner/traces/`, see [docs/sub-agent-delegation-internals.md](sub-agent-delegation-internals.md)) for diagnostics, never sent to the provider.
- While the parent interactive session is in `plan` execution mode, the `code` sub-agent tool is denied outright, and `follow_up` is denied when it targets a session spawned by `code` — both can mutate files, which plan mode disallows. See [docs/execution-modes.md](execution-modes.md) for the full enforcement matrix.
- Two workflows deliberately diverge from the system prompt's `Delegation vs direct work` section, and are labelled as such at their source: `skills/review/SKILL.md` and `skills/simplify/SKILL.md` permit a last-resort inline-fixes tier — looser than the section — for when delegation tooling itself is unavailable; `skills/implement/SKILL.md` and `internal/oneshot/prompts/implement.md` forbid any direct file-mutation tool use on implementation-scoped files — stricter than the section's allowance to apply `mutate` directly to a tiny correction whose exact replacement text or source lines are supplied in the current request, because the executor owns the feature branch and delegation is that workflow's whole point. The oneshot review phase (`internal/oneshot/prompts/review.md`) goes further still and has no inline-fix tier at all.

### Default tool allowlists

| Agent      | Tools available                                             |
|------------|-------------------------------------------------------------|
| `explore`  | `read`, `glob`, `grep`, `ls`, `bash` (read-only project sandbox) |
| `research` | `read`, `glob`, `grep`, `ls`, `web_search`\*, `fetch_url`\* |
| `code`     | `read`, `glob`, `grep`, `ls`, `mutate`, `bash`, `advisor`   |
| `evaluate`    | `read`, `glob`, `grep`, `ls`, `advisor`                     |
| `sanity_check`| `read`, `glob`, `grep`, `ls`, `bash`                        |
| `vision`   | `read`                                                      |
| `review`      | `read`, `glob`, `grep`, `ls`, `bash`, `advisor`             |

\* `fetch_url` is always available. `web_search` requires a configured search backend (Google, Kagi, Brave, or SearXNG). When no backend is configured, the `research` sub-agent is not exposed to the model.

### Extra tools per agent type

Delegation accepts a generic per-agent-type projection of extra allowed tool names, `ExtraAllowedTools`, supplied when the delegate registry is assembled. It is a narrow seam: the delegation package only receives registered tool names, never their origin. Nil or empty projections grant no extra tools, so the built-in allowlists above remain the effective default. Extras merge with the built-in allowlist into a sorted, deduplicated set before child registry construction.

The MCP integration consumes this seam. An MCP server entry's `sub_agents` list names the agent types that may use that server's tools; those tool names are projected into `ExtraAllowedTools` for the listed types. MCP access for children defaults to closed — a server without a matching `sub_agents` entry grants no tools to any sub-agent. When a server is granted to an agent type, the server's tools appear in the child's registry with their `mcp__<server>__<tool>` names. Per-server tool filtering (`allowed_tools` and `blocked_tools`) applies to sub-agents exactly as it does to the parent: a tool excluded by filtering is not exposed to child agents. See [configuration.md](configuration.md) for the MCP config block and [docs/mcp.md](mcp.md) for the full MCP reference.

### Configuration

Sub-agents are configured under the `sub_agent` key in `config.yaml`. Their
model assignments belong to the selected profile:

```yaml
sub_agent:
  # Master switch — set to false to remove all sub-agent tools from the model.
  enabled: true

  # Default limits for all sub-agents (the code applies a floor of 15 turns).
  max_turns: 30
  max_tokens: 100000

models:
  definitions:
    gpt-4o:
      provider: openai
      id: gpt-4o
    claude-sonnet-4:
      provider: anthropic
      id: claude-sonnet-4
    gpt-4o-mini:
      provider: openai
      id: gpt-4o-mini
  profiles:
    default:
      default_model: gpt-4o
      sub_agents:
        code: gpt-4o
        evaluate: claude-sonnet-4
        sanity_check: gpt-4o-mini
```

Each entry under the selected profile's `sub_agents` map, keyed by agent type
name, can set a model alias to any key defined in `models.definitions`. If no
override is set, the sub-agent uses the selected profile's `default_model`.

### Turn budget and extensions

If a child hits `max_turns` mid-work (its last message still has pending tool
calls), it is automatically re-run with a bumped turn budget, up to 3
extensions. Independently of that, once a child crosses roughly 70% of its
current turn budget, it receives a one-time notice in its own conversation
telling it how many turns and extensions remain, so it can wrap up rather than
keep exploring. That notice is updated in place — not appended again — if the
child later gets an extension and crosses the threshold once more.

### Recommended model tiers

Model tier recommendations help choose which model to assign to each agent type.
These are **recommendations, not enforcement** — users configure the selected
profile's `sub_agents.<type>` assignments freely.

| Tier          | Agent types                             | Rationale                                                                 |
|---------------|-----------------------------------------|---------------------------------------------------------------------------|
| **Flash**     | `explore`, `code`, `sanity_check`       | Mechanical tasks — search, implement scoped changes, run checks. Speed matters more than depth. A flash-tier model also works well for the initial `explore` that pre-digests the design during implementation. |
| **Balanced**  | `research`, `review`, `vision`          | Requires synthesis or judgment — gathering information, reviewing code changes, analyzing images. Needs a model that can weigh context and produce nuanced output. |
| **Top-thinker** | `evaluate`                           | Deep reasoning on scoped sub-problems — analysing tradeoffs, evaluating approaches. Benefits most from a stronger model's deliberation capacity. |

### `vision` tool

The `vision` tool requires two parameters:

| Parameter  | Type   | Description |
|------------|--------|-------------|
| `task`     | string | What to analyze or describe about the image. |
| `image_id` | string | The image ID shown in the placeholder (e.g. `img-1`). |

When you paste an image, the TUI displays its assigned ID below the submitted message. Pass that ID to `vision` to examine the image.

After the initial `vision` call, use the `agent_id` inside the returned `continuation` object with `follow_up` to ask additional questions about the same image. The provider's server-side prompt cache makes follow-ups cheap.

The `vision` tool is only registered when the selected profile's `sub_agents.vision`
is configured. It requires a vision-capable model:

```yaml
models:
  definitions:
    claude-sonnet-4:
      provider: anthropic
      id: claude-sonnet-4
  profiles:
    default:
      default_model: claude-sonnet-4
      sub_agents:
        vision: claude-sonnet-4
```

---

For architecture and implementation details, see [Sub-agent Delegation Internals](sub-agent-delegation-internals.md).
