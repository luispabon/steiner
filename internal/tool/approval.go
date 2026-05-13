package tool

import (
	"github.com/luispabon/steiner/internal/config"
)

// ApprovalResolver resolves approval mode and preview data for a tool.
type ApprovalResolver struct {
	Config config.Config
}

// NewApprovalResolver creates an approval resolver from the application configuration.
func NewApprovalResolver(cfg config.Config) ApprovalResolver {
	return ApprovalResolver{Config: cfg}
}

// ResolveApprovalMode resolves the effective approval mode for a tool.
func ResolveApprovalMode(cfg config.Config, def ToolDef) config.ApprovalMode {
	if def.Approval != "" {
		return def.Approval
	}
	if mode, ok := cfg.Approval.ToolOverrides[def.Name]; ok && mode != nil && *mode != "" {
		return *mode
	}
	if cfg.Approval.Default != "" {
		return cfg.Approval.Default
	}
	return config.ApprovalModeAuto
}

// ModeFor returns the effective approval mode for a tool definition.
func (r ApprovalResolver) ModeFor(def ToolDef) config.ApprovalMode {
	return ResolveApprovalMode(r.Config, def)
}

// PreviewFor builds the approval preview for a validated tool input.
func (r ApprovalResolver) PreviewFor(def ToolDef, input map[string]any, policy PathPolicy) (ApprovalPreview, error) {
	normalized, err := policy.ValidateToolInput(def.Name, input)
	if err != nil {
		return ApprovalPreview{}, err
	}
	preview := buildApprovalPreview(def.Name, normalized, policy)
	preview.Mode = r.ModeFor(def)
	preview.Timeout = def.Timeout
	preview.WorkDir = policy.Root()
	return preview, nil
}

// IsApprovalPrompt reports whether approval should be requested interactively.
func IsApprovalPrompt(mode config.ApprovalMode) bool {
	return mode == config.ApprovalModePrompt || mode == ""
}

// IsApprovalDenied reports whether approval should be denied immediately.
func IsApprovalDenied(mode config.ApprovalMode) bool {
	return mode == config.ApprovalModeDeny
}
