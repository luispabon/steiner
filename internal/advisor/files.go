package advisor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
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
	// Deduped is true when Content was cleared because the full,
	// byte-identical file already appears in snapshot as a prior full
	// "read" tool result — see dedupeAgainstSnapshot.
	Deduped bool
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

// advisorDisplayPaths returns the display paths of the loaded advisor files,
// preserving order.
func advisorDisplayPaths(files []advisorFile) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.DisplayPath)
	}
	return paths
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

// dedupeAgainstSnapshot clears the content of any file whose full,
// unmodified bytes already appear in snapshot as a prior full-file "read"
// result, so the advisor request doesn't resend it. Only applies to files
// loadAdvisorFiles read in full (TotalBytes <= maxAdvisorFileBytes) — a
// capped read's Content is a prefix, not the whole file, and read's
// file_hash is always computed over the whole file, so a capped file has
// nothing valid to compare against.
func dedupeAgainstSnapshot(files []advisorFile, snapshot []provider.Message, workDir string) []advisorFile {
	for i := range files {
		f := &files[i]
		if f.TotalBytes > maxAdvisorFileBytes {
			continue
		}
		hash, ok := findFullFileReadHash(snapshot, f.DisplayPath, workDir)
		if !ok || hash != builtin.FileContentHash([]byte(f.Content)) {
			continue
		}
		f.Content = ""
		f.Deduped = true
	}
	return files
}

// findFullFileReadHash scans snapshot most-recent-first for a "read" tool
// result covering path in full (start_line 1 through total_lines) and
// returns its file_hash. A message that isn't a "read" result, doesn't
// decode as one, is for a different path, or covers only part of the file
// is skipped — including a compacted/summarized message, which naturally
// fails to decode and is treated as "not found" (fail safe: falls back to
// sending content, never a false dedup).
//
// rr.Path is run through relDisplayPath(workDir, ...) before comparison
// because read.go only relativizes its display path when the path policy's
// DisplayPath doesn't already start with "/" (in practice it always does),
// while loadAdvisorFiles relativizes unconditionally. Both sides start from
// the same policy.DisplayPath(absPath) value, so applying the same
// relDisplayPath transform to rr.Path here restores comparability with
// path (an advisorFile.DisplayPath, already relativized) without changing
// read.go.
func findFullFileReadHash(snapshot []provider.Message, path, workDir string) (string, bool) {
	for i := len(snapshot) - 1; i >= 0; i-- {
		m := snapshot[i]
		if m.Role != provider.MessageRoleTool || m.Name != "read" {
			continue
		}
		var rr builtin.ReadResult
		if err := json.Unmarshal([]byte(m.Content), &rr); err != nil {
			continue
		}
		if relDisplayPath(workDir, rr.Path) != path || rr.TotalLines == 0 || rr.StartLine != 1 || rr.EndLine != rr.TotalLines {
			continue
		}
		if strings.Contains(rr.Output, builtin.LineTruncationMarker) {
			// A per-line clip means Output isn't a byte-faithful copy of
			// the file even though the range/hash look complete.
			continue
		}
		return rr.FileHash, true
	}
	return "", false
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
