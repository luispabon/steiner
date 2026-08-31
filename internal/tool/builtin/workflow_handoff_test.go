package builtin

import (
	"context"
	"encoding/json"
	"fmt"
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
	if len(enum) != 3 || enum[0] != "implement" || enum[1] != "review" || enum[2] != "build" {
		t.Fatalf("next.enum = %v, want [implement review build]", enum)
	}
	nextDesc := toString(nextProp["description"])
	for _, want := range []string{
		"implement: structured `/implement` workflow using overview.md and plan.yaml",
		"review: structured `/review` workflow using overview.md and plan.yaml",
		"build: direct execution of standalone plan.md in build mode, without skill discovery",
	} {
		if !strings.Contains(nextDesc, want) {
			t.Fatalf("next.description missing %q in %q", want, nextDesc)
		}
	}

	targetProp, ok := props["target"].(map[string]any)
	if !ok {
		t.Fatal("target property missing or malformed")
	}
	if desc := targetProp["description"]; !strings.Contains(toString(desc), "directory") {
		t.Fatalf("target description = %q, want mention of directory", desc)
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
	env, events := workflowHandoffTestEnv(t, true, nil)
	env.WorkflowHandoffResponder = workflowHandoffDecisionResponder{
		response: tool.WorkflowHandoffResponse{Accepted: false},
		onRequest: func(req tool.WorkflowHandoffRequest) {
			env.EventSink.Emit(output.NewWorkflowHandoffRequestedEvent(req.Next, req.Target, req.Message, req.Submission))
		},
	}
	toolDef := NewWorkflowHandoffTool(env)

	msg := "  " + strings.Repeat("handoff note ", 60) + "  "
	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"next":    "implement",
		"target":  ".steiner/plans/step-1",
		"message": msg,
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}

	result, ok := resultI.(struct {
		Status string `json:"status"`
	})
	if !ok {
		t.Fatalf("result type = %T, want minimal declined struct", resultI)
	}
	if result.Status != "declined" {
		t.Fatalf("Status = %q, want declined", result.Status)
	}

	if len(*events) != 1 {
		t.Fatalf("events len = %d, want 1", len(*events))
	}
	payload, ok := (*events)[0].Payload.(output.WorkflowHandoffEvent)
	if !ok {
		t.Fatalf("event payload type = %T, want output.WorkflowHandoffEvent", (*events)[0].Payload)
	}
	if payload.Next != "implement" {
		t.Fatalf("event payload next = %q, want implement", payload.Next)
	}
	if payload.Target != ".steiner/plans/step-1" {
		t.Fatalf("event payload target = %q, want .steiner/plans/step-1", payload.Target)
	}
	if strings.TrimSpace(payload.Message) != payload.Message {
		t.Fatalf("event payload message = %q, want trimmed content", payload.Message)
	}
}

func TestWorkflowHandoffToolReturnsAcceptedControlResult(t *testing.T) {
	env, events := workflowHandoffTestEnv(t, true, nil)
	env.WorkflowHandoffResponder = workflowHandoffDecisionResponder{
		response: tool.WorkflowHandoffResponse{Accepted: true},
		onRequest: func(req tool.WorkflowHandoffRequest) {
			env.EventSink.Emit(output.NewWorkflowHandoffRequestedEvent(req.Next, req.Target, req.Message, req.Submission))
		},
	}
	toolDef := NewWorkflowHandoffTool(env)

	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"next":   "implement",
		"target": ".steiner/plans/step-1",
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}

	result, ok := resultI.(tool.WorkflowHandoffAccepted)
	if !ok {
		t.Fatalf("result type = %T, want tool.WorkflowHandoffAccepted", resultI)
	}
	if got, want := result.Transition.Next, "implement"; got != want {
		t.Fatalf("Transition.Next = %q, want implement", got)
	}
	if got, want := result.Transition.Target, ".steiner/plans/step-1"; got != want {
		t.Fatalf("Transition.Target = %q, want %q", got, want)
	}
	if len(*events) != 1 {
		t.Fatalf("events len = %d, want 1", len(*events))
	}
}

func TestWorkflowHandoffToolReturnsUnsupportedInNonInteractiveMode(t *testing.T) {
	env, events := workflowHandoffTestEnv(t, false, nil)
	toolDef := NewWorkflowHandoffTool(env)

	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"next":   "review",
		"target": ".steiner/plans/step-1",
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}

	result, ok := resultI.(struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	})
	if !ok {
		t.Fatalf("result type = %T, want unsupported status/reason struct", resultI)
	}
	if result.Status != "unsupported" {
		t.Fatalf("Status = %q, want unsupported", result.Status)
	}
	if result.Reason == "" {
		t.Fatal("Reason = empty, want bounded unsupported explanation")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("unsupported result marshal error = %v", err)
	}
	wantJSON := `{"status":"unsupported","reason":"workflow handoff decision handling is unavailable in non-interactive mode"}`
	if string(data) != wantJSON {
		t.Fatalf("unsupported result JSON = %s, want %s", data, wantJSON)
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
				"next":   "review",
				"target": filepath.Join(os.TempDir(), "plans", "step-1"),
			},
			want: "relative .steiner/plans",
		},
		{
			name: "target with newline",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1\nchild",
			},
			want: "control characters",
		},
		{
			name: "target with in-tree traversal",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1/../step-1",
			},
			want: "traversal",
		},
		{
			name: "target with control character",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1\x07child",
			},
			want: "control characters",
		},
		{
			name: "target with shell metacharacter semicolon",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1;child",
			},
			want: "shell metacharacters",
		},
		{
			name: "target with shell metacharacter ampersand",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1&child",
			},
			want: "shell metacharacters",
		},
		{
			name: "target with shell metacharacter backtick",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1`child",
			},
			want: "shell metacharacters",
		},
		{
			name: "target with shell metacharacter backslash",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/step-1\\child",
			},
			want: "shell metacharacters",
		},
		{
			name: "missing target directory",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/missing",
			},
			want: "does not exist",
		},
		{
			name: "missing plan artifacts",
			input: map[string]any{
				"next":   "review",
				"target": ".steiner/plans/incomplete",
			},
			want: "missing plan.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, events := workflowHandoffTestEnv(t, true, workflowHandoffDecisionResponder{
				response: tool.WorkflowHandoffResponse{Accepted: false},
			})
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

func TestWorkflowHandoffRegisteredTargetsHaveValidKeys(t *testing.T) {
	schema := WorkflowHandoffSchema()
	props := schemaProperties(schema)
	nextProp := props["next"].(map[string]any)
	enum := nextProp["enum"].([]string)

	enumMap := make(map[string]struct{})
	for _, k := range enum {
		enumMap[k] = struct{}{}
	}

	if len(enumMap) != len(enum) {
		t.Fatal("schema enum has duplicate keys")
	}

	for _, k := range enum {
		if lookupWorkflowTarget(k) == nil {
			t.Fatalf("schema enum %q not found in registered targets", k)
		}
	}
}

func TestWorkflowHandoffValidateUnknownTarget(t *testing.T) {
	env, _ := workflowHandoffTestEnv(t, true, workflowHandoffDecisionResponder{
		response: tool.WorkflowHandoffResponse{Accepted: false},
	})
	toolDef := NewWorkflowHandoffTool(env)

	_, err := toolDef.Handler(context.Background(), map[string]any{
		"next":   "unknown",
		"target": ".steiner/plans/step-1",
	})
	if err == nil {
		t.Fatal("expected error for unknown target, got nil")
	}
	if !strings.Contains(err.Error(), "next must be one of") {
		t.Fatalf("error = %v, want substring 'next must be one of'", err)
	}
}

// TestWorkflowHandoffGenericTargetSeam proves the generic validate path works for
// a synthetic target with a non-plan path prefix and different required files,
// without touching the registered plan target. This guards the #188 genericisation
// seam against regressing back to hardcoded plan-loop assumptions.
func TestWorkflowHandoffGenericTargetSeam(t *testing.T) {
	const prefix = "custom/specs"
	requiredFiles := []string{"spec.md", "design.md"}

	root := t.TempDir()
	abs := filepath.Join(root, prefix, "feature")
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", abs, err)
	}
	for _, name := range requiredFiles {
		if err := os.WriteFile(filepath.Join(abs, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
	}

	tests := []struct {
		name          string
		target        string
		prefix        string
		requiredFiles []string
		wantErr       string
	}{
		{
			name:          "valid synthetic target",
			target:        filepath.Join(prefix, "feature"),
			prefix:        prefix,
			requiredFiles: requiredFiles,
		},
		{
			name:          "prefix mismatch rejected",
			target:        ".steiner/plans/feature",
			prefix:        prefix,
			requiredFiles: requiredFiles,
			wantErr:       "must be under " + prefix,
		},
		{
			name:          "missing required file rejected",
			target:        filepath.Join(prefix, "feature"),
			prefix:        prefix,
			requiredFiles: []string{"spec.md", "missing.md"},
			wantErr:       "missing missing.md",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, absTarget, err := normalizeWorkflowHandoffTarget(root, tc.target, tc.prefix)
			if err == nil {
				err = validateWorkflowHandoffArtifacts(absTarget, tc.requiredFiles)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestWorkflowHandoffInputDecodeRejectsUnknownFields(t *testing.T) {
	_, err := decodeInput[WorkflowHandoffInput](map[string]any{
		"next":          "implement",
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

func TestBuildTargetAcceptsWithPlanMd(t *testing.T) {
	env, events := workflowHandoffTestEnv(t, true, nil)
	submissionCaptured := ""
	env.WorkflowHandoffResponder = workflowHandoffDecisionResponder{
		response: tool.WorkflowHandoffResponse{Accepted: true},
		onRequest: func(req tool.WorkflowHandoffRequest) {
			submissionCaptured = req.Submission
			env.EventSink.Emit(output.NewWorkflowHandoffRequestedEvent(req.Next, req.Target, req.Message, req.Submission))
		},
	}
	toolDef := NewWorkflowHandoffTool(env)

	resultI, err := toolDef.Handler(context.Background(), map[string]any{
		"next":   "build",
		"target": ".steiner/plans/build-plan",
	})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}

	if submissionCaptured == "" {
		t.Errorf("build target submission = %q, want non-empty submission", submissionCaptured)
	}
	if !strings.Contains(submissionCaptured, ".steiner/plans/build-plan") {
		t.Errorf("submission = %q, want to contain target path", submissionCaptured)
	}

	result, ok := resultI.(tool.WorkflowHandoffAccepted)
	if !ok {
		t.Fatalf("result type = %T, want tool.WorkflowHandoffAccepted", resultI)
	}
	if got, want := result.Transition.Next, "build"; got != want {
		t.Fatalf("Transition.Next = %q, want build", got)
	}
	if len(*events) != 1 {
		t.Fatalf("events len = %d, want 1", len(*events))
	}
	payload, ok := (*events)[0].Payload.(output.WorkflowHandoffEvent)
	if !ok {
		t.Fatalf("event payload type = %T, want output.WorkflowHandoffEvent", (*events)[0].Payload)
	}
	if payload.Submission == "" {
		t.Errorf("event payload submission = %q, want non-empty", payload.Submission)
	}
}

func TestBuildTargetRejectsWithoutPlanMd(t *testing.T) {
	env, _ := workflowHandoffTestEnv(t, true, nil)
	env.WorkflowHandoffResponder = workflowHandoffDecisionResponder{
		response: tool.WorkflowHandoffResponse{Accepted: false},
	}
	toolDef := NewWorkflowHandoffTool(env)

	_, err := toolDef.Handler(context.Background(), map[string]any{
		"next":   "build",
		"target": ".steiner/plans/incomplete",
	})
	if err == nil {
		t.Fatal("expected error for missing plan.md, got nil")
	}
	if !strings.Contains(err.Error(), "missing plan.md") {
		t.Fatalf("error = %v, want substring 'missing plan.md'", err)
	}
}

func TestExistingTargetsUnaffectedByBuildTarget(t *testing.T) {
	env, events := workflowHandoffTestEnv(t, true, nil)
	env.WorkflowHandoffResponder = workflowHandoffDecisionResponder{
		response: tool.WorkflowHandoffResponse{Accepted: true},
		onRequest: func(req tool.WorkflowHandoffRequest) {
			if req.Next == "build" {
				t.Fatalf("unexpected build target in existing target test")
			}
			if req.Submission != "" {
				t.Errorf("implement/review targets should have empty submission, got %q", req.Submission)
			}
			env.EventSink.Emit(output.NewWorkflowHandoffRequestedEvent(req.Next, req.Target, req.Message, req.Submission))
		},
	}
	toolDef := NewWorkflowHandoffTool(env)

	for _, target := range []string{"implement", "review"} {
		*events = (*events)[:0]
		_, err := toolDef.Handler(context.Background(), map[string]any{
			"next":   target,
			"target": ".steiner/plans/step-1",
		})
		if err != nil {
			t.Fatalf("handler error for %q = %v", target, err)
		}
		if len(*events) != 1 {
			t.Fatalf("events len for %q = %d, want 1", target, len(*events))
		}
		payload, ok := (*events)[0].Payload.(output.WorkflowHandoffEvent)
		if !ok {
			t.Fatalf("event payload type for %q = %T, want output.WorkflowHandoffEvent", target, (*events)[0].Payload)
		}
		if payload.Submission != "" {
			t.Errorf("%q target submission = %q, want empty", target, payload.Submission)
		}
	}
}

func workflowHandoffTestEnv(t *testing.T, interactive bool, responder tool.WorkflowHandoffResponder) (Env, *[]output.Event) {
	t.Helper()

	root := t.TempDir()
	mustWriteWorkflowHandoffTarget(t, root, ".steiner/plans/step-1", true, true)
	mustWriteWorkflowHandoffTarget(t, root, ".steiner/plans/incomplete", true, false)
	mustWriteBuildPlanTarget(t, root, ".steiner/plans/build-plan", true)

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	events := &[]output.Event{}
	env := Env{
		WorkDir:                  root,
		PathPolicy:               &policy,
		Interactive:              interactive,
		WorkflowHandoffResponder: responder,
		EventSink: output.SinkFunc(func(event output.Event) {
			*events = append(*events, event)
		}),
	}

	if !interactive {
		env.EventSink = nil
	}

	return env, events
}

type workflowHandoffDecisionResponder struct {
	response  tool.WorkflowHandoffResponse
	err       error
	onRequest func(tool.WorkflowHandoffRequest)
}

func (r workflowHandoffDecisionResponder) RequestWorkflowHandoff(_ context.Context, req tool.WorkflowHandoffRequest) (tool.WorkflowHandoffResponse, error) {
	if r.onRequest != nil {
		r.onRequest(req)
	}
	if r.err != nil {
		return tool.WorkflowHandoffResponse{}, fmt.Errorf("request workflow handoff: %w", r.err)
	}
	return r.response, nil
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

func mustWriteBuildPlanTarget(t *testing.T, root, rel string, withPlanMd bool) {
	t.Helper()

	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", abs, err)
	}
	if withPlanMd {
		if err := os.WriteFile(filepath.Join(abs, "plan.md"), []byte("# Build Plan\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(plan.md) error = %v", err)
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
