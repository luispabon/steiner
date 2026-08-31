# Tool result thinning: design notes

Working notes for the multi-session effort trimming provider-visible JSON
across steiner's built-in tools (originating from GitHub issues #608-#619,
"immediate" label). Read this before trimming any tool result — it captures
hard-won constraints so each session doesn't have to rediscover them.

## The core problem: one string, multiple audiences

A tool handler returns a struct (e.g. `builtin.GrepResult`). That struct
gets `json.Marshal`ed into one string that then serves, unmodified unless a
tool has explicit reshaping:

1. **The provider** — this is the actual message content sent to the model,
   and the thing the whole initiative is trying to shrink.
2. **Session persistence** — `agent.Message.Content` is the *only* thing
   ever written to disk for a completed tool call (see
   `internal/session`/`internal/history`). There is no separate host-only
   channel. On process resume, `internal/interactive/replay.go`'s
   `replayToolResult` reconstructs everything — including a fresh
   `ToolCallFinishedEvent` — from this one persisted string.
3. **The TUI preview** — `internal/output/preview_*.go`'s `BuildToolPreview`
   is called with this same string
   (`internal/agent/turn_progression.go`: `preview =
   output.BuildToolPreview(call.Name, ..., toolContent)`), live during
   generation and again during replay.

Some tools add a second shaping layer before persistence:
`internal/tool/noise_strip.go`'s `ShapeIngestedToolResult` re-parses the
marshaled JSON into its own mirror struct (currently `bashIngestionResult`,
`grepIngestionResult`) and re-marshals — applying an *additional* context-
ingestion size cap independent of whatever the handler already did. When a
mirror struct exists for a tool, it — not the handler's own struct — is the
last writer of what actually reaches the provider/persistence/TUI. **Trim
the handler struct and the mirror struct together, in the same commit, or
the field silently comes back** (a missing `omitempty`, or a field the
mirror still declares, reinjects a zero/empty value on re-marshal even after
the handler stops setting it).

## The decision procedure, per field

For every field on a tool result struct, ask in this order:

1. **Does the model need it?** If yes, keep it. Don't guess — check real
   usage (see "Verify against real session data" below) before assuming a
   field is optional or dead.
2. **Does the TUI preview need it?** Grep `internal/output/preview_*.go` for
   the field's JSON key (not just the Go identifier — a consumer might
   parse a raw string). If some `preview_*.go` reads it into a `ToolPreview`
   field, check whether `internal/tui/content_render_preview.go` actually
   *renders* that `ToolPreview` field anywhere, or whether the preview
   builder reads it into a struct field that's parsed but never copied into
   `ToolPreview` (dead code — this exact case existed for `preview_grep.go`
   reading `Matches` and unused, and reading `Truncated`/`HasMore` from
   already-stripped content that made them permanently `false`).
3. **If the TUI genuinely needs it, can it be *derived* from `output`
   instead of carried as a separate field?** This is the preferred fix and
   has precedent twice already:
   - `preview_list.go` (#609/#611, commit `8279a957`) derives `Returned:
     len(entries)` from the already-parsed `output` instead of trusting a
     `returned` field — the same fix was then applied to `grep`
     (`preview_grep.go` derives `Returned: len(preview.GrepFiles)`, since
     `GrepFiles` is already built from `output` by the mode-specific
     parsers).
   - For a *string* rather than a count: `bash`'s `message` field carried
     text no field-derivation could produce (e.g. "sandbox wrapper not
     resolved…") — the fix there was to fold that text directly into
     `output` at the point the handler would otherwise have set `message`,
     so `output` alone is self-describing for both audiences. Same
     principle, applied to text instead of a count.
4. **Only if a field is truly needed by neither audience, delete it
   outright.** Also remove whatever computed it — don't leave dead
   computation behind (e.g. `grep`'s `grepFileHashes()`, which hashed every
   matched file's full disk content for a field nothing ever read).
5. **Decoupling the TUI's content source from the provider's is a last
   resort, not yet used anywhere in this codebase**, and probably shouldn't
   be reached for first. It sounds appealing (full TUI fidelity, minimal
   provider payload) but doesn't actually work cleanly: since only the
   provider-bound string persists to disk, a resumed session can only ever
   reconstruct the TUI preview from that same trimmed string — so a
   live/persisted split just creates an asymmetry (full fidelity live,
   degraded after every resume) instead of solving the problem. If you
   really can't resolve a field via steps 1-4, that asymmetry needs to be a
   deliberate, called-out decision — not a silent side effect — and probably
   needs new persistence machinery (e.g. actually persisting
   `ToolCallFinishedEvent.Preview`, which is currently `json:"-"` and never
   written to disk) to avoid the asymmetry entirely.

## Verify against real session data, don't guess either direction

Local session history lives at `~/.config/steiner/sessions/` (JSON files,
one per session, structured for `jq` — `.lineage.generations[].messages[]`
holds both tool calls and tool result messages, filterable by
`.name == "<tool>"`). These files can be large; never read one directly into
context — always filter/count via `jq`/`grep` pipelines (a background
sub-agent works well for this, since the aggregate numbers are all that's
needed, not the raw content).

Two concrete lessons from the #608/#610/#612 pass, in both directions:

- **Don't assume "optional" means "unused."** `read`'s `file_hash` looked
  like pure host-side verification data — optional in `mutate`'s
  `file_hash` input, with a working fallback (`FileObserved`, session-scoped
  read tracking) when absent. Checking session history showed the model
  actually supplies it on 31% of `mutate` operations against existing files
  (up to 50% for some operation types). Dropping it would have silently
  disabled something genuinely load-bearing.
- **Don't assume "computed" means "used."** `grep`'s `file_hashes` (a map of
  every matched file's content hash) looked like it might be enabling the
  same kind of guard from a `grep` call instead of a `read`. Checking both
  the code (the mirror struct in `noise_strip.go` never carried it, and
  `grep` was never wired into the `FileObserved` tracker) and session
  history (0 of 482 stored grep results contained the key) confirmed it was
  dead on arrival — safe to delete along with the disk-read cost that
  computed it.

## Known pre-existing gaps — don't silently "fix" them mid-trim

A few things surfaced during this pass that are real but out of scope for a
field-trimming PR — note them in the PR description if you hit them, don't
fold a fix into an unrelated trim:

- `internal/agent/file_tracker_read_observation.go`'s dedup-annotation path
  (`decorateReadObservation`) re-marshals a separate host-side `readResult`
  struct (`internal/agent/file_tracker_read.go`) that never included
  `file_hash` to begin with — so a deduped re-read of an unchanged file
  drops `file_hash` even on tools where a normal read keeps it. Pre-existing
  asymmetry, unrelated to any single tool's field trim.
- README.md's `mutate` row (~line 204, may drift) documents "same-session
  read/grep observation" as a guard-satisfying mechanism — `grep` was never
  actually wired into that tracker (only `read` was). Reword when you touch
  a tool whose fields make the sentence say something false; don't leave it
  claiming a mechanism that doesn't exist.

## Non-goals that hold across this entire initiative

Every one of the #608-#619 issues explicitly excludes changing `mutate`
result behavior. Don't let a "consistent pattern" impulse pull `mutate`'s
result shape into a tool-result trim PR — if `mutate` needs the same
treatment, that's its own issue/plan.
