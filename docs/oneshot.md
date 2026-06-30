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

The `oneshot` config block controls per-phase model aliases and PR closeout behavior:

```yaml
oneshot:
  auto_pr: false                  # push branch and open PR/MR on passing review
  models:
    plan: ""                       # model alias override for plan phase (empty = default_model)
    implement: ""                  # model alias override for implement phase
    review: ""                     # model alias override for review phase
```

- `models.*` are optional; omit to use `default_model` at runtime.
- `auto_pr` is optional; defaults to `false`.

For architecture, manifest schema, and phase contracts, see [Oneshot Internals](oneshot-internals.md).
