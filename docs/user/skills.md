# Skills

A skill is a Markdown instruction file that gives the agent a workflow for a specific job, such as planning a feature or reviewing a branch. Skills are extra context: they do not override your instructions, tool policy, or project files.

## Bundled skills

| Skill | What it does | When to use it |
|---|---|---|
| `configure` | Inspects and edits Steiner YAML configuration with minimal changes, secret handling, and validation. | You want to understand or change Steiner config. |
| `plan` | Writes a planning bundle (`overview.md`, `plan.yaml`) under `.steiner/plans/YYYY-MM-DD_FEATURE_NAME/`. Plans only; never edits code. | Before implementing a feature, refactor, or migration. |
| `implement` | Executes an approved plan folder step by step, delegating work to isolated sub-agents. | Planning is complete and approved. |
| `review` | Reviews changes against the approved plan, runs approved fix passes, then offers planning-doc cleanup and PR creation. | Implementation is complete. |
| `simplify` | Runs four parallel `explore` sub-agents (reuse, simplification, efficiency, altitude) over the branch's changes against a base branch (default `main`), then offers a fix and review loop. Does not hunt for bugs. | A code-quality pass on any branch; after `/review` when using the plan, implement, review flow. |
| `pull-request` | Detects the Git host, picks a target branch, writes a title and body from the branch commits, and opens a pull or merge request. | The branch is ready to publish. |
| `security-audit` | Static, read-only security audit of a diff or the repository. See [Security audit](security-audit.md). | Before shipping a change, or when assessing unfamiliar code. |
| `fast-iteration` | Trial-and-error mode: edits directly, without delegation or routine confirmation, and skips tests and docs; periodically reminds you that you can say "done", then offers a button-up pass (cleanup, tests, docs, full checks) or a discard of your changes. | You want to try small changes quickly and clean up once the direction works. |

The workflows in `plan`, `implement`, `review`, `simplify`, and `security-audit` dispatch sub-agents, so they work best with delegation enabled. `fast-iteration` is the exception: it edits directly and does not need delegation. See [Sub-agent delegation](sub-agent-delegation.md).

## Using skills

Every skill starts disabled in a new interactive session.

| Command | Effect |
|---|---|
| `/<skill> [args]` | Enables the skill and submits `/<skill> [args]` as your message. Also switches execution mode: `/plan` switches to `plan`, `/security-audit` keeps the current mode, and every other skill switches to `build`. See [Execution modes](working-with-steiner.md#plan-and-build-modes). |
| `/skill <name>` or `/skill +<name>` | Enables a skill without sending a message. |
| `/skill -<name>` | Disables a skill. |
| `/skills` | Lists available skills. |
| `steiner skills` | Lists available skills from the command line. |

Examples:

```text
/plan add retry support to the HTTP client
/implement .steiner/plans/2026-11-01_http-retry
/simplify develop
/security-audit diff
```

Selecting `/implement` or `/review` from the slash menu, or typing either followed by a space, opens a picker of folders under `.steiner/plans/`, newest first. Choosing one appends its path to the composer.

A skill change takes effect with your next message. An enabled skill survives compaction; a disabled skill is dropped at the next compaction. See [Context management](context-management.md).

## Custom skills

A custom skill is a directory containing a `SKILL.md` file. The directory name is the skill name. Steiner looks in these roots, highest precedence first:

1. `<project>/.steiner/skills/<name>/SKILL.md`
2. `~/.config/steiner/skills/<name>/SKILL.md`
3. `~/.agents/skills/<name>/SKILL.md`

When the same name exists in more than one root, the first match wins. Bundled skill names always win: a custom skill with a bundled name is skipped and a warning is logged.

Optional YAML frontmatter supplies the description shown in the slash menu:

```markdown
---
name: release-notes
description: Draft release notes from merged changes since the last tag.
---

# Release notes

Instructions for the agent...
```

Without a `description`, Steiner uses the first line of prose in the file. The skill name always comes from the directory name, not the frontmatter `name`.

A skill body larger than 107520 bytes is truncated with a marker, and Steiner warns with the original size and the cap.
