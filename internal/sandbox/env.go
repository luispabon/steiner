package sandbox

import "strings"

// allowedEnvVars is the set of exact-match environment variable keys passed through.
var allowedEnvVars = map[string]bool{
	"PATH":            true,
	"HOME":            true,
	"TERM":            true,
	"LANG":            true,
	"TZ":              true,
	"SSH_AUTH_SOCK":   true,
	"EDITOR":          true,
	"VISUAL":          true,
	"SHELL":           true,
	"USER":            true,
	"LOGNAME":         true,
	"XDG_RUNTIME_DIR": true,
}

// FilterEnv filters os.Environ()-style KEY=VALUE pairs through the allowlist.
// HOME passes through unchanged.
func FilterEnv(env []string) []string {
	if env == nil {
		return nil
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if !isAllowed(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// isAllowed reports whether the env var key should be passed through.
func isAllowed(key string) bool {
	if allowedEnvVars[key] {
		return true
	}
	// LC_ prefix match.
	if strings.HasPrefix(key, "LC_") {
		return true
	}
	return false
}
