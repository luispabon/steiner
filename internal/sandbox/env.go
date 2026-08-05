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

	// Proxy
	"HTTP_PROXY":  true,
	"HTTPS_PROXY": true,
	"FTP_PROXY":   true,
	"NO_PROXY":    true,
	"http_proxy":  true,
	"https_proxy": true,
	"ftp_proxy":   true,
	"no_proxy":    true,

	// TLS trust
	"SSL_CERT_FILE":       true,
	"SSL_CERT_DIR":        true,
	"CURL_CA_BUNDLE":      true,
	"REQUESTS_CA_BUNDLE":  true,
	"NODE_EXTRA_CA_CERTS": true,
	"GIT_SSL_CAINFO":      true,

	// Go
	"GOFLAGS":     true,
	"GOPROXY":     true,
	"GOPRIVATE":   true,
	"GOSUMDB":     true,
	"GONOSUMDB":   true,
	"GOTOOLCHAIN": true,
	"GOPATH":      true,
	"GOCACHE":     true,
	"GOMODCACHE":  true,

	// Rust
	"CARGO_HOME":  true,
	"RUSTUP_HOME": true,

	// Node
	"NODE_OPTIONS": true,

	// Python
	"PYTHONPATH":  true,
	"VIRTUAL_ENV": true,
	"PYENV_ROOT":  true,

	// Java
	"JAVA_HOME": true,

	// XDG
	"XDG_CONFIG_HOME": true,
	"XDG_DATA_HOME":   true,
	"XDG_CACHE_HOME":  true,
	"XDG_STATE_HOME":  true,

	// Terminal
	"COLORTERM":    true,
	"NO_COLOR":     true,
	"TERM_PROGRAM": true,
}

// EnvPolicy decides which host environment variables cross the sandbox boundary.
type EnvPolicy struct {
	// PassthroughAll disables filtering entirely: the host environment is
	// passed to sandboxed processes verbatim, including credentials.
	PassthroughAll bool
	// Extra names allowed in addition to the built-in list. An entry ending in
	// "*" matches by prefix (e.g. "MYAPP_*").
	Extra []string
}

// newEnvPolicy builds an EnvPolicy from configured values, trimming
// surrounding whitespace from each Extra entry so that validation
// (internal/config's validateSandboxConfig) and matching (isAllowed) agree on
// the same strings.
func newEnvPolicy(passthroughAll bool, extra []string) EnvPolicy {
	trimmed := make([]string, len(extra))
	for i, e := range extra {
		trimmed[i] = strings.TrimSpace(e)
	}
	return EnvPolicy{PassthroughAll: passthroughAll, Extra: trimmed}
}

// FilterEnv filters os.Environ()-style KEY=VALUE pairs through the allowlist,
// as extended by policy. HOME passes through unchanged.
//
// A nil env means "no environment" and is returned as nil unchanged — this is
// not "inherit the host environment". Callers that want host inheritance must
// pass os.Environ() explicitly; conflating the two is what let the sandbox's
// allowlist go unenforced for every built-in tool before this was fixed.
func FilterEnv(env []string, policy EnvPolicy) []string {
	if env == nil {
		return nil
	}
	if policy.PassthroughAll {
		out := make([]string, len(env))
		copy(out, env)
		return out
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if !isAllowed(key, policy.Extra) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// isAllowed reports whether the env var key should be passed through, given
// the extra allowlist entries from policy.
func isAllowed(key string, extra []string) bool {
	if allowedEnvVars[key] {
		return true
	}
	// LC_ prefix match.
	if strings.HasPrefix(key, "LC_") {
		return true
	}
	for _, e := range extra {
		if prefix, ok := strings.CutSuffix(e, "*"); ok {
			if strings.HasPrefix(key, prefix) {
				return true
			}
			continue
		}
		if key == e {
			return true
		}
	}
	return false
}
