package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	steinertool "github.com/luispabon/steiner/internal/tool"
)

type searchRequest struct {
	Query string `json:"query"`
}

type searchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func runSearch(ctx context.Context, payload []byte) (any, error) {
	_ = ctx

	req, err := decodeRequest[searchRequest](payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "invalid_input",
			Message: "query is required",
		}
	}

	var matches []searchMatch
	err = filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		line := 1
		for scanner.Scan() {
			text := scanner.Text()
			if strings.Contains(text, req.Query) {
				matches = append(matches, searchMatch{
					Path: path,
					Line: line,
					Text: text,
				})
			}
			line++
		}
		return scanner.Err()
	})
	if err != nil {
		return nil, &steinertool.JSONEnvelopeError{
			Kind:    "search_error",
			Message: err.Error(),
			Details: map[string]any{"query": req.Query},
		}
	}

	return map[string]any{
		"query":   req.Query,
		"matches": matches,
	}, nil
}
