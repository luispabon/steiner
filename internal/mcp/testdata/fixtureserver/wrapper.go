package fixtureserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Env is the environment variable that makes a test binary act as the fixture
// server (by calling Main from its TestMain) instead of running tests.
const Env = "STEINER_MCP_FIXTURE_SERVER"

// WriteWrapper writes an executable shell script named name into dir that
// re-execs the binary at exe with envVar=1 and returns its path. Callers use
// os.Executable() for exe, or a path valid inside a sandbox. exec keeps the
// PID, so process-reaping tests see the helper itself rather than a shell. The
// race runtime's exit sleep is disabled for the helper: it has no late race
// reports worth waiting for and would otherwise add ~100ms to every spawn.
func WriteWrapper(dir, name, envVar, exe string) (string, error) {
	quoted := "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nGORACE=\"$GORACE atexit_sleep_ms=0\" " + envVar + "=1 exec " + quoted + ` "$@"` + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // wrapper must be executable
		return "", fmt.Errorf("write wrapper: %w", err)
	}
	return path, nil
}
