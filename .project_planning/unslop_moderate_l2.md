# Unslop L2 — `internal/tui` local dedup (das-plan hand-off)

> **Feed this file to `/das-plan`.** It is a scoped, behavior-preserving cleanup brief for
> the `internal/tui` package, derived from a 5-slice audit of Steiner on 2026-06-19.
> Nothing here is implemented. This is **Loop 2 of 4** in the "unslop" program:
> L1 = non-TUI semantic dedup + dead-code (done/in-progress separately), **L2 = this
> (TUI dedup)**, L3 = pure file-splits across all packages, L4 = aggressive backlog.
>
> **Run order:** L2 should run **after L1** (L1 exports shared helpers some TUI code may
> reuse) and **before L3** (L3 splits the TUI god-files; do the semantic edits first so
> the split rebases cleanly). L2 is independent of L4.

## Scope

In: behavior-preserving local deduplication and dead-code removal **inside
`internal/tui` only** (plus `internal/tui/theme`, `internal/tui/prefs` where noted).
`internal/tui` is ~13.2k LOC — 27% of the codebase — so it gets its own loop for focused
review.

Out: the TUI oversized-file splits (A-006 `content_render_chrome.go`, A-007
`model_update_keys.go`, A-016 `model_input.go`) → those are **L3**. The aggressive TUI
structural items (A-008 theme maps, A-011 agent-type drift, A-014 overlay registration)
→ those are **L4**. No behavior change, no new features, no public-API change.

## Constraints (any executing loop must honor)

- **Behavior-preserving only.** TUI rendering output must be byte-identical. The existing
  golden/snapshot-style tests (`content_*_test.go`, `model_test.go` ~3386 lines,
  `model_input_test.go`, `theme/*_test.go`) are the safety net. **No test may be edited to
  accommodate a behavior change.**
- CLAUDE.md invariants: keep render/update/sidebar/event-state concerns split; file-size
  targets (~300, split by ~500); **no `util`/`helper`/`common` packages** — shared
  helpers stay in `internal/tui`; `0o` octal; don't shadow builtins.
- A-003 (approval-button strip) is the one **med-risk** item here — it reconciles two
  divergent visual code paths with different width math; verify pixel-for-pixel against
  `content_render_chrome_test.go` + `content_tool_test.go`, or drop it to a follow-up.

## Verification

Per step: `gofmt -w <files>` + `goimports -w <files>`, then
`go test ./internal/tui/...` + `go build ./...`. Loop end: `make check`.

## Findings (full detail)

**A-001 — repetition** · `content_tool.go:71-99` · risk low
`summarizeDelegateArgs`, `delegatePromptText`, `summarizeFirstArgValue` are a near-
identical triplicate: all walk the ordered key list `{task, prompt, description,
instructions, goal}` over `map[string]any` and fall back to the first value. Differences
are only nil-guard placement and empty-map fallback. **Fix:** collapse into one
`delegateArgText(args map[string]any) string` with a nil-guard up front; delete the other
two. Call sites: `content_tool.go`, `content_events_delegation.go:355,403`. Value M
(~20 LOC, 3→1 fn).

**A-002 — repetition** · `content_tool.go:33-57` (`summarizeArgs`) · risk low
Calls `strings.EqualFold(strings.TrimSpace(tool), …)` 9×. `normalizeToolName`
(`content_events_tool_state.go:257`) already does `ToLower(TrimSpace())`. **Fix:**
`tool = normalizeToolName(tool)` at top, switch on `tool == "x"`. Value S (~15 LOC).

**A-003 — repetition** · `content_render_chrome.go:62-76` + `content_tool.go:578-625`
(`renderToolApprovalButtons`) · risk **med**
Two independent implementations of the 3-button approval strip with divergent styling and
label text (`[y] approve/[n] deny/[a] always` vs `Allow once/Always allow/Deny`). **Fix:**
extract `renderApprovalButtonStrip(accent lipgloss.Color, selectedIdx int) string`
parameterized on selection state; call from both. Reconciles a user-visible label
inconsistency too. Value M (~40 LOC). **Width math must be reconciled carefully.**

**A-004 — repetition** · `model_layout.go:15-66` (`layout`/`relayoutInput`) · risk low
First ~15 lines verbatim-duplicated (contentWidth, clamp, `MaxWidth=0`,
`SetWidth(99999)`, inputRows/activityRows/maxInputRows). Magic `9` (`delegationOverhead`)
literal in both. **Fix:** extract `computeInputRows(contentWidth int) (inputRows,
activityRows int)` + named const `delegationBodyOverhead = 9`; `layout()` additionally
sets viewport width, `relayoutInput()` early-returns on unchanged height. Value M
(~25 LOC).

**A-005 — overengineering** · `content_render_chrome.go:491-503` · risk low
`delegationToolLabelStyle` / `delegationBorderStyle` are single-use one-line wrappers
destructuring `delegationStyles`'s two returns. **Fix:** delete; destructure inline at the
2 call sites (453-455, 345). Value S (~14 LOC). *(Note: this file is split in L3; do A-005
before the split or coordinate.)*

**A-009 — repetition** · `content_events_delegation.go:647` (`nanoNow`) +
`content_events_approval_diagnostics.go:200` · risk low
Testable time hook `nanoNow` (a `var func() int64`) exists but approval diagnostics uses
an inline current-time expression. **Fix:** call `nanoNow()` for a single time source;
makes approval-diagnostics tests consistent with delegation tests. Value S.

**A-010 — dead-code** · `content_render.go:164-178` · risk low
Five single-line forwarding render methods (`renderApprovalSegment`, `renderToolSegment`,
`renderThinkingSegment`, `renderInterruptedSegment`, `renderDefaultSegment`), each called
once from `renderSegment`. **Fix:** inline into the switch arms. Value S (~20 LOC, 5 fns).
Covered by `content_test.go`.

**A-012 — overengineering** · `model_view.go:190-260` (`renderInputView`) · risk low
70-line fn with deeply nested if/for interleaving placeholder, command-prefix, and normal
line rendering via `continue`. **Fix:** extract `renderPlaceholderInputLines(...)` and
`renderNormalInputLines(...)`; `renderInputView` becomes a short dispatcher. Value M
(~30 LOC reshuffled). Covered by `model_input_test.go`, `model_test.go`.

**A-013 — repetition** · `content_render_chrome.go:340-348` + `103-114` · risk low
Box-style construction (`NewStyle().Background().Padding(0,1).Border(NormalBorder()).
BorderForeground().Width(w-2).Render`) appears verbatim 3× (delegation, compaction). **Fix:**
extract `renderStyledBox(content string, borderColor, bgColor lipgloss.Color, width int)
string`. Value S (~25 LOC). *(Coordinate with L3 split of this file.)*

**A-015 — repetition** · `content_events_tool_state.go:18-91` · risk low
`appendToolCallStartedEvent` / `appendToolCallFinishedEvent` share verbatim guards
(display_file early-return, advisor early-return, delegate/specialized branch). **Fix:**
extract `shouldSkipToolEvent(tool string) bool` + `isDelegateOrSpecialized(tool string)
bool`. Value S (~12 LOC). Covered by `content_tool_test.go`, `content_test.go`.

**A-017 — repetition** · `model_layout.go:15,46` + `model_view.go:View()` · risk low
Sidebar-aware `contentWidth` computed 3× (twice with clamp): `contentWidth := m.width;
if m.sidebar.Visible(m.width) { contentWidth = m.width - sidebarWidth - 1 }; clamp`. **Fix:**
add `func (m *Model) contentWidth() int`; all three sites use it. Value S (~15 LOC + a
bug-class removed if `sidebarWidth` ever changes).

**A-018 — dead-code/repetition** · `content_render_preview.go:~221` (`buildFetchURLLines`
markdown arm) · risk low
The markdown sub-path hand-rolls a `previewDocument` + `renderPreviewLine` loop that
duplicates `buildFilePreviewLines`. **Fix:** extract `renderPreviewDocumentLines(doc
output.PreviewDocument) []string`, call from both. Value S (~20 LOC). Covered by
`content_render_preview_test.go`. *(`content_render_preview.go` 531 LOC is split in L3;
coordinate.)*

## Recommended step decomposition for the planner

Group by file/concern (each a coherent, independently-verifiable deliverable):

1. **`content_tool.go` dedup** — A-001 (delegate-arg triplicate) + A-002 (normalize-once).
2. **`model_layout.go` / `model_view.go` width & input-rows dedup** — A-004 + A-017 + A-012.
3. **`content_render_chrome.go` helper extraction** — A-005 + A-013 (+ feeds A-003).
4. **Approval-button strip unification** — A-003 (med-risk; isolate as its own step).
5. **Segment/event/preview dedup** — A-010 + A-015 + A-018 + A-009.

Steps 1–3,5 are low-risk and parallel-safe in principle; keep serial for review sanity.
Step 4 (A-003) is the only one needing careful visual verification.

## Provenance

Audit slice A (read-only sub-agent, 2026-06-19) applying slop-detector + go-code-audit +
improve-codebase-architecture methodology. Line numbers were evidence-cited but must be
re-verified at edit time (the executor reads each file before mutating). Companion:
`.project_planning/2026-06-19_complexity-reduction/research.md`. Total est. impact:
~190 LOC removed, behavior-preserving.
