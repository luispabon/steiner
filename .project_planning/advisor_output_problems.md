# Advisor Output Problems

## Summary

The `advisor` tool can silently return empty or visually truncated results to the
caller. The handler does not capture or surface `response.FinishReason`, so neither
the TUI nor a reviewer using the `advisor` tool can tell when a response was
clipped by the provider's `max_tokens` budget. A second, distinct failure mode is
the handler returning an empty string when the provider's response content is
empty or whitespace, with no signal that anything went wrong.

These two issues were observed during a live review pass of the
`cl/2026-06-17_advisor_tui_and_skill_gates` branch and are recorded here as a
follow-up.

## Observed Symptoms

### Symptom 1: Body appears truncated inside the labeled block

When the advisor is called, the TUI renders its output as a labeled block:

```
─────────────────────────────────────────────────────────────────────── Advisor output ────────────────────────────────────────────────────────────────────────

   Advisor Note [body text wrapped to terminal width] ...

────────────────────────────────────────────────────────────────────── End of Advisor output ────────────────────────────────────────────────────────────────────
```

The body may end with a trailing `...` or appear cut off mid-sentence at a wrap
boundary. The closing separator is still present, so the visual block is
well-formed. From the user's perspective, the output looks truncated.

Root cause candidates:

1. **Provider `max_tokens` clip.** The Anthropic provider returns
   `finish_reason: max_tokens` (normalized to `"length"`) when the response hits
   the configured output cap. The advisor handler in
   `internal/advisor/tool.go:99` does `strings.TrimSpace(response.Message.Content)`
   and returns whatever the provider sent, including the partial. The user has
   no way to know it was clipped.

2. **TUI wrap artifact.** The body is rendered as a single
   `segmentAssistantMarkdown` or `segmentAssistantProse` segment with no line
   cap. The TUI wraps the segment to the available width. The visible end of the
   body may coincide with a wrap boundary and look like a clip when it is not.

The two are not mutually exclusive: the user can hit a `max_tokens` clip and
then see the already-clipped text wrapped further. Without `FinishReason`
plumbing, the two cases are indistinguishable from outside the handler.

### Symptom 2: Advisor call returns an empty result

A second, distinct failure mode: the advisor call completes with no error, but
the tool result is the empty string. The handler at
`internal/advisor/tool.go:88-101` calls `Advise`, gets a `ChatResponse` with an
empty `Message.Content`, trims it, and returns `""`. The TUI's
`handleAdvisorComplete` checks `if body := strings.TrimSpace(dd.output); body != ""`
and skips the labeled block entirely. The model that called the advisor
receives an empty string back as the tool result and has no signal that the
advisor failed to produce a note.

This is a strictly worse failure than Symptom 1 because the user does not even
see a visible "advisor output" block to read. The advisor call appears to have
succeeded and produced nothing.

## Root Cause

Both symptoms trace to the same gap in `internal/advisor/tool.go`:

```go
response, err := Advise(ctx, Request{
    Provider:     deps.Provider,
    Model:        deps.Model.BackendModelID,
    Conversation: snapshot,
    MaxTokens:    deps.Config.MaxTokens,
})
if err != nil {
    emitEvent(deps.Events, output.NewAdvisorCompleteEvent(deps.Model.BackendModelID, nextUse, maxUses, "", err))
    return nil, err
}

note := strings.TrimSpace(response.Message.Content)
emitEvent(deps.Events, output.NewAdvisorCompleteEvent(deps.Model.BackendModelID, nextUse, maxUses, note, nil))
return note, nil
```

The handler:

- Does not read `response.FinishReason`.
- Does not distinguish a successful stop from a `max_tokens` clip.
- Returns an empty string when the trimmed content is empty, with no error.
- Emits `AdvisorCompleteEvent` with `note=""` in both cases, so the TUI cannot
  show a status that differs from a normal successful call.

The `AdvisorCompleteEvent` payload in `internal/output/` does not currently carry
a `FinishReason` or a `Truncated` flag. Adding one is a precondition for any
fix.

## Recommended Fix

A bounded, single-PR fix that addresses both symptoms:

1. **Capture `FinishReason` in the advisor handler.** Read
   `response.FinishReason` after `Advise` returns. When it is `"length"`, append
   a visible truncation marker to the trimmed note:

   ```
   \n\n[advisor response truncated at max_tokens; raise advisor.max_tokens in config]
   ```

   Emit the marked-up note on the `AdvisorCompleteEvent` so the TUI shows the
   warning inline, inside the same labeled block, and the model that called
   the advisor receives the warning in its tool result.

2. **Surface empty-note results explicitly.** When the trimmed note is empty
   and `FinishReason` is not `"length"`, return an explicit message instead of
   `""`:

   ```
   advisor returned an empty note (finish_reason=stop, content was blank)
   ```

   This is a one-line change in the handler. The TUI's
   `handleAdvisorComplete` will then render the empty-note case as a labeled
   block with the message, which the user can see and act on.

3. **Plumb `FinishReason` through `AdvisorCompleteEvent`.** Add a
   `FinishReason string` field to the event payload in `internal/output/`. The
   TUI does not need to render it directly (the marker in step 1 is the
   user-facing signal), but the field is useful for log-based diagnosis and
   for future tests.

4. **Add tests.**

   - `internal/advisor/tool_test.go`: a test that simulates a `MaxTokens` clip
     and asserts the note contains the truncation marker.
   - `internal/advisor/tool_test.go`: a test that simulates an empty
     `Message.Content` and asserts the handler returns the explicit
     empty-note message rather than `""`.
   - `internal/advisor/advisor_test.go`: assert `FinishReason` flows from
     `ChatResponse` through `Advise` to the caller.

5. **Document the marker and the empty-note message** in
   `docs/ADVISOR_SUBAGENT.md` under a new "Failure Modes" subsection, so users
   know what the marker means when they see it in the TUI.

## Out of Scope for This Report

- Changes to the advisor system or user prompts in
  `internal/advisor/prompt.go`. The prompts are generic and the truncation
  is not caused by verbosity in them; it is caused by the absence of
  `FinishReason` plumbing.
- A default `MaxTokens` for the advisor in `internal/config/defaults.go`.
  This is a separate decision. Users can already set `advisor.max_tokens` in
  their config. Bumping the default is a follow-up if at all.
- TUI viewport-related changes. The TUI renders the full body; any
  viewport clipping is a viewport concern, not an advisor concern.
- Skill loader changes that gate the `plan` and `review` skills on
  `AdvisorEnabled`. Out of scope; the skills' escape-hatch wording handles
  the missing-tool case.

## Verification Strategy

- `gofmt -w` and `goimports -w` on edited files.
- `go test ./internal/advisor/...`
- `go test ./internal/output/...` (event payload changes)
- `go test ./internal/tui/...` (no TUI changes expected, but the marker
  flows through the same render path)
- `go test ./...`
- `make check`
- `make test-race`

## Why This Is a Follow-Up, Not Part of the Original Branch

The original branch `cl/2026-06-17_advisor_tui_and_skill_gates` resolved
GitHub issue #217, which covered four defects:

1. Double tool box around advisor calls.
2. Advisor output body appears truncated.
3. No blank margin after the closing advisor output separator.
4. The `review` skill nudges the reviewer to use the advisor only weakly.

Items 1, 3, and 4 are fully fixed in the original branch. Item 2 was
classified during planning as "viewport clipping on small terminals" and
deferred to a follow-up. The diagnosis was structurally plausible but did not
account for the provider-level `max_tokens` clip path, which is also a real
source of truncation. This report captures that gap and proposes a fix.

The follow-up should ship as a separate change set so that the original
issue's PR remains focused on the four defects it actually resolved.

## Observed Evidence

The empty-result failure (Symptom 2) was reproduced in a live session calling
the `advisor` tool after the original branch was merged locally. The tool
returned an empty string with no error message. The fix in step 2 above would
have surfaced the failure as a labeled block with the explicit empty-note
message instead of an invisible empty tool result.

The truncation observation (Symptom 1) is from a screenshot captured during
the same review pass, showing the advisor output block ending with `...` at a
wrap boundary. The screenshot is not embedded in this report; the diagnosis
above is the structural reading of the handler code combined with the
observation.
