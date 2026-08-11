### Worktree Provisioning

Always create worktrees under `.steiner/worktrees/` inside the project root. Do not use `/tmp` or other system temporary directories — they may be sandboxed and silently fail.

After running `git worktree add`, verify the directory actually exists:

1. Run `ls -d <worktree-path>` to confirm the directory was created.
2. Run `git -C <worktree-path> branch --show-current` to confirm it is on the expected temporary branch.
3. If either check fails, prune the worktree entry with `git worktree remove <worktree-path>` and fall back to direct delegation.
