package tool

import (
	"sort"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

type Registry struct {
	defs map[string]ToolDef
}

func NewRegistry(defs ...ToolDef) *Registry {
	reg := &Registry{defs: make(map[string]ToolDef, len(defs))}
	for _, def := range defs {
		reg.Register(def)
	}
	return reg
}

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
			Approval:        toolCfg.Approval,
		})
	}
	return reg
}

func (r *Registry) Register(def ToolDef) {
	if r == nil || strings.TrimSpace(def.Name) == "" {
		return
	}
	if r.defs == nil {
		r.defs = make(map[string]ToolDef)
	}
	r.defs[def.Name] = cloneToolDef(def)
}

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

func (r *Registry) Definitions() []ToolDef {
	names := r.Names()
	defs := make([]ToolDef, 0, len(names))
	for _, name := range names {
		defs = append(defs, cloneToolDef(r.defs[name]))
	}
	return defs
}

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
