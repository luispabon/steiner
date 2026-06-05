## Request

GitHub issue: https://github.com/luispabon/steiner/issues/128

Improve interactive UX for workflow handoffs:

- When a planner finishes and writes its handoff, the user should be offered an option to clear context and jump into implement.
- The final handoff text should still be present.
- The same transition should exist from implement to review.

## Overview

The change is repo-local and should add a general workflow handoff primitive. The agreed design is a model-facing built-in tool named `workflow_handoff`.

Initial tool input shape:

```json
{
  "next": "implement",
  "target": ".steiner/plans/2026-06-05_feature",
  "message": "Planning is complete and ready for implementation."
}
```

Fields:

- `next`: required string identifying the next workflow/skill to run. Initial allowed values are Steiner's built-in coding-loop skills: `implement` and `review`.
- `target`: required string passed as the next workflow argument. Initial accepted targets are safe relative `.steiner/plans/...` directories.
- `message`: optional short user-facing modal text.

Tool lifecycle:

1. Model calls `workflow_handoff`.
2. Steiner validates `next`, `target`, and optional `message`.
3. TUI immediately shows an accept/dismiss dialog.
4. If the user accepts, Steiner does not return a tool result to the model. It terminates the current run cleanly, clears conversation/context, enables/loads the `next` workflow, and submits `target`.
5. If the user dismisses, Steiner returns a minimal tool result such as `{"status":"declined"}` and the current model turn may continue.

The tool is a user decision point, not a passive notification and not an execution tool. It never starts the next workflow without user acceptance. Accepting always clears context; there is no "keep context" option and no `clear_context` field.

Existing relevant seams:

- `internal/tui/model_input.go` parses slash commands and skill invocation.
- `internal/tui/model_update_keys.go` opens the `/implement` and `/review` plan picker.
- `internal/tui/model_update.go` already has `clearConversationState()`, which clears TUI state and sends `interactive.ClearConversation`.
- `internal/tui/exit_modal.go` provides an existing modal pattern with selected actions, rendering, key handling, and controller dispatch.
- `internal/interactive/session.go` handles `ClearConversation`, `SubmitPrompt`, skill enablement, and session state reset.
- `internal/tool/builtin` owns built-in tool schemas and handlers.
- `internal/output` owns structured events consumed by terminal and TUI renderers.

Initial validation should be narrow and safe:

- `next` must be one of `implement` or `review`.
- `target` must be relative, under `.steiner/plans/`, and free of traversal, shell metacharacters, newlines, or control characters.
- Target directory must exist.
- Target directory must contain `overview.md` and `plan.yaml`.
- Invalid input should not show a modal.

Built-in skill wording must change alongside the tool:

- `skills/plan/SKILL.md` should keep the existing textual fallback handoff, then call `workflow_handoff` with `next: implement` and `target: .steiner/plans/FEATURE`.
- `skills/implement/SKILL.md` should keep the existing textual fallback handoff, then call `workflow_handoff` with `next: review` and `target: <same planning folder>`.
- `skills/review/SKILL.md` does not need a handoff call for this issue because review is the final coding-loop gate.

Initial modal copy:

```text
Continue to implementation?

Planning folder:
.steiner/plans/2026-06-05_feature

[Accept: Clear + Implement]    [Dismiss]
```

For review, use "Continue to review?" and "Accept: Clear + Review".

Open design details for implementation:

- Decide the exact bridging mechanism between a blocking tool handler and the TUI modal decision. This should likely follow approval-style coordination rather than a passive output-only event.
- Ensure accepted handoff clears retained conversation state so no dangling tool call/result survives.
- Keep `/implement` and `/review` picker behavior unchanged. This feature is an extra fast path after a handoff, not a replacement.
- Keep `workflow_handoff` out of sub-agent allowlists initially. Child agents should not drive parent UI transitions without a separate contract.
- Outside interactive TUI, return an unsupported/declined-style result and rely on textual handoff fallback.

## Verification Strategy

Repository instructions require `make check` before finalizing Go changes. Verification commands discovered from `AGENTS.md` and `Makefile`:

- `gofmt -w <files>`: cheap, required after Go edits.
- `goimports -w <files>`: cheap, required after Go edits.
- `go test ./internal/tui -run <targeted test>`: cheap, first target for TUI modal/input behavior.
- `go test ./internal/interactive -run <targeted test>`: cheap, if controller/session sequencing changes.
- `go test ./...`: medium, broader regression suite.
- `go test -race ./...`: expensive, included in `make check`.
- `go build ./cmd/steiner` or `make build-binaries`: medium, included in `make check`.
- `go vet ./...`: medium, included in `make check`.
- `golangci-lint run ./...`: medium to expensive, included in `make check`, requires installed `golangci-lint`.
- `govulncheck ./...`: medium to expensive, included in `make check`, requires installed `govulncheck`.
- `make check`: required final command; runs tidy, format/import checks, build, tests, race tests, vet, lint, and vuln check.

Recommended executor flow:

- Add focused built-in tool tests for schema, `.steiner/plans/...` validation, and accepted/declined control flow.
- Add TUI tests for immediate modal display, accept clearing/submitting the next workflow, and dismiss returning a declined result.
- Add interactive/session tests for any new action or coordinator introduced to bridge tool calls to UI decisions.
- Run targeted package tests after each behavior slice.
- Run `make check` before final response. If external check tools are missing, report the exact failing command and reason.

## Decision Log

- 2026-06-05: External research skipped with user approval. The task is repo-local and the required behavior is discoverable from local TUI/session code.
- 2026-06-05: Planning branch created: `cl/2026-06-05_128_clear_context_handoff`.
- 2026-06-05: Scope is limited to planning artifacts until overview approval; no implementation files are edited during planning.
- 2026-06-05: Initial preferred direction was a conservative TUI post-handoff offer, but this was rejected because it depends too much on model wording.
- 2026-06-05: Agreed direction is a built-in `workflow_handoff` tool with `next`, `target`, and optional `message`.
- 2026-06-05: Accepted handoff clears context and starts the next workflow without returning a tool result. Dismissed handoff returns a minimal declined result so the current turn can continue.
- 2026-06-05: The modal has only accept and dismiss actions. There is no keep-context path.
- 2026-06-05: Corrected workflow names and target paths to Steiner built-ins: `implement`, `review`, and `.steiner/plans/...`.
