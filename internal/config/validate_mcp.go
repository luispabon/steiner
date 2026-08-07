package config

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func validateMCPConfig(problems *[]string, cfg MCPConfig) {
	for name, srv := range cfg.Servers {
		if name == "" {
			*problems = append(*problems, "mcp.servers contains an empty server name")
			continue
		}
		if strings.Contains(name, "__") {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s: server name must not contain \"__\" (the naming delimiter)", name))
		}

		// Normalize empty transport to "stdio" for validation, matching applyMCPDefaults behavior.
		transport := srv.Transport
		if transport == "" {
			transport = "stdio"
		}

		// Validate transport value.
		if transport != "stdio" && transport != "http" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.transport: %q is not supported (must be stdio or http)", name, srv.Transport))
		}

		// Per-transport field validation and URL validation.
		validateMCPTransportFields(problems, name, transport, srv)
		validateMCPURL(problems, name, srv.URL)

		// Validate header names.
		validateMCPHeaders(problems, name, srv.Headers)

		// Validate approval value.
		if srv.Approval != "" && srv.Approval != "ask" && srv.Approval != "allow" && srv.Approval != "deny" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.approval: %q is invalid (must be ask, allow, or deny)", name, srv.Approval))
		}

		// Reject negative connect timeouts. Zero (absent or explicit) means
		// unset and is defaulted to 15s by applyMCPDefaults.
		if srv.ConnectTimeout.Duration() < 0 {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.connect_timeout must be non-negative", name))
		}
	}
}

func validateMCPTransportFields(problems *[]string, name, transport string, srv MCPServerConfig) {
	switch transport {
	case "stdio":
		if srv.Command == "" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.command is required", name))
		}
		if srv.URL != "" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.url: must be empty for stdio transport", name))
		}
		if len(srv.Headers) > 0 {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: must be empty for stdio transport", name))
		}
	case "http":
		if srv.URL == "" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.url is required", name))
		}
		if srv.Command != "" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.command: must be empty for http transport", name))
		}
		if len(srv.Args) > 0 {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.args: must be empty for http transport", name))
		}
		if len(srv.Env) > 0 {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.env: must be empty for http transport", name))
		}
	}
}

func validateMCPURL(problems *[]string, name, rawURL string) {
	if rawURL == "" {
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.url: %v", name, err))
		return
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.url: unsupported scheme %q (must be http or https)", name, parsed.Scheme))
	}
	if parsed.Host == "" {
		*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.url: must have a non-empty host", name))
	}
}

func validateMCPHeaders(problems *[]string, serverName string, headers map[string]string) {
	sdkReservedHeaders := map[string]bool{
		"Content-Type":         true,
		"Accept":               true,
		"Mcp-Protocol-Version": true,
		"Mcp-Session-Id":       true,
		"Last-Event-Id":        true,
		"Mcp-Method":           true,
		"Mcp-Name":             true,
	}

	for name, value := range headers {
		if name == "" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: header name must not be empty", serverName))
			continue
		}

		// Check for invalid HTTP token characters.
		if strings.ContainsAny(name, " :\n") {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: header name %q contains invalid characters (space, colon, or newline)", serverName, name))
			continue
		}

		// Check if it's a valid HTTP token.
		if !isValidHTTPToken(name) {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: header name %q contains invalid characters", serverName, name))
			continue
		}

		// Check for SDK-reserved headers (case-insensitive).
		canonical := http.CanonicalHeaderKey(name)
		if sdkReservedHeaders[canonical] {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: header %q is reserved by the SDK", serverName, name))
		}

		// Check for Mcp-Param- prefix (case-insensitive).
		if strings.HasPrefix(strings.ToLower(canonical), "mcp-param-") {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: header %q uses reserved Mcp-Param- prefix", serverName, name))
		}

		// Check for CR/LF in header value.
		if strings.ContainsAny(value, "\r\n") {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.headers: header %q has a value containing a newline or carriage return", serverName, name))
		}
	}
}

// isValidHTTPToken checks if a string is a valid HTTP token per RFC 7230.
// Tokens consist of 1*tchar where tchar = "!" / "#" / "$" / "%" / "&" / "'" / "*" / "+" / "-" / "." / "^" / "_" / "`" / "|" / "~" / DIGIT / ALPHA
func isValidHTTPToken(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

const httpTokenExtraChars = "!#$%&'*+-.^_`|~"

func isTokenChar(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z':
		return true
	default:
		return strings.IndexByte(httpTokenExtraChars, b) >= 0
	}
}
