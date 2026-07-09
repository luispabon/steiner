package provider

// ReasoningCapabilities describes what reasoning effort options are known
// for a resolved model. SupportedEfforts and ProviderDefaultEffort are
// provider/model-native strings and are not normalized into a Steiner-owned
// enum. An empty SupportedEfforts means the model's reasoning options are
// unknown or the model does not support configurable reasoning effort.
type ReasoningCapabilities struct {
	SupportedEfforts      []string
	ProviderDefaultEffort string
	Source                string
	Confidence            string
}

// ReasoningRequest is the resolved runtime reasoning selection for a chat
// request. Effort is empty when no explicit effort should be sent, meaning
// the provider's own default applies.
type ReasoningRequest struct {
	Effort string
}

// ReasoningOverrideKind identifies the kind of session-time reasoning
// override applied on top of config-resolved reasoning state.
type ReasoningOverrideKind string

const (
	// ReasoningOverrideNone applies no override; config/provider resolution
	// is used unchanged.
	ReasoningOverrideNone ReasoningOverrideKind = "none"
	// ReasoningOverrideProviderDefault explicitly clears the effective
	// effort, even if config declared one, so the provider default applies.
	ReasoningOverrideProviderDefault ReasoningOverrideKind = "provider_default"
	// ReasoningOverrideEffort explicitly sets the effective effort to Effort.
	ReasoningOverrideEffort ReasoningOverrideKind = "effort"
)

// ReasoningOverride represents a session-time override of a resolved
// model's effective reasoning effort. Kind distinguishes "no override" from
// "explicit provider default" from "explicit effort value", since a nil/zero
// value alone cannot express all three states unambiguously.
type ReasoningOverride struct {
	Kind   ReasoningOverrideKind
	Effort string
}
