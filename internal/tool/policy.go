package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// PathPolicyError is returned when a path is rejected by policy.
type PathPolicyError struct {
	Path       string
	Reason     string
	Promptable bool // true when the violation is "outside project root" (can be overridden by user)
}

// Error implements the error interface.
func (e *PathPolicyError) Error() string {
	return fmt.Sprintf("tool path policy denied: %s", e.Reason)
}

// PathPolicy constrains tool paths relative to the active project root.
type PathPolicy struct {
	root            string
	sandboxTmpDir   string
	projectRootOnly bool
	blockedPaths    []string
	writablePaths   []string
}

// NewPathPolicy creates a path policy from the working directory and paths configuration.
func NewPathPolicy(root string, cfg config.PathsConfig) PathPolicy {
	return NewPathPolicyWithSandbox(root, cfg, "")
}

// NewPathPolicyWithSandbox creates a path policy with an optional sandbox tmpDir.
// When sandboxTmpDir is non-empty, /tmp paths are rewritten to sandboxTmpDir.
func NewPathPolicyWithSandbox(root string, cfg config.PathsConfig, sandboxTmpDir string) PathPolicy {
	normalizedRoot := normalizePolicyPath(root, root)
	policy := PathPolicy{
		root:            normalizedRoot,
		sandboxTmpDir:   sandboxTmpDir,
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

// WithoutRoot returns a copy of p with the root and projectRootOnly constraints
// cleared. Used when the user has approved access to a path outside the project root.
func (p PathPolicy) WithoutRoot() PathPolicy {
	return PathPolicy{
		root:            "",
		sandboxTmpDir:   p.sandboxTmpDir,
		projectRootOnly: false,
		blockedPaths:    p.blockedPaths,
		writablePaths:   p.writablePaths,
	}
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

// ResolveReadPath normalizes a path for read-only access, checking only blocked
// paths. The project-root constraint is intentionally skipped so that read,
// glob, grep, and ls can operate anywhere the filesystem allows.
func (p PathPolicy) ResolveReadPath(raw string) (string, error) {
	normalized := normalizePolicyPath(p.root, raw)
	if normalized == "" {
		return "", fmt.Errorf("path is required")
	}
	normalized = p.rewriteTmpPath(normalized)
	for _, blocked := range p.blockedPaths {
		if pathWithinRoot(blocked, normalized) {
			return "", &PathPolicyError{
				Path:       normalized,
				Reason:     fmt.Sprintf("path %q is blocked by policy", normalized),
				Promptable: false,
			}
		}
	}
	if err := rejectSpecialFile(normalized); err != nil {
		return "", err
	}
	return normalized, nil
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
	normalized = p.rewriteTmpPath(normalized)
	if err := p.ensureAllowed(normalized, writable); err != nil {
		return "", err
	}
	if err := rejectSpecialFile(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func (p PathPolicy) ensureAllowed(path string, writable bool) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if p.root != "" && !pathWithinRoot(p.root, path) {
		return &PathPolicyError{
			Path:       path,
			Reason:     fmt.Sprintf("path %q is outside project root %q", path, p.root),
			Promptable: true,
		}
	}
	for _, blocked := range p.blockedPaths {
		if pathWithinRoot(blocked, path) {
			return &PathPolicyError{
				Path:       path,
				Reason:     fmt.Sprintf("path %q is blocked by policy", path),
				Promptable: false,
			}
		}
	}
	if writable && len(p.writablePaths) > 0 {
		for _, allowed := range p.writablePaths {
			if pathWithinRoot(allowed, path) {
				return nil
			}
		}
		return &PathPolicyError{
			Path:       path,
			Reason:     fmt.Sprintf("path %q is not in the writable allowlist", path),
			Promptable: false,
		}
	}
	return nil
}

// ValidateToolInput normalizes path-bearing tool arguments.
func (p PathPolicy) ValidateToolInput(toolName string, input map[string]any) (map[string]any, error) {
	normalized := CloneJSONMap(input)
	switch toolName {
	case "read", "glob", "grep", "ls":
		return p.validateReadOnlyToolInput(normalized)
	case "mutate":
		return p.validateMutateToolInput(normalized)
	case "bash":
		return p.validateBashToolInput(normalized)
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

func (p PathPolicy) validateReadOnlyToolInput(input map[string]any) (map[string]any, error) {
	path := stringInput(input["path"])
	if path == "" {
		path = "."
		input["path"] = "."
	}
	resolved, err := p.ResolveReadPath(path)
	if err != nil {
		return nil, err
	}
	input["path"] = resolved
	return input, nil
}

func (p PathPolicy) validateMutateToolInput(input map[string]any) (map[string]any, error) {
	ops, ok := input["operations"].([]any)
	if !ok {
		return input, nil
	}

	normalizedOps := make([]any, 0, len(ops))
	for _, rawOp := range ops {
		normalizedOp, err := p.normalizeMutateOperation(rawOp)
		if err != nil {
			return nil, err
		}
		normalizedOps = append(normalizedOps, normalizedOp)
	}
	input["operations"] = normalizedOps
	return input, nil
}

func (p PathPolicy) normalizeMutateOperation(rawOp any) (any, error) {
	op, ok := rawOp.(map[string]any)
	if !ok {
		return rawOp, nil
	}
	nextOp := CloneJSONMap(op)
	for _, key := range []string{"path", "from", "to"} {
		resolved, err := p.resolveOptionalPath(nextOp[key], true)
		if err != nil {
			return nil, err
		}
		if resolved != "" {
			nextOp[key] = resolved
		}
	}
	return nextOp, nil
}

func (p PathPolicy) validateBashToolInput(input map[string]any) (map[string]any, error) {
	cwd, err := p.ResolveCWD(stringInput(input["cwd"]))
	if err != nil {
		return nil, err
	}
	if cwd != "" {
		input["cwd"] = cwd
	}
	return input, nil
}

func (p PathPolicy) resolveOptionalPath(value any, writable bool) (string, error) {
	path := stringInput(value)
	if path == "" {
		return "", nil
	}
	return p.ResolvePath(path, writable)
}

// rewriteTmpPath rewrites /tmp paths to sandboxTmpDir when sandbox is active.
// If sandboxTmpDir is empty, returns path unchanged.
func (p PathPolicy) rewriteTmpPath(path string) string {
	if p.sandboxTmpDir == "" {
		return path
	}
	if path == "/tmp" {
		return p.sandboxTmpDir
	}
	if strings.HasPrefix(path, "/tmp/") {
		return filepath.Join(p.sandboxTmpDir, path[5:])
	}
	return path
}

func normalizePolicyPath(root, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	path := strings.TrimSpace(raw)
	path = expandTilde(path)
	if !filepath.IsAbs(path) {
		if root == "" {
			path = filepath.Clean(path)
		} else {
			path = filepath.Join(root, path)
		}
	}
	return filepath.Clean(path)
}

func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
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

func stringInput(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

// rejectSpecialFile denies device, pipe, and socket paths so tools can never
// open the controlling terminal (e.g. /dev/stdin) or other non-regular files.
func rejectSpecialFile(path string) error {
	fi, err := os.Stat(path) // follow symlinks so /dev/stdin -> tty is caught
	if err != nil {
		return nil // missing/unstatable: let the tool surface its own error
	}
	const special = os.ModeDevice | os.ModeCharDevice | os.ModeNamedPipe | os.ModeSocket
	if fi.Mode()&special != 0 {
		return &PathPolicyError{
			Path:   path,
			Reason: fmt.Sprintf("path %q is not a regular file", path),
		}
	}
	return nil
}
