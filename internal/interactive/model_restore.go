package interactive

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

// Model selection sources reported through Dependencies.StartupModelSource.
const (
	// ModelSourceFlag marks a startup model chosen with --model.
	ModelSourceFlag = "flag"
	// ModelSourceEnv marks a startup model chosen with STEINER_MODEL.
	ModelSourceEnv = "env"
	// ModelSourceProfile marks a startup model from the selected profile.
	ModelSourceProfile = "profile"
)

// restoredModel is the outcome of resolving a loaded session's saved model
// selection against the current configuration and local credentials.
type restoredModel struct {
	// ref is the saved model reference to activate; empty keeps the startup
	// selection.
	ref string
	// applied reports whether ref replaced the active orchestrator model.
	applied bool
	// overrides are the saved runtime reasoning overrides, restored whether or
	// not the saved model can be used.
	overrides map[string]session.ReasoningOverride
	// warnings are user-facing notices emitted after the transcript replay.
	warnings []string
}

// resolveRestoredModel decides which model a loaded session continues on. The
// saved selection wins over the startup selection; when the saved model cannot
// be used locally the startup selection is kept and the reason becomes a
// warning. An explicit startup choice that the saved model overrides is also
// reported.
func (s *Session) resolveRestoredModel(saved session.Session) restoredModel {
	s.mu.RLock()
	cfg := s.deps.Config
	startupRef := s.startupModel
	startupSource := s.startupModelSource
	s.mu.RUnlock()

	if startupRef == "" {
		startupRef = effectiveModelRef(cfg)
	}
	result := restoredModel{overrides: saved.ReasoningOverrides}
	ref, warnings := savedModelRef(cfg, saved, startupRef)
	result.warnings = warnings
	if ref == "" {
		// No usable saved identity, or an ambiguous legacy id with no startup
		// match: reset to the startup selection so a previous /model switch or
		// an earlier resume cannot leak into this conversation.
		result.ref = startupRef
		result.applied = startupRef != ""
		return result
	}
	wantProvider, wantID := savedIdentity(saved)
	if err := localModelUsageError(cfg, ref, wantProvider, wantID); err != nil {
		result.warnings = append(result.warnings, fallbackWarning(savedModelLabel(saved, ref), err, startupRef))
		result.ref = startupRef
		result.applied = startupRef != ""
		return result
	}
	result.ref = ref
	result.applied = true
	if ref != startupRef {
		if warning := ignoredOverrideWarning(startupSource, startupRef, ref); warning != "" {
			result.warnings = append(result.warnings, warning)
		}
	}
	return result
}

// savedModelRef returns the configured model reference a saved session asks
// for, plus any ambiguity warning. It returns an empty reference when the
// session recorded no model or the legacy backend id is ambiguous and the
// startup selection is not one of the matches.
func savedModelRef(cfg config.Config, saved session.Session, startupRef string) (string, []string) {
	if ref := strings.TrimSpace(saved.ModelRef); ref != "" {
		return ref, nil
	}
	backend := strings.TrimSpace(saved.Model)
	if backend == "" {
		return "", nil
	}
	aliases := configuredAliasesForBackend(cfg, backend)
	switch len(aliases) {
	case 0:
		return "", []string{fmt.Sprintf("model restore: saved session recorded backend model %q, which no longer matches a configured model; using %s", backend, fallbackTarget(startupRef))}
	case 1:
		return aliases[0], nil
	default:
		if slices.Contains(aliases, startupRef) {
			return startupRef, nil
		}
		return "", []string{fmt.Sprintf("model restore: saved session backend model %q matches several configured models (%s); using %s", backend, strings.Join(aliases, ", "), fallbackTarget(startupRef))}
	}
}

// configuredAliasesForBackend lists, in sorted order, the configured model
// aliases whose backend id is backend.
func configuredAliasesForBackend(cfg config.Config, backend string) []string {
	aliases := make([]string, 0, len(cfg.Models.Definitions))
	for alias, model := range cfg.Models.Definitions {
		if strings.TrimSpace(model.ID) == backend {
			aliases = append(aliases, alias)
		}
	}
	slices.Sort(aliases)
	return aliases
}

// savedIdentity returns the provider alias and backend id the session recorded
// for its model, when the record carries them.
func savedIdentity(saved session.Session) (providerAlias, backendID string) {
	providerAlias = strings.TrimSpace(saved.ModelProvider)
	backendID = strings.TrimSpace(saved.Model)
	return providerAlias, backendID
}

// localModelUsageError reports why ref cannot be used from this machine without
// contacting a provider. It checks the reference, the provider configuration
// and locally resolvable credentials only. Model catalogs and metadata are not
// consulted, so a remote catalog cannot make an unusable model look local.
func localModelUsageError(cfg config.Config, ref, wantProvider, wantID string) error {
	model, isAlias := config.ResolveModelConfig(&cfg, ref)
	if !isAlias && (model.Provider == "" || strings.TrimSpace(model.ID) == "") {
		return errors.New("is not a configured model alias or provider/model reference")
	}
	providerCfg, ok := cfg.Providers[model.Provider]
	if !ok {
		return fmt.Errorf("uses provider %q, which is not configured", model.Provider)
	}
	if strings.TrimSpace(model.ID) == "" {
		return errors.New("has no backend model id")
	}
	if wantProvider != "" && model.Provider != wantProvider {
		return fmt.Errorf("now resolves to provider %q, not the saved provider %q", model.Provider, wantProvider)
	}
	if wantID != "" && strings.TrimSpace(model.ID) != wantID {
		return fmt.Errorf("now resolves to backend model %q, not the saved backend model %q", model.ID, wantID)
	}
	return localCredentialError(provider.ResolveProviderConfig(providerCfg))
}

// localCredentialError reports why a configured provider cannot be used from
// this machine, or nil when its locally resolvable credentials look usable.
func localCredentialError(resolved config.ProviderConfig) error {
	if resolved.Type == config.ProviderTypeCodex {
		// A stored sign-in or a locally resolvable API key both leave the
		// provider usable; neither needs a network call to check.
		if strings.TrimSpace(resolved.APIKey) != "" {
			return nil
		}
		identity, err := provider.LoadCodexIdentity()
		if err != nil {
			return fmt.Errorf("cannot read the codex sign-in: %w", err)
		}
		if !identity.SignedIn {
			return errors.New("needs a codex sign-in; run 'steiner login codex'")
		}
		return nil
	}
	if resolved.Type == config.ProviderTypeClaudeSubscription {
		if _, err := exec.LookPath("claude"); err != nil {
			return errors.New("needs the claude CLI on PATH; install Claude Code, then run 'claude auth login'")
		}
		return nil
	}
	if strings.TrimSpace(resolved.APIKeyEnv) != "" && strings.TrimSpace(resolved.APIKey) == "" {
		return fmt.Errorf("needs credentials: %s is unset", resolved.APIKeyEnv)
	}
	return nil
}

// fallbackWarning explains why the saved model was not applied and names the
// startup model kept in its place.
func fallbackWarning(label string, err error, fallbackRef string) string {
	return fmt.Sprintf("model restore: saved session model %s %v; using %s", label, err, fallbackTarget(fallbackRef))
}

// fallbackTarget names the startup selection kept after a restore fallback.
func fallbackTarget(ref string) string {
	if ref == "" {
		return "the configured startup model"
	}
	return fmt.Sprintf("startup model %q", ref)
}

// ignoredOverrideWarning reports an explicit startup model choice that the
// restored session model replaced. Profile defaults are not explicit choices,
// so they produce no warning.
func ignoredOverrideWarning(source, startupRef, restoredRef string) string {
	if startupRef == "" {
		return ""
	}
	switch source {
	case ModelSourceFlag:
		return fmt.Sprintf("model restore: session model %q overrides --model %q", restoredRef, startupRef)
	case ModelSourceEnv:
		return fmt.Sprintf("model restore: session model %q overrides STEINER_MODEL=%q", restoredRef, startupRef)
	default:
		return ""
	}
}

// savedModelLabel describes the saved selection in a warning.
func savedModelLabel(saved session.Session, resolvedRef string) string {
	if ref := strings.TrimSpace(saved.ModelRef); ref != "" {
		return fmt.Sprintf("%q", ref)
	}
	if backend := strings.TrimSpace(saved.Model); backend != "" {
		if resolvedRef != backend {
			return fmt.Sprintf("%q (backend model %q)", resolvedRef, backend)
		}
		return fmt.Sprintf("backend model %q", backend)
	}
	return fmt.Sprintf("%q", resolvedRef)
}

// effectiveModelRef returns the active orchestrator reference, falling back to
// the profile default.
func effectiveModelRef(cfg config.Config) string {
	if ref := strings.TrimSpace(cfg.Models.Effective.ActiveOrchestratorModel); ref != "" {
		return ref
	}
	return strings.TrimSpace(cfg.Models.Effective.DefaultModel)
}

// storedReasoningOverrides converts the session's live overrides to their
// persisted form, returning nil when none are set.
func storedReasoningOverrides(overrides map[string]provider.ReasoningOverride) map[string]session.ReasoningOverride {
	if len(overrides) == 0 {
		return nil
	}
	stored := make(map[string]session.ReasoningOverride, len(overrides))
	for ref, override := range overrides {
		switch override.Kind {
		case provider.ReasoningOverrideProviderDefault:
			stored[ref] = session.ReasoningOverride{Kind: string(provider.ReasoningOverrideProviderDefault)}
		case provider.ReasoningOverrideEffort:
			if effort := strings.TrimSpace(override.Effort); effort != "" {
				stored[ref] = session.ReasoningOverride{Kind: string(provider.ReasoningOverrideEffort), Effort: effort}
			}
		}
	}
	if len(stored) == 0 {
		return nil
	}
	return stored
}

// providerReasoningOverrides converts persisted overrides back to their live
// form, dropping entries that are not valid overrides.
func providerReasoningOverrides(stored map[string]session.ReasoningOverride) map[string]provider.ReasoningOverride {
	if len(stored) == 0 {
		return nil
	}
	converted := make(map[string]provider.ReasoningOverride, len(stored))
	for ref, override := range stored {
		switch provider.ReasoningOverrideKind(strings.TrimSpace(override.Kind)) {
		case provider.ReasoningOverrideProviderDefault:
			converted[ref] = provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault}
		case provider.ReasoningOverrideEffort:
			if effort := strings.TrimSpace(override.Effort); effort != "" {
				converted[ref] = provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: effort}
			}
		}
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

// modelIdentityLocked returns the persisted identity of the active model. The
// caller must hold s.mu.
func (s *Session) modelIdentityLocked() (ref, providerAlias, backendID string) {
	model := SelectedModelConfig(s.deps.Config)
	ref = effectiveModelRef(s.deps.Config)
	return ref, model.Provider, model.ID
}
