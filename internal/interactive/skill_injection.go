package interactive

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// setSkillEnabled toggles skill tracking and, when enabling with a configured
// loader, verifies the skill loads and warns once when its body exceeds the
// injection cap. The load and event run outside the session lock. On load
// failure the skill stays enabled so the next submission retries it.
func (s *Session) setSkillEnabled(ctx context.Context, name string, enabled bool) error {
	s.skills.Set(name, enabled)
	if !enabled {
		return nil
	}
	loader := s.deps.SkillLoader
	if loader == nil {
		return nil
	}
	loaded, err := loader.Load(ctx, name)
	if err != nil {
		return fmt.Errorf("load skill %s: %w", name, err)
	}
	if len(loaded.Content) > prompt.SkillContentCapBytes {
		s.events.Emit(output.NewSkillTruncatedEvent(name, len(loaded.Content), prompt.SkillContentCapBytes))
	}
	return nil
}

// skillDeltaBlocks computes the skill block envelopes to prepend to the next
// user message: deactivations for skills effective in conversation that are no
// longer enabled, then activations for enabled skills not yet effective. It
// returns nil when no loader is configured.
func (s *Session) skillDeltaBlocks(ctx context.Context, conversation []agent.Message) []string {
	loader := s.deps.SkillLoader
	if loader == nil {
		return nil
	}

	effective := prompt.EffectiveSkills(userContents(conversation))
	enabled := s.skills.Snapshot()

	enabledSet := make(map[string]bool, len(enabled))
	for _, name := range enabled {
		enabledSet[name] = true
	}
	effectiveSet := make(map[string]bool, len(effective))
	for _, block := range effective {
		effectiveSet[block.Name] = true
	}

	var blocks []string
	for _, block := range effective {
		if !enabledSet[block.Name] {
			blocks = append(blocks, prompt.RenderSkillDeactivation(block.Name))
		}
	}
	for _, name := range enabled {
		if effectiveSet[name] {
			continue
		}
		loaded, err := loader.Load(ctx, name)
		if err != nil {
			slog.Warn("load skill for injection", "skill", name, "error", err)
			continue
		}
		rendered, _ := prompt.RenderSkillActivation(name, loaded.Content)
		blocks = append(blocks, rendered)
	}
	return blocks
}

// userContents folds the content of user-role messages in order.
func userContents(messages []agent.Message) []string {
	contents := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == agent.MessageRoleUser {
			contents = append(contents, msg.Content)
		}
	}
	return contents
}
