## Request

GitHub issue #141 asks for workflow handoffs to allow model selection. Today, when a planning workflow hands off to implementation, or implementation hands off to review, the next workflow inherits the active session model. That is suboptimal when the next workflow has different cost, speed, context, or reasoning requirements.

The planned feature should let users configure persistent per-workflow handoff model defaults and override the chosen model at handoff time from the interactive dialog.

## Overview

Add a model selection path to interactive workflow handoffs without changing the core handoff purpose: confirm a destructive clear, rotate to a fresh workflow session, and invoke the next skill against the approved planning folder.

The handoff modal should show the model that will be used for the next workflow:

```text
Model: qwen3-coder-480b-a35b-instruct  from handoff default
```

The button row should keep model names out of buttons so long aliases do not distort the modal:

```text
Accept: Clear + Implement    Change Model    Dismiss
```

`Change Model` should reuse the existing model picker behavior in a handoff-specific mode and placement. The picker should be titled for the destination workflow, such as `Select model for implementation`, and should show useful badges where practical, such as `default for implement` and `current session`. Selecting a model updates only the pending handoff selection; it should not switch the active session model until the handoff is accepted.

Persistent defaults should be config-backed. The likely shape is a new workflow handoff config block mapping destination workflow names to model aliases:

```yaml
workflow_handoff:
  models:
    implement: sonnet
    review: opus
```

When opening a handoff, model preselection should follow this order:

1. Configured model alias for the destination workflow, if present and valid.
2. Current session model alias.

On accept, Steiner should submit the workflow handoff decision, switch the session model to the selected alias, clear the conversation, rotate the session, then launch the destination skill with the target planning folder.

## Key Decisions

- Reuse the existing model picker instead of creating a second picker. This keeps filtering, keyboard behavior, and visual language consistent.
- Keep the model name in a body line rather than on an action button. Model aliases can be long, and buttons should remain stable and scannable.
- Include persistent per-workflow defaults in scope. This addresses the core issue that different workflow phases often have predictable model needs.
- Treat user picker selection as a one-off override for the pending handoff, not a config mutation. Runtime selection should not silently rewrite user configuration.
- Keep model choice user/config-owned rather than agent-owned. The current `workflow_handoff` tool schema does not need to let the model request a preferred model unless implementation reveals a stronger reason.

## Tradeoffs

- A modal inline model row with left/right model cycling was rejected because it conflicts with existing action navigation and scales poorly with long model aliases.
- Putting the model alias on a `Model: <alias>` button was rejected because long aliases can bloat or wrap the button row.
- A mandatory two-step model selection flow was rejected because it slows the common path where a configured default or current model is already correct.
- Persisting the last manually selected handoff model was deferred. Config defaults are explicit and predictable; implicit persistence can surprise users.
- Adding model selection to non-interactive handoffs is deferred unless current code paths prove it is needed. The reported issue is about dialog handoffs.

## Scope Boundaries

In scope:

- Add config fields for per-workflow handoff model defaults.
- Validate configured handoff model aliases against `models`.
- Update config patching, defaults, validation, and documentation for the new config block.
- Update the interactive handoff modal to display the pending handoff model and model source.
- Add a `Change Model` action that reuses the existing model picker in a handoff-specific mode or placement.
- Apply the selected model only when the handoff is accepted, before the next workflow run starts.
- Add unit tests around config validation, handoff modal rendering, picker selection/cancel behavior, and accepted handoff launch behavior.

Out of scope:

- Letting the agent choose or suggest a model through the `workflow_handoff` tool schema.
- Persisting one-off picker choices back to config.
- Redesigning slash commands, skill invocation, or the general model picker.
- Changing sub-agent model configuration or delegation model routing.
- Adding provider/model discovery features.

## Verification Strategy

Shallow discovery found these repo-mandated commands:

- Cheap formatter after Go edits: `gofmt -w <files>`
- Cheap import formatter after Go edits: `goimports -w <files>`
- Targeted tests, cheap to medium: `go test ./internal/config ./internal/interactive ./internal/tui ./internal/tool/builtin`
- Specific likely targeted tests, cheap: `go test ./internal/tui -run 'WorkflowHandoff|ModelPicker'`
- Specific likely targeted tests, cheap: `go test ./internal/config -run 'WorkflowHandoff|Validate|Patch'`
- Full tests, medium: `go test ./...`
- Required final check before finalizing Go changes, expensive: `make check`

`make check` currently runs `tidy-check`, `fmt-check`, `imports-check`, `build-binaries`, `test`, `test-race`, `vet`, `lint`, and `vuln`. It depends on `goimports`, `golangci-lint`, and `govulncheck` being installed.

## Decision Log

- 2026-06-12: Issue #141 reviewed. Current handoff carries `next`, `target`, and `message`, while the TUI launch state carries only `next` and `target`; current model switching already exists through `interactive.SwitchModel`.
- 2026-06-12: Research skipped because the work is repo-local and depends on stable local code paths rather than external API behavior.
- 2026-06-12: User chose to include persistent per-workflow config defaults in scope.
- 2026-06-12: UX direction agreed: use Option 5 with a visible model body line, a `Change Model` action, and the existing model picker reused in a handoff-specific position.
