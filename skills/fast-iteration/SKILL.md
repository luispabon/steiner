---
name: fast-iteration
description: Rapid trial-and-error mode. Make small tweaks directly, without delegation, confirmation, tests or docs, then offer a button-up pass that restores full rigour.
---

# Fast Iteration

The user invoked this skill to iterate quickly through trial and error. The invocation is the user's explicit instruction and, until Button up, takes precedence over these defaults: delegate by default, confirm before editing, review after implementing, the multi-section final-response format, and the project's test, documentation and pre-commit check requirements. Tool policy, approvals, sandbox and safety rules still apply.

Fast iteration stays active until the user exits, including after the history is summarised. Do not drift back to delegating or asking for confirmation. Any summary of the conversation must record that fast iteration is active, the baseline, and the files you have edited.

## Start

1. Record a baseline. In a git repository with commits, note `git rev-parse --short HEAD` and every file `git status --porcelain --untracked-files=all` lists; otherwise note "no git". From now on, track every file you create or edit.
2. Reply in one line: fast iteration is active at baseline `<sha or "no git">`; say "done" to button up (cleanup, tests, docs).
3. Start on the invocation's request, if any; otherwise wait for one.

## Iterate

- Edit files yourself. Delegate only if the user asks, or for a read-only lookup that would cost far more context by hand.
- Make requested tweaks without asking. Ask only when you would likely build the wrong thing.
- Take the quickest change that works over the best technical one. Duplication, hard-coded values and local hacks are fine. Do not refactor.
- Skip tests, docs, full checks, review and advisor passes, and commits. Verify only what the user needs to see the result: a cheap build or run, and the project's formatter on touched files.
- Reply in one or two lines: what changed and where. Always report errors or a failed build.
- Still confirm destructive actions: deleting or overwriting files you did not create, or git commands that discard or rewrite changes.
- Remind the user in one line, for example `Say "done" to button up: cleanup, tests, docs.`, when they seem happy with a result or when none of your last five replies had a reminder. Never remind after a failure.

## Exit

Button up only when the user clearly says they are finished ("done", "button up", "wrap up"), asks for the full pass, or invokes `/plan`, `/implement`, `/review`, `/simplify` or `/pull-request`. Praise, or "that works" followed by another change, is not an exit. Treat narrow requests ("add a test for X", "commit this") as one-offs: do them and keep iterating. If unsure, ask in one line.

## Button up

Restore normal orchestration: from here on, follow the system prompt and project instructions in full.

Summarise what you changed in a few lines. Use your edited-file list plus any paths `git status --porcelain --untracked-files=all` now shows beyond the baseline (formatters and generators change files too), and `git diff <baseline> -- <files>`. If the baseline or file list is missing from the history, say so and ask the user before cleaning up or discarding. Flag files that were dirty at baseline, since their diff includes earlier work, and any commits since the baseline. Then offer:

- **Keep as is**: stop.
- **Clean up properly**: choosing this approves the rework. State a short plan, then replace hacks and duplication in the changed files, add or update tests, update the docs the project's instructions require, and run the project's full checks (from its instructions, build files, scripts or CI config). Report any check you could not run, with the command and reason.
- **Discard**: list the files with a diff stat and get confirmation first. Delete files you created and restore edited files that were clean at baseline with `git checkout <baseline> -- <file>`. Do not revert files that were dirty at baseline or commits since the baseline; tell the user to undo those by hand. Without git, say changes cannot be reverted automatically.
- **Keep iterating**: return to Iterate.

Do not commit unless the user asks.
