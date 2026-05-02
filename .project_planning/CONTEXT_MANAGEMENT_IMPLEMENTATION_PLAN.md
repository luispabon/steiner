# Implementation Plan: Context Management Improvements

Three changes to steiner's context management system. Ordered by dependency graph - stage 1 has no dependencies, stages 2 and 3 depend on stage 1 only for the config parsing (they are otherwise independent and can be parallelised).

---

## Stage 1: File Annotation Write Generation Counter

**Impact:** Closes a correctness bug (sub-second mtime race).
**Risk:** Low. Additive change, no existing behaviour modified.
**Files likely affected:** FileTracker, write/edit tool handlers.

### What

Add a per-path write generation counter to FileTracker. Every steiner-initiated file write or edit increments the counter for that path. File read annotation checks both mtime AND generation counter before serving an annotation. If either has changed since the last read, serve full content.

### Implementation steps

1. **Add generation counter to FileTracker state.** Add a `map[string]uint64` field (e.g. `writeGeneration`) to whatever struct holds the per-path file tracking metadata. Initialise to 0 for new paths.

2. **Bump generation counter on writes.** Find the write and edit tool handlers (the code paths that modify files on disk on behalf of the model). After the file write succeeds but before the tool result is returned, increment `writeGeneration[path]`. This must be synchronous - the counter must be current before any subsequent read can check it.

3. **Record generation counter on reads.** When a file read is tracked (the code that records path, turn, mtime for annotation purposes), also record the current `writeGeneration[path]` value at read time. If the path has no entry in the generation map, record 0.

4. **Check generation counter in annotation logic.** The existing annotation decision point checks mtime. Add a second condition: the generation counter at read time must equal the current generation counter. If `writeGeneration[path]` has incremented since the last read, the annotation is invalid - serve full content.

5. **Persist generation counters across compaction.** Verify that the generation counter map, like the rest of FileTracker state, lives in Go and is not stored in conversation history. It should survive compaction automatically if FileTracker already does. Add a test confirming this.

6. **Emit generation counter in FileAnnotationEvent.** When a file annotation is served or skipped, include the generation counter state in the event. When the annotation is skipped specifically because the generation counter mismatched (mtime was identical but generation differed), log this distinctly - it means the race condition was caught.

### Testing

- Unit test: write a file, read it, write it again within the same mtime second, read it again. Assert the second read serves full content (generation counter mismatch), not an annotation.
- Unit test: write a file, read it, wait >1 second, read it again without any intervening write. Assert annotation is served (both mtime and generation match).
- Unit test: verify generation counters survive compaction (trigger compaction, confirm counters are intact).

---

## Stage 2: Epoch-Based Observation Masking

**Impact:** Eliminates per-turn KV cache invalidation in the masked conversation prefix.
**Risk:** Medium. Changes the core masking loop. Existing masking tests will need updating.
**Files likely affected:** SmartContextManager (masking logic in PreAssembly), config parsing, compaction handlers.

### What

Replace rolling per-turn masking with batched epoch-based masking. The masking boundary is frozen between epoch advances, keeping the masked section byte-stable across turns. Epochs advance on a turn count trigger or a context pressure trigger.

### New state on SmartContextManager

```go
epochMaskBoundary int  // turn index below which all turns are masked
epochStartTurn    int  // turn at which the current epoch began
```

Both initialise to 0 at session start (no turns masked initially).

### Implementation steps

1. **Add epoch state fields to SmartContextManager.** Two integer fields: `epochMaskBoundary` and `epochStartTurn`. Initialise both to 0.

2. **Refactor masking predicate.** The current masking logic likely has a predicate like `turn < currentTurn - maskingWindow`. Change this to `turn < epochMaskBoundary`. This is the core change - everything else is plumbing.

3. **Implement epoch advance check.** At the start of PreAssembly (before masking is applied), check the two epoch advance triggers:
   - **Turn count trigger:** `currentTurn - epochStartTurn >= maskingWindowTurns` (where `maskingWindowTurns` is the existing M config value, reused as the epoch length K). Using M as K is the natural default - the epoch length equals the masking window, so turns become eligible for masking at the same rate they would have in rolling mode, just applied in batches.
   - **Context pressure trigger:** estimated token usage exceeds 80% of `(contextSize - safetyMarginTokens - maxCompletionTokens)`. This requires a token estimate before masking is applied. If the token estimator is not available at this point in the pipeline, defer this trigger to a later iteration and ship with the turn count trigger only - it handles the common case.

4. **Execute epoch advance.** When either trigger fires:
   - Set `epochMaskBoundary = currentTurn - maskingWindowTurns`
   - Set `epochStartTurn = currentTurn`
   - Emit an EpochEvent with: trigger reason, new boundary, number of turns masked in this batch

5. **Reset epoch state on compaction.** In every compaction strategy (drop, summarize, hybrid), after compaction completes:
   - Set `epochMaskBoundary = 0` (or the index of the oldest retained turn, so no retained turns are masked)
   - Set `epochStartTurn = currentTurn`
   - Include epoch reset in CompactionEvent

6. **Add EpochEvent to EventSink.** New structured event type:
   ```go
   type EpochEvent struct {
       Trigger       string // "turn_count" or "context_pressure"
       NewBoundary   int
       TurnsMasked   int    // number of turns newly masked in this batch
       EpochStartTurn int
   }
   ```

7. **Update ContextMaskingEvent.** When masking is applied as part of an epoch advance, annotate the ContextMaskingEvent (or the new EpochEvent) so the observer can distinguish "turn was already masked from a previous epoch" from "turn was newly masked in this epoch advance."

### Edge cases

- **First epoch advance:** If `epochMaskBoundary` is 0 and the session has run for M turns, the first epoch advance masks turns 0 through `currentTurn - M`. All prior turns transition from verbatim to masked in one batch. This is a large cache invalidation on the first epoch advance but is unavoidable - it's the transition from "nothing masked" to "masking active."
- **Session start from loaded conversation (PostIngestion):** If a session loads an existing conversation, `epochMaskBoundary` should be set based on the loaded conversation's turn count, not 0. Set `epochMaskBoundary = max(0, loadedTurnCount - maskingWindowTurns)` and `epochStartTurn = loadedTurnCount`.
- **Very short sessions:** If the session ends before the first epoch advance triggers, no masking is applied. This is correct - short sessions don't need masking.

### Testing

- Unit test: run 10 turns with M=5. Assert that turns 0-4 are unmasked on turn 9 (epoch hasn't fired yet at turn 5 if epoch length = M = 5... actually check: epoch fires when `currentTurn - epochStartTurn >= M`, so at turn 5 it fires). Verify that masking boundary advances to `5 - 5 = 0` - wait, that masks nothing. Let me reconsider.

  Actually: at session start, `epochStartTurn = 0`, `epochMaskBoundary = 0`. At turn 5, `currentTurn - epochStartTurn = 5 >= M = 5`, so epoch fires. New boundary = `5 - 5 = 0`. That masks turns < 0, which masks nothing. At turn 10, epoch fires again. New boundary = `10 - 5 = 5`. Now turns 0-4 are masked. This is correct but means the first epoch of actual masking happens at turn 2*M, not turn M. This is consistent with the spec's intent (context runs slightly larger between epochs) but the test should reflect this.

  Alternatively, initialise `epochStartTurn = -M` so the first epoch fires at turn 0 + M = M. This would make turn M the first epoch advance, setting boundary to `M - M = 0`, masking turns < 0 (nothing). Second advance at turn 2M, boundary = `2M - M = M`, masking turns 0 to M-1. Same result. The choice of initial `epochStartTurn` doesn't change when actual masking begins. The first M turns are always within the masking window by definition. Document this behaviour.

- Unit test: verify masked section is byte-identical between epoch advances. Run turns M+1 through 2M-1, serialize the masked portion of the prompt on each turn, assert all serializations are identical.
- Unit test: trigger compaction, verify epoch state resets. Run enough turns to trigger compaction, assert `epochMaskBoundary` is reset to match retained turns.
- Integration test: context pressure trigger. Inject large tool results to push token usage past 80%, verify epoch advances early.

---

## Stage 3: Tiered Scratchpad Mode

**Impact:** Eliminates model-written scratchpad dependency for small models. Reduces system prompt size. Removes scratchpad tool registration overhead.
**Risk:** Medium-high. Touches system prompt construction, tool registration, scratchpad injection, and adds a new second-pass inference path.
**Files likely affected:** SmartContextManager, system prompt builder, tool registry, config parsing, scratchpad state management.

### What

Add a `scratchpad_mode` config field with two values: `scaffold_only` (default) and `hybrid`. In scaffold_only mode, the model is never asked to call the scratchpad tool, the tool is not registered, and scratchpad instructions are removed from the system prompt. Intent fields are populated by steiner via a cheap second-pass inference on pivot turns. The decisions field is fully scaffold-managed via heuristic extraction. In hybrid mode, the existing behaviour is preserved with a reduced field set (4 fields instead of 7).

### Sub-stage 3a: Config and mode plumbing

1. **Add `scratchpad_mode` to config.** New field under `context_management`:
   ```yaml
   context_management:
     scratchpad_mode: scaffold_only  # scaffold_only or hybrid (default: scaffold_only)
   ```
   Parse and validate. Accepted values: `scaffold_only`, `hybrid`. Default: `scaffold_only`.

2. **Thread scratchpad mode through SmartContextManager.** The mode must be available to: system prompt builder, tool registry, OnTurnComplete handler, scratchpad injection logic, and the new pivot detection + second-pass inference code.

3. **Conditional tool registration.** When `scratchpad_mode == scaffold_only`, do not register the scratchpad tool with the model. The model should not see it in the tool list. When `scratchpad_mode == hybrid`, register it.

4. **Conditional system prompt content.** When `scratchpad_mode == scaffold_only`, omit scratchpad instructions from the system prompt. This saves tokens and avoids confusing the model with instructions for a tool it can't see. When `scratchpad_mode == hybrid`, include them. Note: this changes the stable zone content, so the cached system prompt must be rebuilt when the mode changes (but mode changes only happen at session start via config, so this is not a runtime concern).

5. **Conditional OnTurnComplete logic.** When `scratchpad_mode == scaffold_only`, OnTurnComplete is a no-op (no miss tracking, no consecutive-miss counter). When `scratchpad_mode == hybrid`, existing behaviour.

### Sub-stage 3b: Enhanced scaffold state

Extend the scaffold state (the Go-managed portion of the scratchpad) with three new fields, derived deterministically from tool call outcomes:

1. **Last action.** After each model response, extract the tool call the model made and a truncated summary of the result. For bash: exit code + last N bytes of output. For read: file path + line count. For edit: file path + success/fail. For grep: result count. Store as a single string, e.g. `"bash(go test ./...): exit 1, 3 failures"` or `"read(internal/agent/runner.go): 247 lines"`. Overwritten each turn.

2. **Working file.** The most recently read or edited path. Trivially derived from FileTracker - it already knows the last file touched. Store as a single path string.

3. **Momentum signal.** A simple state machine over the last 2-3 tool calls:
   - `iterating` — same primary file, similar tool types (e.g. edit then bash, edit then edit)
   - `pivoting` — different file from previous turn, or different tool type category
   - `exploring` — grep/glob/ls/read of new files without edits

   Categories for the state machine:
   - Read: read, ls, glob
   - Search: grep
   - Modify: edit, write
   - Execute: bash

   Transition logic: if the current tool category differs from the previous turn's category AND the primary file differs, signal is `pivoting`. If the current tool category is Read/Search on files not previously read, signal is `exploring`. Otherwise `iterating`.

### Sub-stage 3c: Pivot detection and second-pass inference

1. **Detect pivot turns.** After each model response, before injecting the scratchpad for the next turn, check whether this was a pivot turn. A turn is a pivot if any of:
   - Momentum signal is `pivoting`
   - The turn immediately follows compaction (compaction count incremented this turn)
   - It is the first turn of the session

2. **Second-pass inference on pivot turns.** When a pivot is detected and `scratchpad_mode == scaffold_only`:
   - Build a minimal context: scaffold state (~200 tokens) + model's last response truncated to first ~200 tokens
   - System prompt: `"Given the current scaffold state and the model's last action, respond with ONLY a JSON object: {\"intent\": \"what is being done and why\", \"next\": \"planned next action\"}"`
   - Call the model with `max_tokens: 150`
   - Parse the JSON response. On parse failure, carry forward previous intent/next values (same carry-forward philosophy as hybrid mode's miss handling)
   - Store the extracted `intent` and `next` in scratchpad state

3. **Carry forward on non-pivot turns.** When the turn is not a pivot, carry forward the previous `intent` and `next` values unchanged. No inference call.

4. **Provider routing for second-pass.** The second-pass inference should use the same provider/model as the main inference. No separate model config. The call is small enough (~600-800 input tokens, 150 output tokens) that even on iGPU inference it completes in under a second.

### Sub-stage 3d: Scaffold-managed decisions

Replace the model-written `decisions` field with heuristic extraction from tool call outcomes. This applies in both scaffold_only and hybrid modes (in hybrid mode, the model can also write to `decisions` via the tool call, and steiner merges both sources).

1. **Extract decisions from tool results.** After each tool result is processed:
   - **Edit tool:** append `"edited {path}: {edit summary}"` (use the edit tool's own summary if available, otherwise `"edited {path}"`)
   - **Bash tool with test commands:** detect test commands (look for `go test`, `pytest`, `npm test`, `cargo test`, `make test` in the command string). Append `"tests {passed|failed}: {command summary}"` based on exit code
   - **File switch:** when the primary working file changes (detected via FileTracker), append `"switched to {new path} (from {old path})"`
   - **Compaction:** append `"compaction occurred at turn {N}, {strategy} strategy"`

2. **Storage.** Same 2000-byte ring buffer with oldest-first eviction. Same append-only semantics - decisions are never overwritten or edited, only appended and evicted.

3. **Hybrid mode merging.** When `scratchpad_mode == hybrid`, the model's tool call may also include a `decisions` value (new decisions this turn). Steiner appends both the heuristic-extracted decisions and the model-written decisions, heuristic first. This means the decisions field always has the factual record even if the model writes nothing.

### Sub-stage 3e: Reduce hybrid mode to 4 fields

When `scratchpad_mode == hybrid`, the scratchpad tool takes 4 fields instead of 7:

| Field | Owner | Description |
|-------|-------|-------------|
| `intent` | model | What is being done and why (merges old goal/plan/step) |
| `decisions` | steiner-managed | Model writes new decisions this turn; steiner appends alongside heuristic-extracted decisions |
| `open` | model | Unresolved questions or risks |
| `next` | model | Planned next action |

Removed fields:
- `goal`, `plan`, `step` — collapsed into `intent`
- `files` — redundant with FileTracker's ground-truth tracking

Update the scratchpad tool schema, system prompt instructions, and IngestToolResult processing to reflect the new field set.

### Sub-stage 3f: Scratchpad injection update

The synthetic user message injected at the end of the prompt must reflect the active mode:

**scaffold_only mode:**
```
[SCRATCHPAD - steiner context state]
Turn: {N} | Compactions: {C}
Momentum: {iterating|pivoting|exploring}
Working file: {path}
Last action: {summary}

Files read:
- {path} (turn {T}, {modified|unmodified})
- ...

Recent tool calls:
- turn {T}: {tool}({args summary})
- ...

Decisions:
- {decision 1}
- ...

Intent: {intent from last pivot inference or carry-forward}
Next: {next from last pivot inference or carry-forward}
```

**hybrid mode:**
Same scaffold block, plus model-written fields below it:
```
[MODEL SCRATCHPAD]
Intent: {model-written intent}
Open: {model-written open}
Next: {model-written next}
```

### Testing

- Unit test: scaffold_only mode does not register scratchpad tool. Assert tool list does not contain scratchpad.
- Unit test: scaffold_only mode system prompt does not contain scratchpad instructions. Compare system prompt content between modes.
- Unit test: pivot detection triggers on file switch, tool type change, and post-compaction.
- Unit test: second-pass inference produces valid intent/next JSON. Mock the model call, verify parsing.
- Unit test: second-pass inference failure (invalid JSON) carries forward previous values.
- Unit test: heuristic decision extraction from edit, bash (test), file switch, compaction.
- Unit test: decisions ring buffer eviction at 2000 bytes.
- Unit test: hybrid mode merges heuristic and model-written decisions.
- Unit test: hybrid mode uses 4-field tool schema, not 7.
- Integration test: run a 15-turn session in scaffold_only mode, verify scratchpad content is coherent throughout, especially across a compaction event.
- Integration test: run same session in hybrid mode with a compliant model, verify model-written fields appear.

---

## Implementation order

```
Stage 1 (generation counter)  ──────────────────────────────────► merge
Stage 2 (epoch masking)        ──────────────────────────────────► merge
Stage 3a (config plumbing)     ─► 3b (scaffold) ─► 3c (pivot) ─► 3d (decisions) ─► 3e (hybrid fields) ─► 3f (injection) ─► merge
```

Stages 1 and 2 are independent and can be developed in parallel worktrees. Stage 3 is internally sequential (each sub-stage depends on the previous) but independent of stages 1 and 2 at the code level.

Recommended merge order: 1 first (smallest, lowest risk), then 2 (medium risk, isolated to masking), then 3 (largest, most cross-cutting).

If using plan-orchestrator for dispatch, stages 1 and 2 are good candidates for parallel worktree execution. Stage 3 sub-stages should be sequential within a single worktree due to their tight coupling.
