# Skill Partials

Bundled skills that share instructions (e.g. worktree provisioning, a pre-commit checklist) source that shared prose from a single partial file instead of each skill restating it independently. This prevents drift: duplicated instructions edited in one skill but not another.

## What a partial is

A partial is a plain markdown fragment at `skills/partials/<name>.md`. It holds prose meant to be reused verbatim across more than one bundled skill's `SKILL.md`.

## Include directive syntax

A skill source file (`skills/<name>/SKILL.md.src`) pulls in a partial with a directive line whose entire content is:

```
<!-- include: partials/<name>.md -->
```

The directive must occupy the whole line, with a single space on either side of the path. The path must match `^partials/[a-z0-9]+(-[a-z0-9]+)*\.md$` — lowercase alphanumerics and hyphens only, directly under `partials/`. Paths outside that shape (including anything containing `..` or an absolute path) are rejected.

## Splicing semantics

When `skills/gen` expands a source file, each directive line is replaced by the referenced partial's bytes with exactly one trailing newline removed, if present. This means the newline that originally terminated the directive line now terminates the partial's last line — the partial is spliced in place of the directive, not appended after it. Everything else in the source is left untouched: blank lines, surrounding prose, and the source file's own trailing newline are preserved exactly as written. This precise splicing rule is what makes the generated `SKILL.md` byte-identical to what a human would get by manually pasting the partial's content in place of the directive.

## Build-time only, never at runtime

Partial resolution happens exclusively via `go generate ./...`, which runs `skills/gen` and writes the expanded `SKILL.md` files. The skill loader in `internal/skill` never expands include directives — it loads whatever bytes are already on disk (or embedded) as-is.

This is deliberate: CLAUDE.md forbids introducing per-turn non-determinism into the prompt prefix, and skills are part of that static prefix. Resolving includes at runtime would mean the prefix depends on filesystem reads happening at request time, and it would need to re-run on every load, undermining the memoized, cache-stable prompt prefix the rest of the system depends on. Resolving at build time keeps the bundled `SKILL.md` files themselves as the stable, cacheable artifact.

## Bundled-only scope

Only the committed `skills/` tree bundled into the binary goes through this mechanism. User-authored skills loaded at runtime from `RootDirs` are completely unaffected — the loader has no knowledge of include directives, so a user skill containing a line that looks like `<!-- include: ... -->` is loaded as literal content, not expanded.

## Generated files must not be hand-edited

Any skill with a `SKILL.md.src` sibling has its `SKILL.md` generated from that source (and any partials it includes). Edit `SKILL.md.src` or the relevant partial, then run `go generate ./...` to regenerate `SKILL.md` — never edit the generated `SKILL.md` directly, since those edits are silently overwritten on the next generate.

Each generated skill directory also has a `SKILL.md.generated` marker file that names this rule, so it's visible from a directory listing without opening `SKILL.md` itself.

`make generate-check` is the CI backstop: it runs `go generate ./...` and fails the build if that produces a diff, catching a `.src` or partial edit that wasn't followed by regeneration. It's wired into `make check`.

## Partials cannot nest

An include directive found inside a partial's own content is an error at generation time, not a recursive expansion. Partials are meant to stay flat, single-level fragments.

## Which skills use this

Only skills that consume partials have a `SKILL.md.src` file. Currently that's `implement`, `review`, and `simplify`. Skills with no `.src` file — `plan` and `pull-request` — are hand-authored `SKILL.md` files with no generation step involved; nothing in this document applies to them.

## Safety net: golden tests

`skills/goldens_test.go` (`TestBundledSkillsMatchGoldens`) loads each generated skill through the same `internal/skill` loader used at runtime and compares its content byte-for-byte against a checked-in golden file under `skills/testdata/goldens/*.golden.md`. This is the safety net that proves the partial mechanism doesn't silently alter what the model actually sees — any unintended change to splicing behavior, whitespace handling, or partial content shows up as a golden test failure rather than a subtle prompt drift.
