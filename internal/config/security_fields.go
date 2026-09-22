package config

import "strings"

// securityFieldPatterns lists dotted path patterns whose override is
// security-relevant. "*" matches exactly one path segment (a map key).
// A leaf matches when the pattern is a prefix of the leaf's segments, or the
// leaf is a proper ancestor of the pattern (e.g. a whole "sandbox: {}"
// mapping override).
var securityFieldPatterns = []string{
	"sandbox", "permissions",
	"paths.writable_paths", "paths.blocked_paths", "paths.project_root_only",
	"mcp.servers.*.transport", "mcp.servers.*.command", "mcp.servers.*.args",
	"mcp.servers.*.env", "mcp.servers.*.url", "mcp.servers.*.headers",
	"mcp.servers.*.approval", "mcp.servers.*.trust_annotations",
	"lsp.servers.*.command", "lsp.servers.*.args", "lsp.servers.*.env",
	"tools.*.exec", "tools.*.subcommand",
	"providers.*.base_url", "providers.*.headers", "providers.*.api_key",
	"providers.*.api_key_env",
}

// isSecurityPath reports whether path (a dotted YAML path) is covered by
// securityFieldPatterns, either directly or as an ancestor of a covered path.
func isSecurityPath(path string) bool {
	pathSegs := strings.Split(path, ".")
	for _, pattern := range securityFieldPatterns {
		if segmentsOverlap(strings.Split(pattern, "."), pathSegs) {
			return true
		}
	}
	return false
}

// segmentsOverlap reports whether pattern and segs agree on every position up
// to the shorter of the two, treating "*" in pattern as a wildcard for a
// single segment.
func segmentsOverlap(pattern, segs []string) bool {
	n := len(pattern)
	if len(segs) < n {
		n = len(segs)
	}
	for i := 0; i < n; i++ {
		if pattern[i] != "*" && pattern[i] != segs[i] {
			return false
		}
	}
	return true
}
