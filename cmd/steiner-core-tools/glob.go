package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type globRequest struct {
	Pattern string `json:"pattern"`
}

func runGlob(ctx context.Context, payload []byte) (any, error) {
	_ = ctx

	req, err := decodeRequest[globRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "pattern is required",
		}
	}

	matches, err := filepath.Glob(req.Pattern)
	if err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "glob_error",
			Message: err.Error(),
			Details: map[string]any{"pattern": req.Pattern},
		}
	}
	sort.Strings(matches)

	return map[string]any{
		"pattern": req.Pattern,
		"matches": matches,
	}, nil
}
