package builtin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/deepnoodle-ai/dive/toolkit"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// NewGrepTool creates a ToolDef for the grep tool backed by Dive's GrepTool.
func NewGrepTool(env Env) tool.ToolDef {
	grepTool := toolkit.NewGrepTool()
	return tool.ToolDef{
		Name:            "grep",
		Description:     `Search file contents. Use output_mode="files_with_matches" to locate relevant files. Use output_mode="content" with context to inspect matches. Use offset to paginate large results.`,
		ParameterSchema: GrepSchema(),
		Approval:        config.ApprovalModeAuto,
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[GrepInput](input)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			NormalizeGrep(&in)

			if in.OutputMode == "" {
				in.OutputMode = "content"
			}
			if in.LineNumbers == nil {
				v := true
				in.LineNumbers = &v
			}
			if in.Context > 0 {
				in.BeforeContext = in.Context
				in.AfterContext = in.Context
			}

			validModes := map[string]bool{
				"content":            true,
				"files_with_matches": true,
				"count":              true,
			}
			if !validModes[in.OutputMode] {
				return nil, fmt.Errorf("grep: invalid output_mode: %q", in.OutputMode)
			}

			searchPath := in.Path
			if searchPath == "" {
				searchPath = "."
			}
			_, err = env.PathPolicy.ResolvePath(searchPath, false)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, searchPath)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			showLines := true
			if in.LineNumbers != nil {
				showLines = *in.LineNumbers
			}

			diveInput := &toolkit.GrepInput{
				Pattern:    in.Pattern,
				Path:       absPath,
				Glob:       in.Glob,
				Type:       in.Type,
				OutputMode: toolkit.GrepOutputMode(in.OutputMode),
				CaseInsens: in.CaseInsensitive,
				ShowLines:  showLines,
				Before:     in.BeforeContext,
				After:      in.AfterContext,
				Multiline:  in.Multiline,
				HeadLimit:  in.HeadLimit,
				Offset:     in.Offset,
			}

			diveResult, err := grepTool.Call(ctx, diveInput)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			if diveResult.IsError {
				return &GrepResult{
					Output: diveText(diveResult),
				}, nil
			}

			outputText := diveText(diveResult)
			matches := countGrepMatches(outputText, in.OutputMode)

			result := GrepResult{
				Matches:  matches,
				Returned: matches,
				Output:   outputText,
			}

			if matches > 0 && matches >= in.HeadLimit {
				result.NextOffset = in.Offset + in.HeadLimit
			}

			return result, nil
		},
	}
}

// countGrepMatches counts the number of match items in grep output.
func countGrepMatches(output, mode string) int {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0
	}
	lines := strings.Split(output, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	if len(nonEmpty) == 0 {
		return 0
	}
	switch mode {
	case "files_with_matches":
		return len(nonEmpty)
	case "count":
		total := 0
		for _, line := range nonEmpty {
			if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
				if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
					total += n
				}
			}
		}
		if total > 0 {
			return total
		}
		return len(nonEmpty)
	case "content":
		count := 0
		for _, line := range nonEmpty {
			if strings.HasPrefix(line, "## ") {
				count++
			}
		}
		if count > 0 {
			return count
		}
		return len(nonEmpty)
	}
	return len(nonEmpty)
}
