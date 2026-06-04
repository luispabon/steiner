# Sandbox Stress Test

Run every test below using the bash tool. Record results in a file called `sandbox_test_results.md` in the workspace root. For each test, record: test name, command run, expected outcome, actual outcome, and PASS/FAIL.

Do NOT skip any test. If a command fails, record the exact error — do not retry or work around it.

## 1. CWD baseline

Run `pwd` as your very first bash command. Expect: prints the workspace absolute path with no preceding error or diagnostic line. This validates --chdir and that no cd error corrupts output.

## 2. First-line capture

Run `echo "FIRST"; echo "SECOND"`. Expect: both lines present in output. If FIRST is missing, the old stdout-swallowing bug is back.

## 3. Multi-line output integrity

Run `seq 1 20`. Expect: 20 lines, 1 through 20, nothing missing or mangled.

## 4. System tools accessible

Run each of these and record whether it succeeds or fails:
- `which ls && ls /`
- `which cat && cat /etc/os-release | head -5`
- `which grep && grep -c . /etc/passwd`
- `which find && find /usr/bin -maxdepth 0 -type d`

## 5. Toolchain accessibility

Run each. Record version or "not installed" — both are valid. A "command not found" for an installed tool means the sandbox is missing paths:
- `go version`
- `rustc --version 2>/dev/null || echo "not installed"`
- `python3 --version 2>/dev/null || echo "not installed"`
- `node --version 2>/dev/null || echo "not installed"`
- `gcc --version 2>/dev/null | head -1 || echo "not installed"`
- `git --version`

## 6. HOME passthrough

Run `echo $HOME`. Expect: the real user home path (e.g. /home/luis), NOT /home/steiner.

Run `ls -la ~ | head -5`. Expect: lists the real home directory contents (read-only is fine).

## 7. Workspace is writable

Run `touch sandbox_write_test && echo "write ok" && rm sandbox_write_test`. Expect: succeeds with no permission error.

## 8. Write outside workspace — must fail

Run each and expect a permission denied or read-only filesystem error:
- `touch /tmp/outside_test_file && echo "FAIL: /tmp writable" || echo "PASS: /tmp write blocked"` — wait, /tmp IS writable (private tmpfs). Change this: expect success.
- `touch /etc/test_file 2>&1 || echo "PASS: /etc write blocked"`
- `touch ~/outside_home_test 2>&1 || echo "PASS: home write blocked"`
- `mkdir /opt/sandbox_test 2>&1 || echo "PASS: /opt write blocked"`

## 9. /tmp is private tmpfs

Run `mount | grep "on /tmp"` or `df /tmp`. Expect: shows tmpfs, confirming /tmp is isolated.

Then: `echo "hello" > /tmp/test_file && cat /tmp/test_file && rm /tmp/test_file`. Expect: succeeds — /tmp is writable.

## 10. /proc and /dev functional

- `ls /proc/self/status | head -1` — expect: succeeds
- `cat /proc/self/status | head -3` — expect: shows process info
- `ls /dev/null /dev/zero /dev/urandom` — expect: all exist

## 11. Env var filtering

Run `env | sort` and check:
- PATH is set (present)
- HOME is set to real home path
- TERM is set
- Any of these should be ABSENT: AWS_SECRET_ACCESS_KEY, GITHUB_TOKEN, ANTHROPIC_API_KEY

Run `echo "AWS=$AWS_SECRET_ACCESS_KEY GH=$GITHUB_TOKEN API=$ANTHROPIC_API_KEY"`. Expect: all empty.

## 12. Credential files readable (ro via root bind)

Run each — expect success (readable) or "file not found" (user doesn't have them), but NOT permission denied:
- `test -r ~/.ssh/id_rsa 2>/dev/null && echo "readable" || echo "not found or not readable"`
- `test -r ~/.gitconfig 2>/dev/null && echo "readable" || echo "not found or not readable"`
- `test -d ~/.aws 2>/dev/null && echo "exists" || echo "not found"`

## 13. Credential files NOT writable

If ~/.gitconfig exists: `echo "test" >> ~/.gitconfig 2>&1 || echo "PASS: gitconfig not writable"`. Expect: fails.

If ~/.ssh exists: `touch ~/.ssh/sandbox_test 2>&1 || echo "PASS: .ssh not writable"`. Expect: fails.

## 14. Network access (shared net namespace)

Run `curl -s -o /dev/null -w "%{http_code}" https://example.com 2>/dev/null || wget -q -O /dev/null https://example.com 2>/dev/null && echo "network ok" || echo "no network tools"`.

## 15. Process isolation

Run `ps aux 2>/dev/null | wc -l || echo "ps not available"`. Expect: very few processes (sandbox has fresh PID namespace).

## 16. Path identity — no remapping

Run all three and confirm paths are real host paths, not /workspace or /home/steiner:
- `pwd` — must NOT be /workspace
- `echo $HOME` — must NOT be /home/steiner
- `ls -d .steiner/home 2>/dev/null && echo "sandbox home exists at $(realpath .steiner/home)"` — path must be under the real workspace

## 17. Git operations inside sandbox

- `git status` — expect: works, shows repo status
- `git log --oneline -3` — expect: works, shows recent commits
- `git config user.name` — expect: shows the real user's git config (readable through root bind)

## 18. Subshell and pipe stress

- `bash -c 'echo "subshell works"'`
- `echo "hello" | tr 'a-z' 'A-Z'`
- `for i in 1 2 3; do echo "iter $i"; done`
- `cat <(echo "process substitution")`

## Summary

After all tests, add a summary section at the bottom of `sandbox_test_results.md`:
- Total tests run
- Total PASS
- Total FAIL
- List of all FAIL test names with their error output
