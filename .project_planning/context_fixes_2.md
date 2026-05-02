# Context Management Cache Fix — Detailed Implementation Plan

## Status

WIP — awaiting implementation.

## Problem

The current prompt assembly produces a system message titled **"retained context state"** that changes on every turn. It contains:

1. **Scratchpad content** — updated every turn via the `scratchpad` tool call
2. **Volatile operational state** — `TurnCount`, `CompactionCount`, `FileTrackerSummary`, `RecentToolCalls`
3. **Retained compaction summaries** — appended each time compaction fires

Because this message sits in the **core/system zone** (before conversation history), every single turn invalidates the KV cache prefix on local inference servers (llama.cpp, LM Studio, Ollama).

## Root Cause

The current implementation (`internal/prompt/source_plan.go`) splits what the design doc calls the **scratchpad** into two separate system messages:

- `durableContextBlock()` — scaffold state (turn count, file tracker, constraints, etc.) as a JSON system message
- `scratchpadBlock()` — model-written scratchpad content as a plain-text system message

Both are placed in `plannedSourcePlacementCore` (the stable zone). This directly contradicts `docs/CONTEXT_MANAGEMENT.md`, which states:

> "The scratchpad is a small block of text (~400-600 tokens) injected as a **user-role message** near the end of each prompt, just before the conversation history's recent turns."

> "**Stable zone** (system prompt only): role, tool rules, project context, scratchpad instructions."

> "**Volatile zone** (messages array): older masked turns, recent turns verbatim, synthetic scratchpad user message, actual user message."

## Target Architecture (per design doc)

```
Stable zone (system prompt, byte-identical across turns):
  ├── system preamble (role, tool rules, scratchpad instructions)
  ├── global AGENTS.md
  ├── project AGENTS.md
  ├── project context files
  └── skills

Volatile zone (messages array, changes every turn):
  ├── older masked turns
  ├── recent verbatim turns
  ├── tool results
  └── synthetic scratchpad user message (LAST message)
```

The **entire** system prompt zone must be byte-identical across turns. The only thing that changes is the messages array.

## Implementation Steps

### Step 1: Remove durable context from core prompt assembly

**File:** `internal/prompt/source_plan.go`

#### 1a. Remove `plannedSourceDurableContext` step

Delete lines 121-132:

```go
{
    Kind:      plannedSourceDurableContext,
    Placement: plannedSourcePlacementCore,
    Apply: func(_ context.Context, state *assemblyState) error {
        if block, ok := durableContextBlock(opts.ContextState, policy.Compaction); ok {
            state.appendBlock(block)
        }
        if block, ok := scratchpadBlock(opts.ContextState, opts.ScratchpadEnabled); ok {
            state.appendBlock(block)
        }
        return nil
    },
},
```

The core zone should now only have: preamble → agents → project context → skills.

#### 1b. Delete `durableContextBlock()` function (lines 179-219)

This function builds the JSON "retained context state" system message. It is no longer needed in the main prompt.

#### 1c. Delete `scratchpadBlock()` function (lines 309-323)

This function builds the scratchpad system message. Scratchpad moves to the volatile zone as a user message.

#### 1d. Delete helper functions only used by the above

- `durableContextSections()` (lines 221-268)
- `compactSessionState()` (lines 270-272)
- `compactDurableContextEntry()` (lines 274-287)
- `compactDurableSummaryEntry()` (lines 289-307)

**Verification:** After this step, `ContextSourceDurableContext` and `ContextSourceScratchpad` should not appear in any `ContextBlock` produced by the main prompt assembly.

---

### Step 2: Build combined scratchpad as user message in agent package

**File:** `internal/agent/message_convert.go`

#### 2a. Add scratchpad assembly helper

Add a new function:

```go
// buildScratchpadMessage creates the synthetic user message that carries both
// scaffold-maintained state and the model's scratchpad content.
// It is injected into the volatile zone (conversation messages), not the system prompt.
func buildScratchpadMessage(state ContextState) (provider.Message, bool) {
    if strings.TrimSpace(state.Scratchpad) == "" && len(state.ActiveConstraints) == 0 &&
        len(state.UnresolvedWork) == 0 && state.ActiveFocus == nil &&
        len(state.FileTrackerSummary) == 0 && len(state.RecentToolCalls) == 0 &&
        state.TurnCount == 0 {
        return provider.Message{}, false
    }

    var parts []string
    parts = append(parts, "[Current task state]")

    if state.TurnCount > 0 || state.CompactionCount > 0 {
        parts = append(parts, fmt.Sprintf("session: turn=%d compactions=%d", state.TurnCount, state.CompactionCount))
    }

    if len(state.ActiveConstraints) > 0 {
        lines := []string{"active constraints:"}
        for _, c := range state.ActiveConstraints {
            lines = append(lines, "- "+c.Text)
        }
        parts = append(parts, strings.Join(lines, "\n"))
    }

    if len(state.UnresolvedWork) > 0 {
        lines := []string{"unresolved work:"}
        for _, w := range state.UnresolvedWork {
            lines = append(lines, "- "+w.Text)
        }
        parts = append(parts, strings.Join(lines, "\n"))
    }

    if state.ActiveFocus != nil && strings.TrimSpace(state.ActiveFocus.Text) != "" {
        parts = append(parts, "active focus:\n- "+state.ActiveFocus.Text)
    }

    if len(state.FileTrackerSummary) > 0 {
        parts = append(parts, "tracked files:\n- "+strings.Join(state.FileTrackerSummary, "\n- "))
    }

    if len(state.RecentToolCalls) > 0 {
        parts = append(parts, "recent tool calls:\n- "+strings.Join(state.RecentToolCalls, "\n- "))
    }

    scratchpad := strings.TrimSpace(state.Scratchpad)
    if scratchpad != "" {
        parts = append(parts, scratchpad)
    } else {
        parts = append(parts, "goal: \nplan: \nstep: \ndecisions: \nfiles: \nopen: \nnext: ")
    }

    return provider.Message{
        Role:    provider.MessageRoleUser,
        Content: strings.Join(parts, "\n\n"),
    }, true
}
```

#### 2b. Modify `assemblyOptions()` to inject scratchpad

Replace the current `assemblyOptions()` (lines 78-88):

```go
func assemblyOptions(base prompt.AssemblyOptions, state RunState) prompt.AssemblyOptions {
    conversation := state.Lineage.SummaryPrefixStrippedMessages()
    if len(conversation) == 0 {
        conversation = state.Conversation
    }

    providerMsgs := toProviderMessages(conversation)

    // Inject combined scratchpad as synthetic user message in volatile zone
    if scratchpadMsg, ok := buildScratchpadMessage(state.Context); ok {
        providerMsgs = append(providerMsgs, scratchpadMsg)
    }

    base.Conversation = providerMsgs
    base.ToolResults = nil
    base.ContextState = toPromptContext(state.Context)
    base.ScratchpadEnabled = base.ScratchpadEnabled || strings.TrimSpace(state.Context.Scratchpad) != ""
    return base
}
```

**Key decisions:**
- Scratchpad is appended as the **last message** in the conversation. This is the simplest correct placement per the design doc ("near the end"). It appears after all conversation history, providing fresh task state right before the model responds.
- We use `SummaryPrefixStrippedMessages()` to get the raw conversation without compaction summaries, then inject scratchpad, and rely on the fact that compaction summaries are already in the lineage's `SummaryPrefix` and get rendered separately. Wait — this needs careful handling. See Step 3.

---

### Step 3: Handle compaction summaries in conversation lineage

**Current behavior:** `SummaryPrefixStrippedMessages()` strips compaction summaries from the conversation before assembly. The summaries then get re-injected via `durableContextBlock()` → `RetainedSummaries`. We are removing that path.

**New behavior:** Compaction summaries should remain in the conversation as system messages. They are part of the conversation history, not part of the scratchpad.

#### 3a. Use `FullMessages()` instead of `SummaryPrefixStrippedMessages()`

In `assemblyOptions()` (Step 2b), change:

```go
conversation := state.Lineage.SummaryPrefixStrippedMessages()
```

To:

```go
conversation := state.Lineage.FullMessages()
```

This includes the `SummaryPrefix` (compaction summaries) as the first messages in the generation. They are system messages (`MessageRoleSummary` maps to `provider.MessageRoleSystem` in `toProviderMessage()`).

**Rationale:** Compaction summaries are part of the conversation history. They belong in the volatile zone. They change only when compaction fires (infrequent), so they don't cause per-turn cache invalidation. When they do change, it's acceptable to invalidate cache because the conversation itself has fundamentally changed.

#### 3b. Verify compaction summary rendering

In `internal/agent/message_convert.go`, `toProviderMessage()` maps `MessageRoleSummary` to `provider.MessageRoleSystem`:

```go
if message.Role == MessageRoleSummary {
    role = provider.MessageRoleSystem
}
```

This is correct. Compaction summaries will appear as system messages within the conversation array.

#### 3c. Remove `SummaryPrefixStrippedMessages()` usage from all prompt assembly paths

Search for `SummaryPrefixStrippedMessages` and verify it's not used elsewhere for prompt assembly:

- `internal/agent/context_manager.go:102` — `next.Lineage.SummaryPrefixStrippedMessages()` in `PreAssembly()`
- `internal/agent/message_convert.go:79` — in `assemblyOptions()` (fixed above)

In `context_manager.go`, `PreAssembly()` uses it to get the conversation for masking:

```go
conversation := next.Lineage.SummaryPrefixStrippedMessages()
```

This is fine for masking (we don't want to mask compaction summaries), but the masked result is then stored back into the lineage. The assembly step should use `FullMessages()` to include summaries in the final prompt.

Wait, let me trace more carefully:

1. `PreAssembly()` gets `SummaryPrefixStrippedMessages()`, masks them, stores masked as current messages
2. `assemblyOptions()` reads from lineage — if we use `FullMessages()`, we get SummaryPrefix + current messages

This is correct. The masking operates on the raw messages (without summaries), and the assembly combines summaries + masked messages.

---

### Step 4: Strip `prompt.DurableContextState` to essentials

**File:** `internal/prompt/types.go`

#### 4a. Remove unused fields from `DurableContextState`

Currently:

```go
type DurableContextState struct {
    ActiveConstraints  []DurableContextEntry `json:"active_constraints,omitempty"`
    UnresolvedWork     []DurableContextEntry `json:"unresolved_work,omitempty"`
    ActiveFocus        *DurableContextEntry  `json:"active_focus,omitempty"`
    RetainedSummaries  []DurableSummaryEntry `json:"retained_summaries,omitempty"`
    FileTrackerSummary []string              `json:"file_tracker_summary,omitempty"`
    RecentToolCalls    []string              `json:"recent_tool_calls,omitempty"`
    TurnCount          int                   `json:"turn_count,omitempty"`
    CompactionCount    int                   `json:"compaction_count,omitempty"`
    Scratchpad         string                `json:"scratchpad,omitempty"`
}
```

After the main prompt stops using durable context, only `RetainedSummaries` is still needed (for compaction prompts). Remove everything else:

```go
type DurableContextState struct {
    RetainedSummaries []DurableSummaryEntry `json:"retained_summaries,omitempty"`
}
```

#### 4b. Keep `DurableContextEntry` and `DurableSummaryEntry` types

These are still used for `RetainedSummaries` and potentially for compaction prompt context.

---

### Step 5: Update conversion functions

**File:** `internal/agent/message_convert.go`

#### 5a. Simplify `toPromptContext()`

Currently copies many fields. After stripping `DurableContextState`, it becomes:

```go
func toPromptContext(state ContextState) prompt.DurableContextState {
    out := prompt.DurableContextState{
        RetainedSummaries: make([]prompt.DurableSummaryEntry, 0, len(state.RetainedSummaries)),
    }
    for _, item := range state.RetainedSummaries {
        out.RetainedSummaries = append(out.RetainedSummaries, prompt.DurableSummaryEntry{
            Title:  item.Title,
            Text:   item.Text,
            Source: item.Source,
            Turn:   item.Turn,
        })
    }
    return out
}
```

#### 5b. Simplify `fromPromptContext()`

```go
func fromPromptContext(state prompt.DurableContextState) ContextState {
    out := ContextState{
        RetainedSummaries: make([]RetainedSummary, 0, len(state.RetainedSummaries)),
    }
    for _, item := range state.RetainedSummaries {
        out.RetainedSummaries = append(out.RetainedSummaries, RetainedSummary{
            Title:  item.Title,
            Text:   item.Text,
            Source: item.Source,
            Turn:   item.Turn,
        })
    }
    return out
}
```

---

### Step 6: Update budget tracking

**File:** `internal/prompt/budget.go`

#### 6a. Remove `ContextSourceDurableContext` and `ContextSourceScratchpad` from budget tracking

In `limitFor()` (lines 95-118), remove:

```go
case ContextSourceDurableContext:
    return m.DurableContextBytes
case ContextSourceScratchpad:
    return m.DurableContextBytes
```

In `newBudgetTracker()` (lines 131-144), remove:

```go
ContextSourceDurableContext:   model.DurableContextBytes,
ContextSourceScratchpad:       model.DurableContextBytes,
```

**Rationale:** These sources no longer participate in block-level budgeting. Scratchpad is a message, not a block. Durable context no longer exists in the main prompt.

#### 6b. Consider removing `DurableContextBytes` from `SourceBudgetModel`

If `DurableContextBytes` is no longer used anywhere, remove it from `SourceBudgetModel` and all callers. Search for usages first.

---

### Step 7: Update zone logging

**File:** `internal/agent/turn_progression.go`

#### 7a. Remove durable context and scratchpad from system byte counting

Lines 256-267 currently classify blocks into zones:

```go
systemBytes, scratchpadBytes, conversationBytes := 0, 0, 0
for _, block := range assembly.Blocks {
    switch block.Source {
    case prompt.ContextSourcePreamble, prompt.ContextSourceGlobalAgentsMD, prompt.ContextSourceProjectAgentsMD,
        prompt.ContextSourceDurableContext, prompt.ContextSourceConversationSummary:
        systemBytes += block.ByteSize
    case prompt.ContextSourceScratchpad:
        scratchpadBytes += block.ByteSize
    default:
        conversationBytes += block.ByteSize
    }
}
```

Update to:

```go
systemBytes, conversationBytes := 0, 0
for _, block := range assembly.Blocks {
    switch block.Source {
    case prompt.ContextSourcePreamble, prompt.ContextSourceGlobalAgentsMD, prompt.ContextSourceProjectAgentsMD,
        prompt.ContextSourceConversationSummary:
        systemBytes += block.ByteSize
    default:
        conversationBytes += block.ByteSize
    }
}
```

And update the slog call:

```go
slog.Debug("prompt zones", "turn", turn, "system_bytes", systemBytes, "conversation_bytes", conversationBytes)
```

**Note:** The scratchpad is now a message in `assembly.Messages`, not a block in `assembly.Blocks`. Its size is implicitly included in the conversation messages size. If we want explicit scratchpad byte counting, we'd need to measure the last message when it's identified as scratchpad. This is optional; the debug log's purpose is cache-busting visibility, and we can already see if system_bytes is stable.

---

### Step 8: Update `ContextSource` constants and rendering

**Files:** `internal/prompt/types.go`, `internal/prompt/source_render.go`

#### 8a. Remove unused source constants

From `types.go`, remove:

```go
ContextSourceDurableContext      ContextSource = "durable_context"
ContextSourceScratchpad          ContextSource = "scratchpad"
```

**Wait — keep `ContextSourceDurableContext` if it's still used in compaction prompts.** Let me check.

In `internal/prompt/compaction.go`:
```go
if durable := durableContextSections(state); len(durable) > 0 {
```

This uses `prompt.DurableContextState` directly, not `ContextSource`. The `ContextSource` constants are for block categorization in the main prompt assembly. If durable context and scratchpad blocks no longer exist in main assembly, these constants can be removed.

But `ContextSourceConversationSummary` is still used. Keep it.

#### 8b. Update `blockMessage()` in `source_render.go`

Remove cases for `ContextSourceDurableContext` and `ContextSourceScratchpad`:

```go
case ContextSourceDurableContext:
    message.Role = provider.MessageRoleSystem
```

These cases are now dead code since those blocks are no longer produced.

---

### Step 9: Update compaction prompt to use `DurableContextState`

**File:** `internal/prompt/compaction.go`

#### 9a. Verify `BuildConversationCompactionPrompt` still works

```go
func BuildConversationCompactionPrompt(messages []provider.Message, state DurableContextState, override string) []provider.Message {
```

This function takes `DurableContextState` and renders durable context sections into the compaction prompt. After stripping `DurableContextState`, only `RetainedSummaries` remains.

Check `renderConversationCompactionSource()` (line 155):

```go
if durable := durableContextSections(state); len(durable) > 0 {
    sections = append(sections, "durable context:", strings.Join(durable, "\n\n"))
}
```

After stripping `DurableContextState`, `durableContextSections()` needs to be updated. Currently it's in `source_plan.go` and we planned to delete it in Step 1.

**Decision:** Move `durableContextSections()` (or a simplified version) to `compaction.go` since it's only needed for compaction prompts now. It should only render `RetainedSummaries`.

```go
func durableContextSections(state DurableContextState) []string {
    if len(state.RetainedSummaries) == 0 {
        return nil
    }
    lines := make([]string, 0, len(state.RetainedSummaries)+1)
    lines = append(lines, "retained summaries:")
    for _, item := range state.RetainedSummaries {
        lines = append(lines, "- "+compactDurableSummaryEntry(item))
    }
    return []string{strings.Join(lines, "\n")}
}
```

But wait, `compactDurableSummaryEntry` is also in `source_plan.go` and we planned to delete it. Move it to `compaction.go` as well, or inline it.

Actually, for compaction prompts, the retained summaries can be rendered more simply:

```go
func renderRetainedSummaries(state DurableContextState) string {
    if len(state.RetainedSummaries) == 0 {
        return ""
    }
    var lines []string
    lines = append(lines, "retained summaries:")
    for _, s := range state.RetainedSummaries {
        text := s.Text
        if len(text) > 160 {
            text = text[:160] + "..."
        }
        if s.Title != "" {
            lines = append(lines, fmt.Sprintf("- %s: %s", s.Title, text))
        } else {
            lines = append(lines, "- "+text)
        }
    }
    return strings.Join(lines, "\n")
}
```

Then update `renderConversationCompactionSource()` to use this instead of `durableContextSections()`.

---

### Step 10: Update all tests

#### 10a. `internal/prompt/source_plan_test.go`

**`TestPlanSourceAssemblyOrdersSources`** (lines 12-45):

Remove `plannedSourceDurableContext` from expected steps:

```go
want := []sourcePlanStep{
    {Kind: plannedSourcePreamble, Placement: plannedSourcePlacementCore, PassThrough: false},
    {Kind: plannedSourceAgents, Placement: plannedSourcePlacementCore, PassThrough: false},
    {Kind: plannedSourceProjectContext, Placement: plannedSourcePlacementCore, PassThrough: false},
    {Kind: plannedSourceSkills, Placement: plannedSourcePlacementCore, PassThrough: false},
    // REMOVED: plannedSourceDurableContext
    {Kind: plannedSourceConversation, Placement: plannedSourcePlacementConversation, PassThrough: true},
    {Kind: plannedSourceToolSummaries, Placement: plannedSourcePlacementToolSummaries, PassThrough: false},
}
```

**`TestPlanSourceAssemblyIncludesAndPlacesOptionalSources`** (lines 66-126):

- Remove `ContextSourceDurableContext` and `ContextSourceScratchpad` from expected `blockSources` (line 100-109).
- Remove `ContextState` setup for durable context and scratchpad (lines 83-88), or keep only what's needed for other tests.
- The message ordering assertion (lines 113-118) should be updated to not reference durable context.

**`TestPlanSourceAssemblyIsBudgetIndependent`** (lines 128-178):

- Remove `DurableContextBytes` from test budgets if the field is removed from `SourceBudgetModel`.

#### 10b. `internal/prompt/assemble_test.go`

Review all tests for expected block/message counts. Key tests:

- `TestAssembleOrdersContextAndSkipsImplicitSkills` — currently expects 6 blocks, 7 messages. After removing durable context/scratchpad, block count stays 6 (preamble, global agents, project agents, 2x project context, tool summary). Message count stays 7 (system preamble, global agents, project agents, 2x project context user messages, conversation user, conversation assistant, tool summary). No changes needed if durable context wasn't present.

- Tests that explicitly set `ContextState` with durable content will need updating to not expect those blocks.

#### 10c. `internal/output/context_report_test.go`

**`TestBuildContextReportIncludesCategoriesAndTotals`** (lines 13-105):

- Remove the "retained context state" message from `Messages` slice (line 23).
- Remove the `ContextSourceDurableContext` block from `Blocks` slice (line 48).
- Remove `"- durable context:"` from the `want` string checks (line 71).

#### 10d. `internal/agent/context_management_integration_test.go`

**`TestRunnerSmartContextManagementEndToEndEmitsDiagnostics`** (lines 32-189):

- The test currently expects scratchpad content in system messages (via `messageContentsContain`). After the change, scratchpad is a user message. If `messageContentsContain` checks all messages regardless of role, it may still pass. Verify.
- Check `messageContentsContain` implementation.

**`TestRunnerSmartContextManagerEmitsScratchpadDiagnostics`** (if exists):
- Verify scratchpad events still fire correctly. The event emission path is unchanged.

#### 10e. `internal/agent/runner_test.go`

Search for tests that check:
- `ContextSourceDurableContext` in blocks
- `"retained context state"` in messages
- `Name: "scratchpad"` with system role

Update or remove these assertions.

#### 10f. `testdata/stage3/compaction_fixture/assembly_blocks.snapshot`

Line 5 contains:
```
source=durable_context path=retained context state truncated=false content=...
```

This snapshot will no longer match because durable context blocks are removed from main assembly. Either:
- Update the test that uses this snapshot to not include durable context
- Or update the snapshot file to remove line 5

Find which test loads this snapshot and update accordingly.

#### 10g. `internal/prompt/assemble_test.go` snapshot test

Search for `assembly_blocks.snapshot` usage:

```go
grep -n "assembly_blocks.snapshot" internal/prompt/assemble_test.go
```

Update the test to expect the new block list without durable context.

---

### Step 11: Verify `ContextState.Render()` still works

**File:** `internal/agent/context_state.go`

The `Render()` method (lines 75-88) produces a text snapshot for diagnostics:

```go
func (s ContextState) Render() string {
    var parts []string
    if s.TurnCount > 0 || s.CompactionCount > 0 {
        parts = append(parts, compactSessionState(s.TurnCount, s.CompactionCount))
    }
    if len(s.FileTrackerSummary) > 0 {
        parts = append(parts, "tracked files: "+strings.Join(s.FileTrackerSummary, "; "))
    }
    if len(s.RecentToolCalls) > 0 {
        parts = append(parts, "recent tool calls: "+strings.Join(s.RecentToolCalls, "; "))
    }
    return strings.Join(parts, "\n")
}
```

This is fine. It reads from `agent.ContextState`, not `prompt.DurableContextState`. It's used for diagnostics/events, not prompt assembly.

---

### Step 12: Verify event emission paths

**File:** `internal/agent/events.go`

The `emitAssemblyDiagnostics()` function (line 100) iterates over blocks and emits events. Check if it references `ContextSourceDurableContext` or `ContextSourceScratchpad`:

```go
grep -n "DurableContext\|Scratchpad" internal/agent/events.go
```

If found, update to remove those cases or handle the new structure.

---

### Step 13: Run full test suite

After all changes:

```bash
go test ./internal/prompt/... -v
go test ./internal/agent/... -v
go test ./internal/output/... -v
go build ./...
go vet ./...
```

Fix any failures iteratively. Pay special attention to:
- Block/message count mismatches
- Role assertions (scratchpad was system, now user)
- Ordering assertions (durable context no longer appears before conversation)

---

## Files Modified

| File | Changes |
|------|---------|
| `internal/prompt/source_plan.go` | Remove durable context & scratchpad steps and functions |
| `internal/prompt/types.go` | Strip `DurableContextState` to `RetainedSummaries` only |
| `internal/prompt/budget.go` | Remove durable context & scratchpad from budget tracking |
| `internal/prompt/source_render.go` | Remove dead `blockMessage` cases |
| `internal/prompt/compaction.go` | Move/adapt retained summary rendering for compaction prompts |
| `internal/agent/message_convert.go` | Add `buildScratchpadMessage()`, update `assemblyOptions()`, simplify `toPromptContext()`/`fromPromptContext()` |
| `internal/agent/turn_progression.go` | Update zone logging |
| `internal/agent/context_manager.go` | Verify `PreAssembly()` still correct with `FullMessages()` |
| `internal/prompt/source_plan_test.go` | Update expected steps and block sources |
| `internal/prompt/assemble_test.go` | Update block/message counts |
| `internal/output/context_report_test.go` | Remove durable context from test data |
| `internal/agent/context_management_integration_test.go` | Update scratchpad message role assertions |
| `internal/agent/runner_test.go` | Update tests referencing durable context/scratchpad blocks |
| `testdata/stage3/compaction_fixture/assembly_blocks.snapshot` | Remove durable context line |

## Acceptance Criteria

- [ ] `go test ./...` passes
- [ ] `go build ./...` succeeds
- [ ] System prompt zone is byte-identical across turns (verified via debug log)
- [ ] Scratchpad appears as the last user message in every prompt
- [ ] Compaction summaries appear as system messages within the conversation, not in a separate durable context block
- [ ] No "retained context state" JSON system message is produced in main prompt assembly
- [ ] Compaction prompts still include retained summaries for context
