# Oneshot Mode

Oneshot is a headless autonomous orchestration mode that runs steiner's agent loop as three distinct phases — plan, implement, review — without user interaction. Each phase is a fresh agent run with empty model context against a dedicated git worktree forked from `origin/main`. Results are committed to a feature branch, and optionally pushed as a pull request.

## Invocation

**Headless CLI**:
```bash
steiner oneshot "<task>"          # run end-to-end
steiner oneshot --resume <id>     # resume an interrupted run
steiner oneshot --list            # list resumable runs
```

**Interactive TUI** (from within `steiner`):
```
/oneshot <task>
/oneshot --resume <id>
/oneshot --list
```

## Configuration

The `oneshot` config block controls PR closeout behavior; per-phase model aliases live under `models.oneshot`:

```yaml
oneshot:
  auto_pr: false                  # push branch and open PR/MR on passing review

models:
  oneshot:
    plan: ""                       # model alias override for plan phase (empty = models.default)
    implement: ""                  # model alias override for implement phase
    review: ""                     # model alias override for review phase
```

- `models.oneshot.*` are optional; omit to use `models.default` at runtime.
- `auto_pr` is optional; defaults to `false`.

## Listing and Resumable Runs

`steiner oneshot --list` displays all resumable runs:

```
  ID          Slug               Status      Phase
  abc123      refactor-auth      incomplete  implement
  def456      add-logging        completed   review
  ghi789      fix-bug-xyz        interrupted implement
```

A run is resumable if:
- The manifest exists and is readable
- The branch exists (remote or local)
- At least one phase is incomplete
- The lock is either absent or stalable

A run is listed as `completed` if all three phases are marked complete in the manifest.

## Error Handling

If a phase fails:

1. The failure is recorded in the manifest with a timestamp.
2. SIGINT aborts gracefully without re-running previous phases.
3. The user can inspect the worktree, fix issues, and resume with `--resume <id>`.

If a resume attempt fails (e.g., the worktree path is corrupted), the user receives a clear error message with the run ID and branch name, allowing manual recovery via `git checkout <branch>` and cleanup of the worktree.

For architecture, manifest schema, and phase contracts, see [Oneshot Internals](oneshot-internals.md).
