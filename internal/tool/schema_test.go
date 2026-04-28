package tool

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestToOpenAISchemaBuildsFunctionEnvelope(t *testing.T) {
	def := ToolDef{
		Name:        "grep",
		Description: "Search files",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
			},
			"required": []any{"pattern"},
		},
	}

	schema := ToOpenAISchema(def)
	if got := schema["type"]; got != "function" {
		t.Fatalf("schema[type] = %v, want function", got)
	}

	function, ok := schema["function"].(map[string]any)
	if !ok {
		t.Fatalf("schema[function] type = %T, want map[string]any", schema["function"])
	}
	if got := function["name"]; got != "grep" {
		t.Fatalf("function[name] = %v, want grep", got)
	}
	if got := function["description"]; got != "Search files" {
		t.Fatalf("function[description] = %v, want Search files", got)
	}

	parameters, ok := function["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("function[parameters] type = %T, want map[string]any", function["parameters"])
	}
	if got := parameters["type"]; got != "object" {
		t.Fatalf("parameters[type] = %v, want object", got)
	}
	if got := parameters["additionalProperties"]; got != false {
		t.Fatalf("parameters[additionalProperties] = %v, want false", got)
	}
	if got := parameters["required"]; !reflect.DeepEqual(got, []any{"pattern"}) {
		t.Fatalf("parameters[required] = %#v, want %#v", got, []any{"pattern"})
	}
}

func TestToOpenAISchemaDefaultsParameters(t *testing.T) {
	schema := ToOpenAISchema(ToolDef{Name: "noop"})

	function, ok := schema["function"].(map[string]any)
	if !ok {
		t.Fatalf("schema[function] type = %T, want map[string]any", schema["function"])
	}
	parameters, ok := function["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("function[parameters] type = %T, want map[string]any", function["parameters"])
	}
	if got := parameters["type"]; got != "object" {
		t.Fatalf("parameters[type] = %v, want object", got)
	}
	if got := parameters["properties"]; !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("parameters[properties] = %#v, want empty object", got)
	}
}

func TestNewRegistryFromConfigCopiesDefinitions(t *testing.T) {
	cfg := config.Config{
		Tools: map[string]config.ToolConfig{
			"read": {
				Exec:        "/bin/read",
				Subcommand:  "file",
				Description: "Read a file",
				Parameters: map[string]any{
					"type": "object",
				},
				Timeout:  config.MustDuration("5s"),
				Approval: config.ApprovalModeAuto,
			},
		},
	}

	reg := NewRegistryFromConfig(cfg)
	def, ok := reg.Get("read")
	if !ok {
		t.Fatal("Get(read) = false, want true")
	}
	if def.Name != "read" {
		t.Fatalf("Name = %q, want read", def.Name)
	}
	if def.ExecPath != "/bin/read" {
		t.Fatalf("ExecPath = %q, want /bin/read", def.ExecPath)
	}
	if def.Subcommand != "file" {
		t.Fatalf("Subcommand = %q, want file", def.Subcommand)
	}
	if def.Description != "Read a file" {
		t.Fatalf("Description = %q, want Read a file", def.Description)
	}
	if def.Timeout != 5_000_000_000 {
		t.Fatalf("Timeout = %v, want 5s", def.Timeout)
	}
	if def.Approval != config.ApprovalModeAuto {
		t.Fatalf("Approval = %q, want auto", def.Approval)
	}
}

func TestRegistryOpenAISchemas(t *testing.T) {
	reg := NewRegistry(
		ToolDef{Name: "write", Description: "Write files"},
		ToolDef{Name: "edit", Description: "Edit files safely"},
		ToolDef{Name: "read", Description: "Read files"},
	)

	schemas := reg.OpenAISchemas()
	if got := len(schemas); got != 3 {
		t.Fatalf("len(OpenAISchemas) = %d, want 3", got)
	}

	firstFunction, ok := schemas[0]["function"].(map[string]any)
	if !ok {
		t.Fatalf("schemas[0][function] type = %T, want map[string]any", schemas[0]["function"])
	}
	if got := firstFunction["name"]; got != "edit" {
		t.Fatalf("schemas[0].function.name = %v, want edit", got)
	}

	secondFunction, ok := schemas[1]["function"].(map[string]any)
	if !ok {
		t.Fatalf("schemas[1][function] type = %T, want map[string]any", schemas[1]["function"])
	}
	if got := secondFunction["name"]; got != "read" {
		t.Fatalf("schemas[1].function.name = %v, want read", got)
	}

	thirdFunction, ok := schemas[2]["function"].(map[string]any)
	if !ok {
		t.Fatalf("schemas[2][function] type = %T, want map[string]any", schemas[2]["function"])
	}
	if got := thirdFunction["name"]; got != "write" {
		t.Fatalf("schemas[2].function.name = %v, want write", got)
	}
}
