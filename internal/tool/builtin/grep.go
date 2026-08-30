package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/tool"
)

// NewGrepTool creates a ToolDef for the grep tool.
func NewGrepTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "grep",
		ParallelSafe:    true,
		Description:     `Search file contents with paginated logical results. Use output_mode="files_with_matches" to locate relevant files, output_mode="count" for per-file counts, and output_mode="content" with context to inspect matches.`,
		ParameterSchema: GrepSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[GrepInput](input)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			normalizeGrep(&in)

			if in.OutputMode == "" {
				in.OutputMode = "content"
			}
			if in.LineNumbers == nil {
				v := true
				in.LineNumbers = &v
			}
			if in.Context > 0 {
				if in.BeforeContext == 0 {
					in.BeforeContext = in.Context
				}
				if in.AfterContext == 0 {
					in.AfterContext = in.Context
				}
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
			absPath, err := env.PathPolicy.ResolveReadPath(searchPath)
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			showLines := true
			if in.LineNumbers != nil {
				showLines = *in.LineNumbers
			}

			files, err := grepSearch(ctx, grepSearchParams{
				root:        absPath,
				displayPath: searchPath,
				pattern:     in.Pattern,
				caseInsens:  in.CaseInsensitive,
				multiline:   in.Multiline,
				fileGlob:    in.Glob,
				fileType:    in.Type,
				excluder:    env.Excluder,
				policy:      env.PathPolicy,
			})
			if err != nil {
				return nil, fmt.Errorf("grep: %w", err)
			}

			result := buildGrepResult(files, in.OutputMode, showLines, in.BeforeContext, in.AfterContext, in.Offset, in.HeadLimit)
			result.FileHashes = grepFileHashes(absPath, files)
			return result, nil
		},
	}
}

func grepFileHashes(absPath string, files []grepFileResult) map[string]string {
	if len(files) == 0 {
		return nil
	}
	rootIsDir := false
	if info, err := os.Stat(absPath); err == nil {
		rootIsDir = info.IsDir()
	}
	hashes := make(map[string]string, len(files))
	for _, f := range files {
		fPath := absPath
		if rootIsDir {
			fPath = filepath.Join(absPath, f.file)
		}
		data, err := os.ReadFile(fPath)
		if err == nil {
			hashes[f.file] = fileContentHash(data)
		}
	}
	return hashes
}
