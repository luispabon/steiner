package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// testMCPServerConfig holds optional knobs for newTestMCPServer.
type testMCPServerConfig struct {
	// observe wraps the SDK Streamable HTTP handler to observe or intercept
	// incoming requests (e.g. header recording, dropped sessions).
	observe func(http.Handler) http.Handler
	// errorTool, when non-empty, registers an additional tool with that name
	// that always returns an MCP isError result.
	errorTool string
}

// testMCPServerOption configures newTestMCPServer.
type testMCPServerOption func(*testMCPServerConfig)

// withRequestObservation wraps the SDK Streamable HTTP handler with wrap so
// tests can observe or intercept incoming requests.
func withRequestObservation(wrap func(http.Handler) http.Handler) testMCPServerOption {
	return func(c *testMCPServerConfig) {
		c.observe = wrap
	}
}

// withErrorTool registers an additional tool named name that always returns an
// MCP isError result, so tests can exercise the client's tool-error envelope
// path over HTTP. The default tools and other options are unaffected.
func withErrorTool(name string) testMCPServerOption {
	return func(c *testMCPServerConfig) {
		c.errorTool = name
	}
}

// newTestMCPServer creates a loopback HTTP MCP test server with a single
// "test_tool" that echoes its "text" argument, so tool defs surface as
// mcp__remote__test_tool. Options may add an isError tool or wrap the Streamable
// HTTP handler to observe or intercept requests. The server is closed via
// t.Cleanup.
func newTestMCPServer(t *testing.T, opts ...testMCPServerOption) *httptest.Server {
	t.Helper()
	server := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "test", Version: "1.0"},
		nil,
	)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "test_tool",
		Description: "echoes text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, args struct {
		Text string `json:"text"`
	}) (*mcpsdk.CallToolResult, any, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: args.Text},
			},
		}, nil, nil
	})

	var handler http.Handler = mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return server
	}, nil)

	cfg := testMCPServerConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.errorTool != "" {
		mcpsdk.AddTool(server, &mcpsdk.Tool{
			Name:        cfg.errorTool,
			Description: "always fails",
			InputSchema: map[string]any{"type": "object"},
		}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: "deliberate failure"},
				},
				IsError: true,
			}, nil, nil
		})
	}
	if cfg.observe != nil {
		handler = cfg.observe(handler)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(func() { ts.Close() })
	return ts
}
