package builtin

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/tool"
)

// NewWebSearchTool creates a ToolDef for the web_search tool backed by any web.Searcher.
func NewWebSearchTool(searcher web.Searcher) tool.ToolDef {
	return tool.ToolDef{
		Name:            "web_search",
		Description:     "Search the web for information. Returns results with URL, title, and description.",
		ParameterSchema: WebSearchSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[WebSearchInput](input)
			if err != nil {
				return nil, fmt.Errorf("web_search: %w", err)
			}

			NormalizeWebSearch(&in)

			if in.Query == "" {
				return nil, fmt.Errorf("web_search: query is required")
			}

			result, err := searcher.Search(ctx, &web.SearchInput{
				Query: in.Query,
				Limit: in.Limit,
			})
			if err != nil {
				return map[string]any{
					"error": err.Error(),
				}, nil
			}

			items := make([]map[string]string, len(result.Items))
			for i, item := range result.Items {
				items[i] = map[string]string{
					"url":         item.URL,
					"title":       item.Title,
					"description": item.Description,
				}
			}

			return items, nil
		},
	}
}
