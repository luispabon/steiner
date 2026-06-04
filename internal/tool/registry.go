package tool

import (
	"sort"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// Registry stores tool definitions by name.
type Registry struct {
	defs map[string]ToolDef
}

// NewRegistry creates a new tool registry from the provided definitions.
func NewRegistry(defs ...ToolDef) *Registry {
	reg := &Registry{defs: make(map[string]ToolDef, len(defs))}
	for _, def := range defs {
		reg.Register(def)
	}
	return reg
}

// NewRegistryFromConfig creates a tool registry from the application configuration.
func NewRegistryFromConfig(cfg config.Config) *Registry {
	reg := &Registry{defs: make(map[string]ToolDef, len(cfg.Tools))}
	for name, toolCfg := range cfg.Tools {
		reg.Register(ToolDef{
			Name:            name,
			ExecPath:        toolCfg.Exec,
			Subcommand:      toolCfg.Subcommand,
			Description:     toolCfg.Description,
			ParameterSchema: CloneJSONMap(toolCfg.Parameters),
			Timeout:         time.Duration(toolCfg.Timeout.Duration()),
		})
	}
	return reg
}

// Register adds or replaces a tool definition.
func (r *Registry) Register(def ToolDef) {
	if r == nil || strings.TrimSpace(def.Name) == "" {
		return
	}
	if r.defs == nil {
		r.defs = make(map[string]ToolDef)
	}
	r.defs[def.Name] = cloneToolDef(def)
}

// Get returns a cloned tool definition by name.
func (r *Registry) Get(name string) (ToolDef, bool) {
	if r == nil {
		return ToolDef{}, false
	}
	def, ok := r.defs[name]
	if !ok {
		return ToolDef{}, false
	}
	return cloneToolDef(def), true
}

// Names returns the registered tool names in sorted order.
func (r *Registry) Names() []string {
	if r == nil || len(r.defs) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.defs))
	for name := range r.defs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Definitions returns all registered tool definitions in sorted order.
func (r *Registry) Definitions() []ToolDef {
	names := r.Names()
	defs := make([]ToolDef, 0, len(names))
	for _, name := range names {
		defs = append(defs, cloneToolDef(r.defs[name]))
	}
	return defs
}

// OpenAISchemas returns the registry definitions as OpenAI function schemas.
func (r *Registry) OpenAISchemas() []map[string]any {
	defs := r.Definitions()
	schemas := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		schemas = append(schemas, ToOpenAISchema(def))
	}
	return schemas
}

func cloneToolDef(def ToolDef) ToolDef {
	def.ParameterSchema = CloneJSONMap(def.ParameterSchema)
	return def
}

// Clone returns a deep copy of the registry. Mutating the clone does not affect the original.
func (r *Registry) Clone() *Registry {
	if r == nil {
		return NewRegistry()
	}
	clone := &Registry{defs: make(map[string]ToolDef, len(r.defs))}
	for name, def := range r.defs {
		clone.defs[name] = cloneToolDef(def)
	}
	return clone
}

// ToProviderSpecs converts the registry definitions to provider tool specs.
// The schema maps are cloned to prevent external mutation.
func (r *Registry) ToProviderSpecs() []provider.ToolSpec {
	if r == nil {
		return nil
	}
	defs := r.Definitions()
	if len(defs) == 0 {
		return nil
	}

	specs := make([]provider.ToolSpec, 0, len(defs))
	for _, def := range defs {
		specs = append(specs, provider.ToolSpec{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  CloneJSONMap(def.ParameterSchema),
			},
		})
	}
	return specs
}

// Subset returns a new Registry containing only definitions whose names appear
// in include and do not appear in exclude. If include is empty, the returned
// registry is empty.
func (r *Registry) Subset(include []string, exclude []string) *Registry {
	if r == nil || len(include) == 0 {
		return NewRegistry()
	}
	excludeSet := make(map[string]struct{}, len(exclude))
	for _, name := range exclude {
		excludeSet[name] = struct{}{}
	}
	includeSet := make(map[string]struct{}, len(include))
	for _, name := range include {
		includeSet[name] = struct{}{}
	}
	var filtered []ToolDef
	for _, name := range r.Names() {
		if _, excluded := excludeSet[name]; excluded {
			continue
		}
		if _, included := includeSet[name]; !included {
			continue
		}
		filtered = append(filtered, cloneToolDef(r.defs[name]))
	}
	return NewRegistry(filtered...)
}
