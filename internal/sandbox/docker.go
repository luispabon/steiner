package sandbox

import (
	"os"
	"path/filepath"
)

// dockerSocketCandidates lists host paths that may expose the Docker daemon.
// Replaceable in tests.
var dockerSocketCandidates = defaultDockerSocketCandidates

// defaultDockerSocketCandidates returns the well-known Docker socket paths:
// the system daemon and, when set, the rootless per-user daemon under
// XDG_RUNTIME_DIR (which is allowlisted through to the sandbox, see env.go).
func defaultDockerSocketCandidates() []string {
	candidates := []string{"/run/docker.sock"}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidates = append(candidates, filepath.Join(runtimeDir, "docker.sock"))
	}
	return candidates
}

// dockerDenyArgs returns bwrap arguments that deny sandboxed access to the
// Docker daemon: masking every reachable candidate socket and unsetting
// DOCKER_HOST (which covers the TCP-endpoint case no bind can reach).
//
// A candidate is masked only when it exists on the host. bwrap's --bind
// requires an existing destination; emitting a mask for a missing one aborts
// sandbox startup for every tool invocation, not just Docker ones.
func dockerDenyArgs(candidates []string) []string {
	args := []string{"--unsetenv", "DOCKER_HOST"}

	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		resolved := candidate
		if r, err := filepath.EvalSymlinks(candidate); err == nil {
			resolved = r
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		args = append(args, "--bind", "/dev/null", resolved)
	}

	return args
}
