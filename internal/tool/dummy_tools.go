package tool

import (
	"context"

	"github.com/luispabon/steiner/internal/config"
)

// DummyWebSearchTool returns a ToolDef for the web_search stub.
func DummyWebSearchTool() ToolDef {
	return ToolDef{
		Name:        "web_search",
		Description: "Search the web for information.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query."},
			},
			"required": []any{"query"},
		},
		Approval: config.ApprovalModeAuto,
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return map[string]any{
				"ok":    false,
				"error": "web_search is not yet implemented",
			}, nil
		},
	}
}

// DummyFetchURLTool returns a ToolDef for the fetch_url stub.
func DummyFetchURLTool() ToolDef {
	return ToolDef{
		Name:        "fetch_url",
		Description: "Fetch content from a URL.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "URL to fetch."},
			},
			"required": []any{"url"},
		},
		Approval: config.ApprovalModeAuto,
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return map[string]any{
				"ok":    false,
				"error": "fetch_url is not yet implemented",
			}, nil
		},
	}
}
