package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// planModeWriteDenial renders the denial reason for a write outside the
// plan-mode allowlist, listing the directories config permits.
func planModeWriteDenial() string {
	dirs := config.PlanModeWritableDirs()
	quoted := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		quoted = append(quoted, "`"+dir+"/`")
	}
	return fmt.Sprintf("plan mode: write operations are restricted to %s. "+
		"Ask the user to switch to build mode, or call workflow_handoff when your plan is ready.",
		strings.Join(quoted, ", "))
}

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
	writeAllowlist  []string
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
		writeAllowlist:  p.writeAllowlist,
	}
}

// RestrictWritesTo returns a copy of p with the write allowlist set to the given
// prefixes. Paths in plan mode are restricted to these prefixes; all other modes
// are unaffected. The prefixes are normalized relative to the policy root.
func (p PathPolicy) RestrictWritesTo(prefixes ...string) PathPolicy {
	normalized := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if n := normalizePolicyPath(p.root, prefix); n != "" {
			normalized = append(normalized, n)
		}
	}
	return PathPolicy{
		root:            p.root,
		sandboxTmpDir:   p.sandboxTmpDir,
		projectRootOnly: p.projectRootOnly,
		blockedPaths:    p.blockedPaths,
		writablePaths:   p.writablePaths,
		writeAllowlist:  normalized,
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
	if blocked, err := p.blocked(normalized); err != nil {
		return "", p.policyResolutionError(normalized, err)
	} else if blocked {
		return "", p.blockedPathError(normalized)
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

func (p PathPolicy) pathWithinSandboxTmp(path string) bool {
	if p.sandboxTmpDir == "" || path == "" {
		return false
	}
	return pathWithinRoot(p.sandboxTmpDir, path)
}

// allowed reports whether path is lexically and resolved-path contained by the
// project root or, for a writable operation, the configured sandbox tmp dir.
// Containment is only enforced when the policy root is set and project_root_only
// is on.
func (p PathPolicy) allowed(path string, writable bool) (bool, error) {
	if p.root == "" || !p.projectRootOnly {
		return true, nil
	}
	if writable && p.pathWithinSandboxTmp(path) {
		return pathWithinPolicyPrefix(p.sandboxTmpDir, path)
	}
	return pathWithinPolicyPrefix(p.root, path)
}

func (p PathPolicy) ensureAllowed(path string, writable bool) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	planWriteAllowed := false
	if writable {
		var err error
		planWriteAllowed, err = p.writePathAllowed(path)
		if err != nil {
			return p.policyResolutionError(path, err)
		}
	}
	if writable && len(p.writeAllowlist) > 0 && !planWriteAllowed {
		return &PathPolicyError{
			Path:       path,
			Reason:     planModeWriteDenial(),
			Promptable: false,
		}
	}
	allowed, err := p.allowed(path, writable)
	if err != nil {
		return p.policyResolutionError(path, err)
	}
	if !allowed {
		return &PathPolicyError{
			Path:       path,
			Reason:     fmt.Sprintf("path %q is outside project root %q", path, p.root),
			Promptable: true,
		}
	}
	if blocked, err := p.blocked(path); err != nil {
		return p.policyResolutionError(path, err)
	} else if blocked {
		return p.blockedPathError(path)
	}
	if planWriteAllowed {
		return nil
	}
	if writable && len(p.writablePaths) > 0 {
		for _, allowed := range p.writablePaths {
			within, err := pathWithinPolicyPrefix(allowed, path)
			if err != nil {
				return p.policyResolutionError(path, err)
			}
			if within {
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

func (p PathPolicy) writePathAllowed(path string) (bool, error) {
	for _, allowed := range p.writeAllowlist {
		if !pathWithinRoot(allowed, path) {
			continue
		}
		within, err := canonicalWritePathWithin(p.root, allowed, path)
		if err != nil {
			return false, err
		}
		if within {
			return true, nil
		}
	}
	return false, nil
}

func (p PathPolicy) blocked(path string) (bool, error) {
	for _, blocked := range p.blockedPaths {
		if pathWithinRoot(blocked, path) {
			return true, nil
		}
		within, err := resolvedPathWithin(blocked, path)
		if err != nil {
			return false, err
		}
		if within {
			return true, nil
		}
	}
	return false, nil
}

func (p PathPolicy) blockedPathError(path string) *PathPolicyError {
	return &PathPolicyError{
		Path:       path,
		Reason:     fmt.Sprintf("path %q is blocked by policy", path),
		Promptable: false,
	}
}

func (p PathPolicy) policyResolutionError(path string, err error) *PathPolicyError {
	return &PathPolicyError{
		Path:       path,
		Reason:     fmt.Sprintf("resolve path for policy check: %v", err),
		Promptable: false,
	}
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
	if p.IsSandboxTmpPath(path) {
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

// DisplayPath reverses the /tmp rewrite: if resolved is under sandboxTmpDir,
// replaces that prefix with /tmp to hide the internal sandbox path from the user.
// Otherwise returns the path unchanged.
func (p PathPolicy) DisplayPath(resolved string) string {
	if p.sandboxTmpDir == "" || resolved == "" {
		return resolved
	}
	if resolved == p.sandboxTmpDir {
		return "/tmp"
	}
	if strings.HasPrefix(resolved, p.sandboxTmpDir+string(filepath.Separator)) {
		return "/tmp" + resolved[len(p.sandboxTmpDir):]
	}
	return resolved
}

// IsSandboxTmpPath reports whether path is under the sandbox tmpDir.
func (p PathPolicy) IsSandboxTmpPath(path string) bool {
	if p.sandboxTmpDir == "" || path == "" {
		return false
	}
	return path == p.sandboxTmpDir || strings.HasPrefix(path, p.sandboxTmpDir+string(filepath.Separator))
}

func normalizePolicyPath(root, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	path := expandTilde(raw)
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

// PathWithinRoot reports whether path is lexically contained within root. Both
// are expected to be cleaned absolute paths; symlinks are not resolved.
func PathWithinRoot(root, path string) bool {
	return pathWithinRoot(root, path)
}

// canonicalWritePathWithin reports whether path resolves under the lexical
// allowlist prefix's intended location beneath the resolved policy root. The
// allowlist prefix itself is not resolved, so a symlink in that prefix cannot
// silently redefine the directory that plan mode permits.
func canonicalWritePathWithin(root, allowed, path string) (bool, error) {
	resolvedRoot, err := resolvePathWithMissing(root)
	if err != nil {
		return false, fmt.Errorf("resolve policy root: %w", err)
	}
	rel, err := filepath.Rel(root, allowed)
	if err != nil || !pathWithinRoot(root, allowed) {
		return false, fmt.Errorf("allowlist prefix is outside policy root")
	}
	intended := filepath.Join(resolvedRoot, rel)
	resolvedPath, err := resolvePathWithMissing(path)
	if err != nil {
		return false, fmt.Errorf("resolve policy path: %w", err)
	}
	return pathWithinRoot(intended, resolvedPath), nil
}

// resolvePathWithMissing resolves path, preserving its missing suffix after
// resolving the nearest existing ancestor. It fails on errors other than a
// missing path component.
func resolvePathWithMissing(path string) (string, error) {
	target := filepath.Clean(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(target)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, lstatErr := os.Lstat(target); lstatErr == nil {
			return "", fmt.Errorf("resolve symlink %q: %w", target, err)
		} else if !os.IsNotExist(lstatErr) {
			return "", lstatErr
		}
		parent := filepath.Dir(target)
		if parent == target {
			return "", err
		}
		missing = append(missing, filepath.Base(target))
		target = parent
	}
}

// pathWithinPolicyPrefix reports whether path is contained within prefix both
// lexically and after resolving symlinks. The lexical check retains configured
// path boundaries while the resolved check prevents a symlink from escaping one.
func pathWithinPolicyPrefix(prefix, path string) (bool, error) {
	if !pathWithinRoot(prefix, path) {
		return false, nil
	}
	return resolvedPathWithin(prefix, path)
}

// resolvedPathWithin reports whether path is contained within prefix after
// resolving symlinks while preserving any missing suffix.
func resolvedPathWithin(prefix, path string) (bool, error) {
	resolvedPrefix, err := resolvePathWithMissing(prefix)
	if err != nil {
		return false, fmt.Errorf("resolve policy prefix: %w", err)
	}
	resolvedPath, err := resolvePathWithMissing(path)
	if err != nil {
		return false, fmt.Errorf("resolve policy path: %w", err)
	}
	return pathWithinRoot(resolvedPrefix, resolvedPath), nil
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
