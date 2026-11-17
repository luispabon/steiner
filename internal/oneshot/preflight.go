package oneshot

import (
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// RequireSubAgents returns an error if sub-agents are disabled in cfg, since
// oneshot runs depend on sub-agent delegation.
func RequireSubAgents(cfg config.Config) error {
	if !cfg.SubAgent.Enabled {
		return errors.New("oneshot unavailable: sub-agents are disabled in config")
	}
	return nil
}

// RequireModels returns an error wrapping config.ErrNoModelConfigured when a
// oneshot phase or the advisor has no model reference in cfg. A phase uses its
// own selection or the profile default; the advisor uses the advisor selection
// or the profile default. It never falls back to the active orchestrator model.
func RequireModels(cfg config.Config) error {
	for _, phase := range phaseOrder() {
		if err := config.RequireModelReference(cfg, phaseModelAlias(cfg, phase)); err != nil {
			return fmt.Errorf("oneshot %s phase model: %w", phase, err)
		}
	}
	advisor := strings.TrimSpace(cfg.Models.Effective.Advisor)
	if advisor == "" {
		advisor = strings.TrimSpace(cfg.Models.Effective.DefaultModel)
	}
	if err := config.RequireModelReference(cfg, advisor); err != nil {
		return fmt.Errorf("oneshot advisor model: %w", err)
	}
	return nil
}
