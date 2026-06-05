package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func TestWorkflowHandoffSchema(t *testing.T) {
	schema := WorkflowHandoffSchema()

	if got := schemaType(schema); got != "object" {
		t.Fatalf("type = %q, want object", got)
	}
	if schemaAdditionalProperties(schema) {
		t.Fatal("additionalProperties = true, want false")
	}

	req := schemaRequired(schema)
	if len(req) != 2 || req[0] != "next" || req[1] != "target" {
		t.Fatalf("required = %v, want [next target]", req)
	}

	props := schemaProperties(schema)
	if props == nil {
		t.Fatal("properties is nil")
	}
	if _, ok := props["clear_context"]; ok {
		t.Fatal("schema unexpectedly exposes clear_context")
	}

	nextProp, ok := props["next"].(map[string]any)
	if !ok {
		t.Fatal("next property missing or malformed")
	}
	enum, ok := nextProp["enum"].([]string)
	if !ok {
		t.Fatalf("next.enum type = %T, want []string", nextProp["enum"])
	}
	if len(enum) != 2 || enum[0] != workflowHandoffNextImplement || enum[1] != workflowHandoffNextReview {
		t.Fatalf("next.enum = %v, want [%s %s]", enum, workflowHandoffNextImplement, workflowHandoffNextReview)
	}

	targetProp, ok := props["target"].(map[string]any)
	if !ok {
		t.Fatal("target property missing or malformed")
	}
	if desc := targetProp["description"]; !strings.Contains(toString(desc), workflowHandoffTargetPrefix) {
		t.Fatalf("target description = %q, want mention of %q", desc, workflowHandoffTargetPrefix)
	}

	messageProp, ok := props["message"].(map[string]any)
	if !ok {
		t.Fatal("message property missing or malformed")
	}
	if got, ok := messageProp["maxLength"].(int); !ok || got != workflowHandoffMessageMaxRunes {
		t.Fatalf("message.maxLength = %v, want %d", messageProp["maxLength"], workflowHandoffMessageMaxRunes)
	}
}

func TestWorkflowHandoffToolCreatesPendingRequest(t *testing.T) {
	env, events := workflowHandoffTestEnv(t, true)
	toolDef := NewWorkflowHandoffTool(env)

	msg := "  " + strings.Repeat("handoff note ", 60) + "  "
	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"next":    workflowHandoffNextImplement,
		"target":  ".steiner/plans/step-1",
		"message": msg,
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}

	result, ok := resultI.(*WorkflowHandoffResult)
	if !ok {
		t.Fatalf("result type = %T, want *WorkflowHandoffResult", resultI)
	}
	if result.Status != "pending" {
		t.Fatalf("Status = %q, want pending", result.Status)
	}
	if result.Next != workflowHandoffNextImplement {
		t.Fatalf("Next = %q, want %q", result.Next, workflowHandoffNextImplement)
	}
	if result.Target != ".steiner/plans/step-1" {
		t.Fatalf("Target = %q, want .steiner/plans/step-1", result.Target)
	}
	if strings.TrimSpace(result.Message) != result.Message {
		t.Fatalf("Message = %q, want trimmed content", result.Message)
	}
	if len([]rune(result.Message)) > workflowHandoffMessageMaxRunes {
		t.Fatalf("Message rune length = %d, want <= %d", len([]rune(result.Message)), workflowHandoffMessageMaxRunes)
	}
	if !result.MessageTruncated {
		t.Fatal("MessageTruncated = false, want true for long message")
	}

	if len(*events) != 1 {
		t.Fatalf("events len = %d, want 1", len(*events))
	}
	payload, ok := (*events)[0].Payload.(output.WorkflowHandoffEvent)
	if !ok {
		t.Fatalf("event payload type = %T, want output.WorkflowHandoffEvent", (*events)[0].Payload)
	}
	if payload.Next != workflowHandoffNextImplement {
		t.Fatalf("event payload next = %q, want %q", payload.Next, workflowHandoffNextImplement)
	}
	if payload.Target != ".steiner/plans/step-1" {
		t.Fatalf("event payload target = %q, want .steiner/plans/step-1", payload.Target)
	}
	if strings.TrimSpace(payload.Message) != payload.Message {
		t.Fatalf("event payload message = %q, want trimmed content", payload.Message)
	}
}

func TestWorkflowHandoffToolReturnsUnsupportedInNonInteractiveMode(t *testing.T) {
	env, events := workflowHandoffTestEnv(t, false)
	toolDef := NewWorkflowHandoffTool(env)

	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"next":   workflowHandoffNextReview,
		"target": ".steiner/plans/step-1",
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}

	result, ok := resultI.(*WorkflowHandoffResult)
	if !ok {
		t.Fatalf("result type = %T, want *WorkflowHandoffResult", resultI)
	}
	if result.Status != "unsupported" {
		t.Fatalf("Status = %q, want unsupported", result.Status)
	}
	if result.Reason == "" {
		t.Fatal("Reason = empty, want bounded unsupported explanation")
	}
	if len(*events) != 0 {
		t.Fatalf("events len = %d, want 0", len(*events))
	}
}

func TestWorkflowHandoffToolRejectsInvalidInputWithoutEvents(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{
			name: "invalid next",
			input: map[string]any{
				"next":   "plan",
				"target": ".steiner/plans/step-1",
			},
			want: "next must be one of",
		},
		{
			name: "unsafe absolute target",
			input: map[string]any{
				"next":   workflowHandoffNextReview,
				"target": filepath.Join(os.TempDir(), "plans", "step-1"),
			},
			want: "relative .steiner/plans",
		},
		{
			name: "missing target directory",
			input: map[string]any{
				"next":   workflowHandoffNextReview,
				"target": ".steiner/plans/missing",
			},
			want: "does not exist",
		},
		{
			name: "missing plan artifacts",
			input: map[string]any{
				"next":   workflowHandoffNextReview,
				"target": ".steiner/plans/incomplete",
			},
			want: "missing plan.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, events := workflowHandoffTestEnv(t, true)
			toolDef := NewWorkflowHandoffTool(env)

			_, err := toolDef.Handler(context.Background(), tt.input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if len(*events) != 0 {
				t.Fatalf("events len = %d, want 0", len(*events))
			}
		})
	}
}

func TestWorkflowHandoffInputDecodeRejectsUnknownFields(t *testing.T) {
	_, err := decodeInput[WorkflowHandoffInput](map[string]any{
		"next":          workflowHandoffNextImplement,
		"target":        ".steiner/plans/step-1",
		"clear_context": true,
	})
	if err == nil {
		t.Fatal("decodeInput(workflow_handoff) = nil error, want unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field \"clear_context\"") {
		t.Fatalf("decodeInput(workflow_handoff) error = %v, want unknown field clear_context", err)
	}
}

func workflowHandoffTestEnv(t *testing.T, interactive bool) (Env, *[]output.Event) {
	t.Helper()

	root := t.TempDir()
	mustWriteWorkflowHandoffTarget(t, root, ".steiner/plans/step-1", true, true)
	mustWriteWorkflowHandoffTarget(t, root, ".steiner/plans/incomplete", true, false)

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	events := &[]output.Event{}
	env := Env{
		WorkDir:     root,
		PathPolicy:  &policy,
		Interactive: interactive,
		EventSink: output.SinkFunc(func(event output.Event) {
			*events = append(*events, event)
		}),
	}

	if !interactive {
		env.EventSink = nil
	}

	return env, events
}

func mustWriteWorkflowHandoffTarget(t *testing.T, root, rel string, overview, plan bool) {
	t.Helper()

	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", abs, err)
	}
	if overview {
		if err := os.WriteFile(filepath.Join(abs, "overview.md"), []byte("# overview\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(overview.md) error = %v", err)
		}
	}
	if plan {
		if err := os.WriteFile(filepath.Join(abs, "plan.yaml"), []byte("steps: []\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(plan.yaml) error = %v", err)
		}
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
