package builtin

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/tool"
)

type webSearchResultItem struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// NewWebSearchTool creates a ToolDef for the web_search tool backed by any web.Searcher.
func NewWebSearchTool(searcher web.Searcher) tool.ToolDef {
	return tool.ToolDef{
		Name:            "web_search",
		ParallelSafe:    true,
		Description:     "Search the web for information. Returns results with URL, title, and description.",
		ParameterSchema: WebSearchSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[WebSearchInput](input)
			if err != nil {
				return nil, fmt.Errorf("web_search: %w", err)
			}

			normalizeWebSearch(&in)

			if in.Query == "" {
				return nil, fmt.Errorf("web_search: query is required")
			}

			result, err := searcher.Search(ctx, &web.SearchInput{
				Query: in.Query,
				Limit: in.Limit,
			})
			if err != nil {
				return nil, fmt.Errorf("web_search: %w", err)
			}

			items := make([]webSearchResultItem, len(result.Items))
			for i, item := range result.Items {
				items[i] = webSearchResultItem{
					URL:         item.URL,
					Title:       item.Title,
					Description: item.Description,
				}
			}

			return items, nil
		},
	}
}
