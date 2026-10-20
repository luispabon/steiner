package interactive

import "github.com/luispabon/steiner/internal/config"

// CurrentModelConfig returns the currently active model config. Test-only: no
// production code calls it; production resolves the model via SelectedModelConfig.
func (s *Session) CurrentModelConfig() config.ModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SelectedModelConfig(s.deps.Config)
}
