# Canon drift checks

`go test ./internal/prompt/` (part of `make check`'s `test-race`) runs two checks that catch a downstream instruction file drifting away from the orchestration canon — the `delegationInstructions` const in `internal/prompt/system.go`. This doc explains what the checks do, what counts as canon and as a "consumer," and what to do when one fails.

## What the two checks are

Two independent Go tests in `internal/prompt/`, each covering a distinct failure class observed in production (both recorded in GitHub issue #445):

- **Roster vocabulary check** (`internal/prompt/canon_roster_drift_test.go`, `TestConsumersNameOnlyCurrentSpecialists`). Addresses #445 §3: `verify` was renamed to `sanity_check`, `plan` to `evaluate`, and the generic `delegate` tool was removed, but three skills kept instructing the model to call tools that no longer existed. This check parses the current specialist/tool roster out of canon and scans consumer files for backticked tokens in a handful of framed patterns (`` `X` sub-agent(s) ``, `` delegated `X` ``, `` `X` delegation ``, `` `X(` `` tool call). Any framed token not in the current roster is a finding.

- **Duplication matcher** (`internal/prompt/canon_drift_test.go`, `TestCanonNotDuplicatedInConsumers`). Addresses #445 §6: an instruction was deleted from three sites as "restated canon," but the preamble had never actually said it — the deletion was based on a false belief that canon already covered it. This check shingles canon and consumer text and flags paragraphs whose overlap crosses a threshold, in both directions: canon prose restated in a consumer, and consumer prose later pasted up into canon.

One mechanism can't cover both failure classes. §3's stale tokens (`verify`, `plan`) are short — one or two words — and sit below any length filter a paraphrase-detection matcher would need to avoid false positives on ordinary shared vocabulary. §6's deleted instruction was longer restated prose, exactly what a shingle-overlap check is built to catch, but a bare token check would never have caught it (there's no fixed vocabulary to check against — canon prose can be paraphrased with none of the same tokens).

## What counts as canon

Only `delegationInstructions` in `internal/prompt/system.go`. Other preamble consts — `coreRules`, `advisorInstructions`, `executionModeInstructions`, workflow instructions, `agentPrompts` — are out of scope for both checks. The boundary is drawn at `delegationInstructions` because that's where the observed drift in #445 occurred, and because it has the most distinctive vocabulary (specialist names, routing rules, tool names) to check against. The other preamble consts are short, generic engineering imperatives ("run tests before committing," "keep changes minimal") that legitimately recur throughout skill docs; treating those as canon would produce constant false positives with no signal.

## Consumer files

```
skills/implement/SKILL.md.src
skills/review/SKILL.md.src
skills/simplify/SKILL.md.src
skills/plan/SKILL.md
skills/pull-request/SKILL.md
skills/partials/*.md
internal/oneshot/prompts/*.md
```

`.src` files are checked in preference to their generated `SKILL.md` for the skills that have one (`implement`, `review`, `simplify`). The generated `SKILL.md` for those skills is `.src` plus resolved partials by construction (see `docs/skill-partials.md`); checking it separately would only reproduce the same findings the `.src` and partial checks already surface. `plan` and `pull-request` have no `.src` — their `SKILL.md` is the source, so it's checked directly.

## Deferral markers

A consumer paragraph that deliberately points back at canon instead of restating it can carry one of two literal marker phrases, checked via substring match on the raw paragraph text:

- `"in your system prompt"` — e.g. `skills/review/SKILL.md.src`: "follow the briefing template in your system prompt, additionally including the appropriate pre-commit checklist from the Review-Fix Loop section."
- `"(Workflow-specific:"` — e.g. `skills/plan/SKILL.md`: "(Workflow-specific: planning never implements; this is stricter than the general routing threshold by design, not an oversight.)"

Exemption is paragraph-scoped: text is split into blank-line-delimited blocks, and if a marker occurs anywhere in a block, the whole block is exempt — not just the line containing the marker.

## Markers exempt duplication, not roster vocabulary

A marked paragraph is skipped by the duplication matcher but is still fully scanned by the roster vocabulary check. This is deliberate: the historical #445 §3 stale-token bug lived inside exactly the kind of labelled, workflow-specific instructions that carry these markers today. Exempting marked paragraphs from the roster check would skip precisely the sites where that bug occurred.

## Pinned matcher parameters

The duplication matcher is pinned to `shingleWidth = 5`, `overlapThreshold = 0.6`, `minUnitWords = 12` (constants in `internal/prompt/canon_drift_test.go`). If the real-tree run produces a false positive on legitimate shared engineering vocabulary, the only sanctioned adjustment is **raising** `minUnitWords`. Never lower `overlapThreshold` and never change `shingleWidth` — either change would silently widen the gap the check exists to close, rather than fixing the specific false positive.

## Waivers

`internal/prompt/testdata/canon_drift_waivers.json` holds waivers for the duplication matcher only. Each entry has:

- `consumer` — repo-relative consumer path
- `fingerprint` — first 12 hex characters of the SHA-256 hash of the space-joined normalized words of the matched unit
- `excerpt` — human-readable, not used for matching
- `reason` — required, non-empty

Every waiver must carry a non-empty reason; a waiver with an empty reason fails the test. A waiver that matches nothing in the current run fails as stale, so a waiver can't silently outlive the finding it was written for.

The roster vocabulary check has no waiver mechanism. A genuine finding there means a consumer names a tool or sub-agent that no longer exists — that must be fixed, not waived.

## This check just failed — now what

- **Genuine drift**: a consumer references a stale tool/sub-agent name, or restates/paraphrases canon prose. Fix the consumer — point it at canon with a deferral marker, or delete the restatement — rather than weakening either check's parameters.
- **False positive on legitimate shared vocabulary** (duplication matcher only): add a waiver with a real, specific reason, and flag it for review. Don't waive a roster vocabulary finding — fix it instead.
