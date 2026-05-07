## `apply_patch`

Use the `apply_patch` shell command to edit files. Your patch language is a stripped-down, file-oriented diff format designed to be easy to parse and safe to apply. You can think of it as a high-level envelope:

*** Begin Patch
[ one or more file sections ]
*** End Patch

Within that envelope, you get a sequence of file operations. You MUST include a header to specify the action you are taking.

Each operation starts with one of three headers:
- `*** Add File:` - create a new file.
- `*** Delete File:` - remove an existing file.
- `*** Update File:` - patch an existing file in place (optionally with a rename).

For update operations you may include `*** Move to:` and one or more hunks introduced by `@@`.

The full grammar is documented upstream in the Codex repository.
