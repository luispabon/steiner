## Question

What current Go libraries are good candidates for `fzf`-like fuzzy matching and ranking for the slash-command and `@` file pickers in `steiner`, and which one is the most pragmatic fit for this repo?

## Findings

Top candidates identified:

- `github.com/junegunn/fzf/src/algo`
  - Closest to actual `fzf` behavior and lineage
  - MIT licensed
  - Active upstream and tagged releases
  - Exposes low-level fuzzy scoring functions rather than a simple "rank this slice of strings" API
  - Best match quality if exact `fzf` feel is the top priority

- `github.com/sahilm/fuzzy`
  - Best pragmatic fit for embedding into existing overlays
  - MIT licensed
  - Simple API over slices of strings or source items
  - Returns ranked matches and matched positions
  - Lower integration cost than `fzf` internals

- `github.com/lithammer/fuzzysearch/fuzzy`
  - Easy to wire in
  - MIT licensed
  - Simpler ranking quality than the stronger candidates
  - Likely too basic if the goal is specifically "`fzf`-like"

- `github.com/ktr0731/go-fuzzyfinder`
  - Good if replacing the picker UI wholesale
  - Poor fit if keeping the current Bubble Tea overlays and only improving filtering/ranking

Research recommendation:

- Default recommendation: `github.com/sahilm/fuzzy`
- Technical closest-to-`fzf` fallback: `github.com/junegunn/fzf/src/algo`

## Implications

The plan should not assume that "fuzzy search" means importing `fzf` internals by default. There are two materially different paths:

- pragmatic embed path: use `sahilm/fuzzy` to improve ranking in the current overlay architecture with low integration friction
- highest-fidelity path: use `junegunn/fzf/src/algo` if matching behavior must track `fzf` more closely and the extra adapter work is justified

For this repo, the pragmatic path is a better default because the current TUI already owns rendering, selection, and insertion behavior. The main requirement is better candidate scoring and ordering over in-memory lists, not a replacement picker UI.

## Risks and Uncertainties

- "`fzf`-like" remains partly subjective unless we define a few concrete matching examples during implementation.
- No benchmark was run against actual `steiner` candidate sizes or path distributions.
- `sahilm/fuzzy` is easier to integrate, but it will not be literal `fzf` behavior.
- `junegunn/fzf/src/algo` may produce better feel, but it carries more integration complexity and API risk.

## Sources

- `junegunn/fzf` repository: https://github.com/junegunn/fzf
- `junegunn/fzf/src` package docs: https://pkg.go.dev/github.com/junegunn/fzf/src
- `junegunn/fzf/src/algo` package docs: https://pkg.go.dev/github.com/junegunn/fzf/src/algo
- `sahilm/fuzzy` repository: https://github.com/sahilm/fuzzy
- `lithammer/fuzzysearch` repository: https://github.com/lithammer/fuzzysearch
- `lithammer/fuzzysearch/fuzzy` package docs: https://pkg.go.dev/github.com/lithammer/fuzzysearch/fuzzy
- `ktr0731/go-fuzzyfinder` repository: https://github.com/ktr0731/go-fuzzyfinder

## Open Questions

- Should planning assume the pragmatic default (`sahilm/fuzzy`) unless implementation testing shows the feel is not close enough?
- Do we want to define a small acceptance set of example queries so "`fzf`-like" is testable during implementation?
