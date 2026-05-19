package prompt

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const defaultProjectContextBudgetBytes = 2000

// ProjectContextOptions configures which project files are gathered into prompt context.
type ProjectContextOptions struct {
	Root        string
	BudgetBytes int
	ExtraFiles  []string
	IgnoreFiles []string
}

func gatherProjectContext(opts ProjectContextOptions) ([]ContextBlock, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, nil
	}
	budget := opts.BudgetBytes
	if budget <= 0 {
		budget = defaultProjectContextBudgetBytes
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	seen := make(map[string]struct{})
	var blocks []ContextBlock
	remaining := budget

	for _, candidate := range opts.ExtraFiles {
		if remaining <= 0 {
			break
		}
		absPath, skip, err := resolveCandidate(rootAbs, candidate, seen, opts.IgnoreFiles)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		block, err := readFileBlock(absPath, remaining)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
		remaining -= block.ByteSize
	}
	return blocks, nil
}

// resolveCandidate validates and resolves a candidate path relative to rootAbs,
// marks it seen, and returns its absolute path. Returns skip=true when the
// candidate should be silently skipped (duplicate, ignored, not-exist, dir).
func resolveCandidate(rootAbs, candidate string, seen map[string]struct{}, ignoreFiles []string) (string, bool, error) {
	relative := filepath.Clean(candidate)
	if relative == "." || relative == "" {
		return "", true, nil
	}
	if _, ok := seen[relative]; ok {
		return "", true, nil
	}
	seen[relative] = struct{}{}
	if isIgnored(relative, ignoreFiles) {
		return "", true, nil
	}

	absPath := filepath.Join(rootAbs, relative)
	relToRoot, err := filepath.Rel(rootAbs, absPath)
	if err != nil {
		return "", false, fmt.Errorf("resolve project context path %s: %w", candidate, err)
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("project context path %q escapes project root", candidate)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("stat %s: %w", absPath, err)
	}
	if info.IsDir() {
		return "", true, nil
	}
	return absPath, false, nil
}

// readFileBlock reads up to remaining bytes from absPath and returns a ContextBlock.
func readFileBlock(absPath string, remaining int) (ContextBlock, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return ContextBlock{}, fmt.Errorf("open %s: %w", absPath, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(f, int64(remaining+1)))
	closeErr := f.Close()
	if readErr != nil {
		return ContextBlock{}, fmt.Errorf("read %s: %w", absPath, readErr)
	}
	if closeErr != nil {
		return ContextBlock{}, fmt.Errorf("close %s: %w", absPath, closeErr)
	}
	if len(data) == 0 {
		return ContextBlock{Source: ContextSourceProjectContext, Path: absPath}, nil
	}

	blockBytes := len(data)
	truncated := false
	if blockBytes > remaining {
		blockBytes = remaining
		truncated = true
	}
	return ContextBlock{
		Source:    ContextSourceProjectContext,
		Path:      absPath,
		Content:   string(data[:blockBytes]),
		ByteSize:  blockBytes,
		Truncated: truncated,
	}, nil
}

func isIgnored(candidate string, ignore []string) bool {
	if len(ignore) == 0 {
		return false
	}

	candidate = filepath.Clean(candidate)
	base := filepath.Base(candidate)

	for _, raw := range ignore {
		value := filepath.Clean(strings.TrimSpace(raw))
		if value == "" || value == "." {
			continue
		}
		if candidate == value || base == value || strings.HasSuffix(candidate, string(filepath.Separator)+value) {
			return true
		}
	}
	return false
}
