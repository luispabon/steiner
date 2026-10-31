package tool

import (
	"fmt"
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
	// writeScope, when active, further restricts writable paths by final file
	// name. writeScopeRoot is the root the scope was installed with; it is
	// retained so clearing root via WithoutRoot cannot relax the scope.
	writeScope     WriteScope
	writeScopeRoot string
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
// The write scope is retained together with the root it was installed with, so
// approval cannot relax it.
func (p PathPolicy) WithoutRoot() PathPolicy {
	relaxed := p
	relaxed.root = ""
	relaxed.projectRootOnly = false
	return relaxed
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
	restricted := p
	restricted.writeAllowlist = normalized
	return restricted
}

// WithWriteScope returns a copy of p restricted so writable paths must match
// scope. The policy's current root is recorded as the scope root and retained
// through later copies, so the scope keeps checking containment against the
// root it was installed with.
func (p PathPolicy) WithWriteScope(scope WriteScope) PathPolicy {
	scoped := p
	scoped.writeScope = cloneWriteScope(scope)
	scoped.writeScopeRoot = p.root
	return scoped
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
	return p.resolveRead(raw, true)
}

// ResolveReadEntryPath normalizes a path discovered inside a directory walk and
// reports whether policy blocks it. Unlike ResolveReadPath it does not reject
// device, pipe, and socket files: listing a name is safe even when reading the
// file is not, so listings can show special files that readers still refuse to
// open.
func (p PathPolicy) ResolveReadEntryPath(raw string) (string, error) {
	return p.resolveRead(raw, false)
}

func (p PathPolicy) resolveRead(raw string, rejectSpecial bool) (string, error) {
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
	if rejectSpecial {
		if err := rejectSpecialFile(normalized); err != nil {
			return "", err
		}
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
	// The write scope is checked before any promptable denial so a scope
	// violation can never turn into an approval prompt or an approved retry.
	if err := p.ensureWriteScopeAllowed(path, writable); err != nil {
		return err
	}
	planWriteAllowed, err := p.ensurePlanWriteAllowed(path, writable)
	if err != nil {
		return err
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
	return p.ensureWritablePathAllowed(path, writable, planWriteAllowed)
}

func (p PathPolicy) ensurePlanWriteAllowed(path string, writable bool) (bool, error) {
	if !writable {
		return false, nil
	}
	allowed, err := p.writePathAllowed(path)
	if err != nil {
		return false, p.policyResolutionError(path, err)
	}
	if len(p.writeAllowlist) > 0 && !allowed {
		return false, &PathPolicyError{
			Path:       path,
			Reason:     planModeWriteDenial(),
			Promptable: false,
		}
	}
	return allowed, nil
}

func (p PathPolicy) ensureWritablePathAllowed(path string, writable, planWriteAllowed bool) error {
	if planWriteAllowed || !writable || len(p.writablePaths) == 0 {
		return nil
	}
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
