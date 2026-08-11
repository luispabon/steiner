### Pre-Commit Checklist

Include the appropriate checklist verbatim in every delegated task that commits. The sub-agent must run all checks before `git commit`.

**Isolated delegation mode:**

1. `git branch --show-current` — must equal the temporary branch name given in the task. If it shows the feature branch, STOP and report without committing.
2. `git rev-parse --show-toplevel` — must equal the worktree path given in the task. If it shows a different path, STOP and report without committing.
3. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.

**Direct delegation mode:**

1. `git branch --show-current` — must equal the feature branch name given in the task. If it shows a different branch, STOP and report without committing.
2. `git status` — must show only files within the declared scope as modified. If unexpected files appear, STOP and report.
