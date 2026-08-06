package advisor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/tool"
)

// advisorFile is one caller-supplied artifact loaded for the advisor's
// bounded suffix payload. Content is read through a limited reader capped at
// maxAdvisorFileBytes, so it may be shorter than TotalBytes for large files;
// TotalBytes always reflects the real on-disk size so the elision marker in
// renderAdvisorFiles can report an accurate total even when Content itself
// was truncated at read time.
type advisorFile struct {
	DisplayPath string
	Content     string
	TotalBytes  int
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
		content, totalBytes, err := readCapped(absPath, maxAdvisorFileBytes)
		if err != nil {
			return nil, fmt.Errorf("advisor: read %q: %w", raw, err)
		}
		files = append(files, advisorFile{
			DisplayPath: relDisplayPath(workDir, policy.DisplayPath(absPath)),
			Content:     content,
			TotalBytes:  totalBytes,
		})
	}
	return files, nil
}

// readCapped reads at most capBytes from absPath without allocating for
// bytes beyond the cap, so a caller-supplied path pointing at a very large
// file cannot force an unbounded read. It also returns the file's real
// on-disk size (via stat, which costs no allocation) so callers can report
// an accurate total even when the returned content was truncated.
func readCapped(absPath string, capBytes int) (string, int, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return "", 0, err
	}
	f, err := os.Open(absPath)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, int64(capBytes)))
	if err != nil {
		return "", 0, err
	}
	return string(data), int(info.Size()), nil
}

// relDisplayPath returns p relative to workDir when possible. For paths
// under workDir this hides the absolute host path; for paths outside
// workDir (or when no relation can be computed) it falls back to p as-is,
// which may itself be absolute or contain "..".
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
