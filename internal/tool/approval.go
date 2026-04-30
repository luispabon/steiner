package tool

import (
	"github.com/luispabon/steiner/internal/config"
)

type ApprovalResolver struct {
	Config config.Config
}

// NewApprovalResolver creates an approval resolver from the application configuration.
func NewApprovalResolver(cfg config.Config) ApprovalResolver {
	return ApprovalResolver{Config: cfg}
}

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

func (r ApprovalResolver) ModeFor(def ToolDef) config.ApprovalMode {
	return ResolveApprovalMode(r.Config, def)
}

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

func IsApprovalPrompt(mode config.ApprovalMode) bool {
	return mode == config.ApprovalModePrompt || mode == ""
}

func IsApprovalDenied(mode config.ApprovalMode) bool {
	return mode == config.ApprovalModeDeny
}
