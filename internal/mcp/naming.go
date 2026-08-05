package mcp

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	namePrefix = "mcp__"
	nameDelim  = "__"
	maxNameLen = 64
	hashLen    = 8
)

// ToolName returns the registry name for a tool discovered on an MCP server.
// It is a pure function of (server, tool) and must never depend on which other
// servers connected, so names stay stable when a server fails to connect.
func ToolName(server, tool string) string {
	// Step 1: sanitise each segment independently.
	// Every rune outside [A-Za-z0-9_-] becomes '_'. Iterate runes, not bytes.
	sSan, sChanged := sanitise(server)
	tSan, tChanged := sanitise(tool)

	// Step 2: compose.
	composed := namePrefix + sSan + nameDelim + tSan

	// Step 3: if neither segment was altered AND length <= 64, return as-is.
	if !sChanged && !tChanged && len(composed) <= maxNameLen {
		return composed
	}

	// Step 4: hash over the ORIGINAL unsanitised inputs, take first 8 hex chars.
	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	suffix := hex.EncodeToString(sum[:])[:hashLen]

	// Reserve = len("mcp__") + len("__") + len("__") + 8 = 17
	// Budget = 64 - 17 = 47
	const budget = maxNameLen - 17      // 47
	const serverBudget = budget / 2     // 23
	toolBudget := budget - serverBudget // 24

	if len(sSan) > serverBudget {
		sSan = truncateRunes(sSan, serverBudget)
	}
	if len(tSan) > toolBudget {
		tSan = truncateRunes(tSan, toolBudget)
	}

	return namePrefix + sSan + nameDelim + tSan + nameDelim + suffix
}

// sanitise replaces every rune outside [A-Za-z0-9_-] with '_'.
// Returns the sanitised string and whether any rune was changed.
func sanitise(s string) (string, bool) {
	changed := false
	runes := []rune(s)
	for i, r := range runes {
		if !isAllowed(r) {
			runes[i] = '_'
			changed = true
		}
	}
	return string(runes), changed
}

func isAllowed(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}

// truncateRunes truncates s to at most n runes.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
