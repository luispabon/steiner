## Question

How do popular AI coding agents and chat tools handle conversation forking/branching UX?

## Findings

### ChatGPT (only tool with real forking)
- **Trigger**: Click edit icon on any past message → creates a new branch with edited history
- **Post-fork**: Auto-switches to the forked branch
- **Visual indicator**: Tree/carousel in sidebar showing branches
- **Scope**: Fork from any point in current session only (not saved sessions)
- **Naming**: Auto-generated, not user-provided

### Claude Code
- Sequential sessions, no in-session forking
- Has `/resume` to load saved sessions but no fork/duplicate

### Aider
- Terminal-based, no multi-branch support
- Users manually copy context to new sessions

### Cursor / Windsurf / Continue.dev
- No documented conversation forking features

## Implications

- Forking is **differentiating** — no CLI coding agent offers it
- Steiner's file-based session store (`internal/session`) makes forking straightforward: deep-copy lineage into new session
- ChatGPT's "Edit and Branch" is the closest prior art but is edit-triggered; steiner's `/fork` is a more explicit, intentional action
- Terminal UX cannot use visual trees — status messages and clear titles are the right indicator
- Supporting fork from both live session AND saved sessions (via picker) is unique and valuable

## Risks and Uncertainties

- Cursor's feature set is proprietary; may have undocumented forking
- Demand for forking may be niche in CLI contexts, but the issue author clearly wants it
- Prompt cache hit maximization depends on keeping the same model and identical message prefix — fork achieves this naturally since the forked session shares the same message history

## Sources

- ChatGPT help center (conversation branching documentation)
- Aider, Claude Code, Cursor, Continue.dev GitHub repositories and documentation
- Steiner codebase (`internal/session`, `internal/interactive`)

## Open Questions

- Should forked session title include lineage info (e.g. "Fork of: <parent title>")?
- Should there be a limit on how many times a session can be forked?
- Future: could fork-from-message (ChatGPT style) be added later as an extension?
