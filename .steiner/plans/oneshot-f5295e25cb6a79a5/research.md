## Question

How can diagnostics writers coordinate appends, rotation, and retention safely across release platforms and process termination?

## Findings

- A stable sibling lock protects the path while data files are replaced. Unix `flock` is advisory and associated with an open file description; deleting the lock file can split lock domains. Locks release on process termination, but a multi-rename rotation is not atomic.
- Windows `LockFileEx` provides exclusive byte-range locking. Open handle sharing and rename behavior require actual runtime tests. Go does not guarantee atomic `os.Rename` on non-Unix platforms.
- Filesystem locking and append behavior can differ on network filesystems. Exclusion is not power-loss durability: syncing files and directories has distinct platform semantics.

## Implications

Use one persistent lock path per stream, held across the complete read-modify-write operation. Fail closed with a warning on unsupported locking. Add restart recovery for each rotation interruption point and exercise separate processes and supported platforms.

## Risks and Uncertainties

External noncooperating writers, unreliable network filesystems, and power loss are outside the supported guarantee. Actual macOS and Windows runtime validation may require external runners.

## Sources

- https://man7.org/linux/man-pages/man2/flock.2.html
- https://man7.org/linux/man-pages/man2/rename.2.html
- https://man7.org/linux/man-pages/man2/fsync.2.html
- https://man7.org/linux/man-pages/man2/open.2.html
- https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/flock.2.html
- https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex
- https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew
- https://pkg.go.dev/os#Rename

## Claude window semantics

Anthropic describes the normal five-hour limit as a session beginning with the first message and resetting in full five hours later. This supports an inferred window start, not a verified zero-valued observation or a guarantee for all plans and exceptional resets. Enterprise descriptions also use rolling-window language, and early manual resets exist. Sources: https://support.claude.com/en/articles/11647753-how-do-usage-and-length-limits-work, https://support.claude.com/en/articles/14552983-models-usage-and-limits-in-claude-code, https://support.claude.com/en/articles/17007452-what-is-a-limit-reset.

## Open Questions

Validate the rotation recovery protocol on actual Windows and macOS systems; cross-compilation is insufficient.
