package advisor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/tool"
)

// advisorFile is one caller-supplied artifact loaded for the advisor's
// bounded suffix payload.
type advisorFile struct {
	DisplayPath string
	Content     string
}

// advisorInput is the decoded optional input to the advisor tool.
type advisorInput struct {
	Question string
	Files    []string
}

// decodeAdvisorInput extracts the optional question and files fields from a
// raw tool-call input map. A nil or empty map decodes to the zero value,
// matching a bare advisor call.
func decodeAdvisorInput(raw map[string]any) (advisorInput, error) {
	var in advisorInput
	if raw == nil {
		return in, nil
	}
	if v, ok := raw["question"]; ok {
		s, ok := v.(string)
		if !ok {
			return in, fmt.Errorf("question must be a string")
		}
		in.Question = s
	}
	if v, ok := raw["files"]; ok {
		items, ok := v.([]any)
		if !ok {
			return in, fmt.Errorf("files must be an array of strings")
		}
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return in, fmt.Errorf("files must be an array of strings")
			}
			in.Files = append(in.Files, s)
		}
	}
	return in, nil
}

// loadAdvisorFiles resolves and reads each caller-supplied path under the
// given path policy, the same way the read tool does. It returns an error
// before any handler state changes so a malformed call never consumes an
// advisor use.
func loadAdvisorFiles(workDir string, policy *tool.PathPolicy, paths []string) ([]advisorFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > maxAdvisorFiles {
		return nil, fmt.Errorf("advisor: too many files (%d), max %d", len(paths), maxAdvisorFiles)
	}
	if policy == nil {
		return nil, fmt.Errorf("advisor: file access is not configured")
	}

	files := make([]advisorFile, 0, len(paths))
	for _, raw := range paths {
		absPath, err := policy.ResolveReadPath(raw)
		if err != nil {
			return nil, fmt.Errorf("advisor: %w", err)
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("advisor: read %q: %w", raw, err)
		}
		files = append(files, advisorFile{
			DisplayPath: relDisplayPath(workDir, policy.DisplayPath(absPath)),
			Content:     string(data),
		})
	}
	return files, nil
}

// relDisplayPath returns p relative to workDir when possible, so absolute
// host paths never leak into the advisor's prompt.
func relDisplayPath(workDir, p string) string {
	if workDir == "" || p == "" {
		return p
	}
	rel, err := filepath.Rel(workDir, p)
	if err != nil {
		return p
	}
	return rel
}
