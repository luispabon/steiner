package tool

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// PathPolicy constrains tool paths relative to the active project root.
type PathPolicy struct {
	root            string
	projectRootOnly bool
	blockedPaths    []string
	writablePaths   []string
}

// NewPathPolicy creates a path policy from the working directory and paths configuration.
func NewPathPolicy(root string, cfg config.PathsConfig) PathPolicy {
	normalizedRoot := normalizePolicyPath(root, root)
	policy := PathPolicy{
		root:            normalizedRoot,
		projectRootOnly: cfg.ProjectRootOnly,
	}
	for _, path := range cfg.BlockedPaths {
		if normalized := normalizePolicyPath(normalizedRoot, path); normalized != "" {
			policy.blockedPaths = append(policy.blockedPaths, normalized)
		}
	}
	for _, path := range cfg.WritablePaths {
		if normalized := normalizePolicyPath(normalizedRoot, path); normalized != "" {
			policy.writablePaths = append(policy.writablePaths, normalized)
		}
	}
	return policy
}

// Root returns the normalized policy root path.
func (p PathPolicy) Root() string {
	return p.root
}

// ResolveCWD resolves a working directory against the policy root.
func (p PathPolicy) ResolveCWD(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		if p.root == "" {
			return "", fmt.Errorf("working directory is not configured")
		}
		return p.root, nil
	}
	return p.ResolvePath(raw, false)
}

// ResolvePath resolves a tool path against the policy root and allowlists.
func (p PathPolicy) ResolvePath(raw string, writable bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("path is required")
	}

	normalized := normalizePolicyPath(p.root, raw)
	if normalized == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := p.ensureAllowed(normalized, writable); err != nil {
		return "", err
	}
	return normalized, nil
}

func (p PathPolicy) ensureAllowed(path string, writable bool) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if p.root != "" && !pathWithinRoot(p.root, path) {
		return policyError("path %q is outside project root %q", path, p.root)
	}
	for _, blocked := range p.blockedPaths {
		if pathWithinRoot(blocked, path) {
			return policyError("path %q is blocked by policy", path)
		}
	}
	if writable && len(p.writablePaths) > 0 {
		for _, allowed := range p.writablePaths {
			if pathWithinRoot(allowed, path) {
				return nil
			}
		}
		return policyError("path %q is not in the writable allowlist", path)
	}
	return nil
}

// ValidateToolInput normalizes path-bearing tool arguments.
func (p PathPolicy) ValidateToolInput(toolName string, input map[string]any) (map[string]any, error) {
	normalized := CloneJSONMap(input)
	switch toolName {
	case "read", "glob", "grep", "ls":
		path := stringInput(normalized["path"])
		if path == "" {
			path = "."
			normalized["path"] = "."
		}
		resolved, err := p.ResolvePath(path, false)
		if err != nil {
			return nil, err
		}
		normalized["path"] = resolved
	case "write", "edit":
		path := stringInput(normalized["path"])
		resolved, err := p.ResolvePath(path, true)
		if err != nil {
			return nil, err
		}
		normalized["path"] = resolved
	case "mutate":
		ops, ok := normalized["operations"].([]any)
		if !ok {
			return normalized, nil
		}
		normalizedOps := make([]any, 0, len(ops))
		for _, rawOp := range ops {
			op, ok := rawOp.(map[string]any)
			if !ok {
				normalizedOps = append(normalizedOps, rawOp)
				continue
			}
			nextOp := CloneJSONMap(op)
			if path := stringInput(nextOp["path"]); path != "" {
				resolved, err := p.ResolvePath(path, true)
				if err != nil {
					return nil, err
				}
				nextOp["path"] = resolved
			}
			if from := stringInput(nextOp["from"]); from != "" {
				resolved, err := p.ResolvePath(from, true)
				if err != nil {
					return nil, err
				}
				nextOp["from"] = resolved
			}
			if to := stringInput(nextOp["to"]); to != "" {
				resolved, err := p.ResolvePath(to, true)
				if err != nil {
					return nil, err
				}
				nextOp["to"] = resolved
			}
			normalizedOps = append(normalizedOps, nextOp)
		}
		normalized["operations"] = normalizedOps
	case "bash":
		cwd, err := p.ResolveCWD(stringInput(normalized["cwd"]))
		if err != nil {
			return nil, err
		}
		if cwd != "" {
			normalized["cwd"] = cwd
		}
	}
	return normalized, nil
}

func (p PathPolicy) previewToolInput(toolName string, input map[string]any) (ApprovalPreview, error) {
	normalized, err := p.ValidateToolInput(toolName, input)
	if err != nil {
		return ApprovalPreview{}, err
	}
	return buildApprovalPreview(toolName, normalized, p), nil
}

func normalizePolicyPath(root, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	path := strings.TrimSpace(raw)
	if !filepath.IsAbs(path) {
		if root == "" {
			path = filepath.Clean(path)
		} else {
			path = filepath.Join(root, path)
		}
	}
	return filepath.Clean(path)
}

func pathWithinRoot(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func policyError(format string, args ...any) error {
	return fmt.Errorf("tool path policy denied: "+format, args...)
}

func stringInput(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
