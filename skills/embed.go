package skills

import "embed"

//go:embed */SKILL.md

// FS holds the embedded skill files bundled at compile time.
var FS embed.FS
