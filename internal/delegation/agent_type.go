package delegation

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed templates/*.txt
var agentTemplates embed.FS

// AgentType identifies a specialized delegate agent type.
type AgentType string

const (
	// AgentTypeExplore is the agent type for exploration tasks.
	AgentTypeExplore AgentType = "explore"
	// AgentTypeResearch is the agent type for research tasks.
	AgentTypeResearch AgentType = "research"
	// AgentTypeCode is the agent type for coding tasks.
	AgentTypeCode AgentType = "code"
	// AgentTypeEvaluate is the agent type for evaluation/analysis tasks.
	AgentTypeEvaluate AgentType = "evaluate"
	// AgentTypeSanityCheck is the agent type for verification tasks.
	AgentTypeSanityCheck AgentType = "sanity_check"
	// AgentTypeReview is the agent type for review tasks.
	AgentTypeReview AgentType = "review"
	// AgentTypeVision is the agent type for image analysis tasks.
	AgentTypeVision AgentType = "vision"
)

// SubAgentToolName is the name of the unified sub-agent delegation tool.
const SubAgentToolName = "sub_agent"

// cacheKeyAgentTypeAdvisor names the advisor's slot in the CacheKeyStore. The
// advisor is not a delegation agent type — this value is deliberately absent
// from AllAgentTypes and validAgentTypeSet (see below).
const cacheKeyAgentTypeAdvisor AgentType = "advisor"

// AllAgentTypes returns all valid agent type values.
func AllAgentTypes() []AgentType {
	return []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeCode, AgentTypeEvaluate, AgentTypeSanityCheck, AgentTypeReview, AgentTypeVision}
}

// IsDelegationTool reports whether name is a delegation tool registered by
// BuildDelegateRegistry: the sub_agent tool or follow_up.
func IsDelegationTool(name string) bool {
	return name == SubAgentToolName || name == FollowUpToolName
}

// AllSpecializedDelegateTools returns the canonical specialized delegate tool
// names used by delegation-aware UIs and other cross-package callers.
func AllSpecializedDelegateTools() []string {
	return []string{SubAgentToolName, FollowUpToolName}
}

// ValidAgentType reports whether s is a recognized agent type name.
func ValidAgentType(s string) bool {
	_, ok := validAgentTypeSet[s]
	return ok
}

// Preloaded agent prompts and code suffix (loaded at init).
var (
	explorePrompt      string
	researchPrompt     string
	evaluatePrompt     string
	sanityCheckPrompt  string
	reviewPrompt       string
	visionPrompt       string
	codeAgentSuffix    string
	advisorAgentSuffix string
)

var agentAllowlists = map[AgentType][]string{
	AgentTypeExplore:     {"read", "glob", "grep", "ls", "bash"},
	AgentTypeResearch:    {"read", "glob", "grep", "ls", "web_search", "fetch_url"},
	AgentTypeCode:        {"read", "glob", "grep", "ls", "mutate", "bash", "advisor"},
	AgentTypeEvaluate:    {"read", "glob", "grep", "ls", "advisor"},
	AgentTypeSanityCheck: {"read", "glob", "grep", "ls", "bash"},
	AgentTypeReview:      {"read", "glob", "grep", "ls", "bash", "advisor"},
	AgentTypeVision:      {"read"},
}

var validAgentTypeSet = map[string]struct{}{
	string(AgentTypeExplore):     {},
	string(AgentTypeResearch):    {},
	string(AgentTypeCode):        {},
	string(AgentTypeEvaluate):    {},
	string(AgentTypeSanityCheck): {},
	string(AgentTypeReview):      {},
	string(AgentTypeVision):      {},
}

func init() {
	mustLoadTemplate := func(filename string) string {
		data, err := fs.ReadFile(agentTemplates, "templates/"+filename)
		if err != nil {
			panic(fmt.Errorf("load template %q: %w", filename, err))
		}
		return string(data)
	}

	explorePrompt = mustLoadTemplate("explore.txt")
	researchPrompt = mustLoadTemplate("research.txt")
	evaluatePrompt = mustLoadTemplate("evaluate.txt")
	sanityCheckPrompt = mustLoadTemplate("sanity_check.txt")
	reviewPrompt = mustLoadTemplate("review.txt")
	visionPrompt = mustLoadTemplate("vision.txt")
	codeAgentSuffix = mustLoadTemplate("code_suffix.txt")
	advisorAgentSuffix = mustLoadTemplate("advisor_suffix.txt")
}

// AgentSystemPrompt returns the system prompt for the given agent type.
func AgentSystemPrompt(t AgentType) string {
	switch t {
	case AgentTypeExplore:
		return explorePrompt
	case AgentTypeResearch:
		return researchPrompt
	case AgentTypeEvaluate:
		return evaluatePrompt
	case AgentTypeSanityCheck:
		return sanityCheckPrompt
	case AgentTypeReview:
		return reviewPrompt
	case AgentTypeVision:
		return visionPrompt
	case AgentTypeCode:
		return ""
	default:
		return ""
	}
}

// AgentAllowedTools returns the tool allowlist for the given agent type.
func AgentAllowedTools(t AgentType) []string {
	if tools, ok := agentAllowlists[t]; ok {
		return append([]string(nil), tools...)
	}
	return nil
}

// AgentSystemSuffix returns the system suffix for the given agent type.
// When advisorEnabled is true and the agent type supports advisor, appends the
// advisor guidance to its base suffix.
func AgentSystemSuffix(t AgentType, advisorEnabled bool) string {
	baseSuffix := ""
	if t == AgentTypeCode {
		baseSuffix = codeAgentSuffix
	}

	if !advisorEnabled {
		return baseSuffix
	}

	if t != AgentTypeCode && t != AgentTypeReview && t != AgentTypeEvaluate {
		return baseSuffix
	}

	if baseSuffix != "" {
		return strings.TrimRight(baseSuffix, "\n") + "\n\n" + advisorAgentSuffix
	}
	return advisorAgentSuffix
}
