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

	seen := make(map[string]struct{})
	blocks := make([]ContextBlock, 0)
	remaining := budget

	candidates := append([]string(nil), opts.ExtraFiles...)

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	for _, candidate := range candidates {
		if remaining <= 0 {
			break
		}
		relative := filepath.Clean(candidate)
		if relative == "." || relative == "" {
			continue
		}
		if _, ok := seen[relative]; ok {
			continue
		}
		seen[relative] = struct{}{}
		if isIgnored(relative, opts.IgnoreFiles) {
			continue
		}

		path := filepath.Join(rootAbs, relative)
		relToRoot, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return nil, fmt.Errorf("resolve project context path %s: %w", candidate, err)
		}
		if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("project context path %q escapes project root", candidate)
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if info.IsDir() {
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(f, int64(remaining+1)))
		closeErr := f.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", path, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close %s: %w", path, closeErr)
		}
		if len(data) == 0 {
			blocks = append(blocks, ContextBlock{
				Source:   ContextSourceProjectContext,
				Path:     path,
				Content:  "",
				ByteSize: 0,
			})
			continue
		}

		blockBytes := len(data)
		truncated := false
		if blockBytes > remaining {
			blockBytes = remaining
			truncated = true
		}

		blocks = append(blocks, ContextBlock{
			Source:    ContextSourceProjectContext,
			Path:      path,
			Content:   string(data[:blockBytes]),
			ByteSize:  blockBytes,
			Truncated: truncated,
		})
		remaining -= blockBytes
	}

	return blocks, nil
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
