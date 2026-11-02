# How to work with steiner

Steiner supports conversational work, structured skill workflows, and unattended runs. Choose the approach that fits the task and how much oversight you want.

## Plan and build modes

Interactive sessions use one of two execution modes. A mode limits what the agent may change; a workflow is the process it follows. Workflows run within the active mode.

- **`build`** is the default for normal workspace editing. `mutate`, `bash`, and the `code` and `documentor` sub-agents are available, subject to the usual path policy, sandbox, and approval checks. See [Sandboxing](sandboxing.md).
- **`plan`** restricts project writes to `.steiner/plans/` for plans and `.steiner/security/` for security reports. Use it for planning, questions, and discussion.

Modes apply only to interactive sessions. There is no `--mode` flag; `steiner oneshot` and `--exec` runs are unaffected. `--profile` selects model assignments and is unrelated to the mode.

To switch modes:

- Press **Shift+Tab** to toggle. Open overlays take the key first.
- Enter **`/mode`** to toggle, or **`/mode plan`** or **`/mode build`** to set a mode. Other arguments report an error and leave the mode unchanged.
- Invoke **`/plan`** to switch to plan mode. Invoking any other skill, including a custom skill, switches to build mode, except `/security-audit`, which leaves the mode unchanged. See [Skills](skills.md).
- Accept a workflow handoff to switch to build mode. It clears the conversation, can switch models, and starts the next step.

The footer badge and sidebar show the current mode, and a status line confirms switches. Restored sessions keep their saved mode. Set the startup mode with [`modes.default`](configuration.md#modes-block), which defaults to `build`.

### What each mode allows

| Surface | `plan` mode | `build` mode |
|---|---|---|
| `mutate` and other path-writing tools | Denied outside `.steiner/plans/` and `.steiner/security/` | Allowed, subject to path policy and approvals |
| `bash` and config-defined subprocess tools | Project mounted read-only, except the plan-mode writable paths, when the sandbox is active; unrestricted without a sandbox | Allowed, sandboxed as normal |
| MCP tools | Available; a server with approval `allow` asks instead | Available |
| `code` and `documentor` sub-agents | Denied | Allowed |
| `follow_up` to a `code` or `documentor` sub-agent | Denied | Allowed |
| Other sub-agent types (`explore`, `review`, `evaluate`, and so on) | Allowed | Allowed |

The `mutate` and sub-agent restrictions apply with or without a sandbox. The `bash` restriction needs a working sandbox: without one, plan-mode `bash` and subprocess tools can write anywhere the user can, so plan mode is not a filesystem guarantee. Even with the sandbox, plan-mode `bash` can still run git commands that commit or create branches. Treat plan mode as a guard against accidental edits, not as protection from a hostile prompt. See [Sandboxing](sandboxing.md) for the mounts.

The `documentor` sub-agent is denied in plan mode because it has `mutate`, even though its writes are limited to documentation files.

## Default workflow

For everyday work, describe the task in the conversation without invoking a skill. Steiner checks whether you want a plan or an implementation, investigates, then summarizes its understanding and recommends an approach. Discuss and refine it until you approve; Steiner asks before editing.

For implementation, Steiner delegates work to sub-agents in isolated worktrees, in parallel where tasks are independent. It runs checks and a review, then reports what changed. The `sub_agent.enabled` and `sub_agent.orchestration_level` settings affect how much work is delegated. See [Sub-agent delegation](sub-agent-delegation.md).

You can ask for a plan only, or use plan mode while discussing the work. In plan mode, once the requirements settle and you are ready to hand off, Steiner writes `.steiner/plans/<slug>/plan.md` and offers a handoff. Accepting clears the conversation, switches to build mode, and starts a fresh session to implement that file directly.

## Structured workflow

For larger work that needs substantive planning and orchestration, use `/plan`, then `/implement`, then `/review`. These are [skills](skills.md); the skill reference covers their individual behavior.

1. **`/plan`** works through staged approvals for understanding, research, approach, and the overview. It produces `.steiner/plans/YYYY-MM-DD_NAME/overview.md`, recording decisions, scope, and verification strategy, and `plan.yaml`, containing ordered steps. The planning branch is `cl/YYYY-MM-DD_NAME`. Artifacts are committed unless `.steiner/plans/` is gitignored.
2. **`/implement`** carries out the approved steps, delegating work to sub-agents in worktrees and checking changes as they land. It records progress and verification in `execution.md`. Steps marked `no_delegate` can be done directly.
3. **`/review`** checks the result against the plan and for correctness. If there are blocking findings, it presents a consolidated fix plan for your approval. After a passing review, it offers closeout: stop, delete the planning folder, create a PR or MR, or both delete the folder and create a PR or MR. Deletion, pushing, and PR or MR creation require confirmation.

At the end of `/plan` and `/implement`, a handoff modal offers **Accept**, **Change Model**, or **Dismiss**. Accept clears the conversation and starts the next skill on the same folder. Dismiss leaves you to start it yourself, for example with `/clear` followed by `/implement <folder>`. The handoff requires all sub-agents to have finished or been cancelled. Configure per-destination models with [`models.profiles.<profile>.workflow_handoff.<destination>`](configuration.md#models-block).

To resume, select `/implement` or `/review` without an argument to open a picker of plan folders.

The default workflow's plan-mode handoff uses `plan.md`. The structured workflow uses `overview.md` and `plan.yaml`; the files are not interchangeable.

### Simplify and pull requests

`/simplify [base]` is a standalone code-quality analysis of a branch's diff against a base, defaulting to `main`. It needs no planning documents and works independently of the structured workflow, on a branch other than its base. When using it with the structured flow, run it after `/review`: review checks plan adherence and overall correctness, and simplify changes could interfere with that check.

If you plan to simplify, you can stop at review closeout and open the PR afterwards with `/pull-request`, or push the simplify commits to a PR already opened. `/pull-request` works independently of the flow on any branch with commits ahead of its target.

## Oneshot

For a well-specified task you are happy to leave unattended, run `steiner oneshot "<task>"` or enter `/oneshot <task>` in the TUI. Oneshot runs plan, implement, and review phases without approval prompts, each with a fresh context. Only `mutate` path approvals inside its own worktree are auto-approved; other approvals are denied.

The run uses its own worktree and an `oneshot/<slug>-<id>` branch. `oneshot.auto_pr` can open a PR at the end. Use `steiner oneshot --list` and `steiner oneshot --resume <id>` to find and resume interrupted runs. Sub-agents must be enabled. See [Oneshot](oneshot.md) for requirements, closeout, and recovery.

## Choosing a workflow

Use the default workflow for small or medium changes when you want to stay in the conversation. Choose the structured workflow for larger, multi-step work when you want reviewable plan artifacts, recorded decisions, and a fresh context for each phase. Choose oneshot for a well-specified task you are willing to run unattended.

| | Default | Structured | Oneshot |
|---|---|---|---|
| Start | Describe the task | `/plan <task>` | `steiner oneshot "<task>"` or `/oneshot <task>` |
| Planning depth | Conversational, as needed | Staged planning and approvals | Unattended planning phase |
| Artifacts | Optional `plan.md` for plan-mode handoff | `overview.md`, `plan.yaml`, `execution.md` | Run manifest and planning artifacts |
| Approval points | Discuss approach and approve edits | Planning gates, review fixes, and closeout | No prompts; other approval requests are denied |
| Context | Stay in the conversation, or clear on handoff | Clear on accepted phase handoffs | Fresh context per phase |
| End | Report changes, or hand off a plan | Review and optional closeout | Final report and optional PR |
