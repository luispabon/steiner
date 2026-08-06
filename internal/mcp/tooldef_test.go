package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestMCPToolDefSchema(t *testing.T) {
	sess := &Session{name: "fixture"}

	tests := []struct {
		name  string
		input any
		want  map[string]any
	}{
		{
			name:  "nil input schema gets the default empty object schema",
			input: nil,
			want:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			name:  "non-map input schema gets the default empty object schema",
			input: "not a schema",
			want:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			name:  "valid input schema passes through",
			input: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			want:  map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo", Description: "echoes text", InputSchema: tt.input}, func() tool.ApprovalResponder { return nil }, func() bool { return false }, config.MCPServerConfig{Approval: "ask"}, config.LimitsConfig{})
			if !reflect.DeepEqual(def.ParameterSchema, tt.want) {
				t.Errorf("ParameterSchema = %#v, want %#v", def.ParameterSchema, tt.want)
			}
		})
	}
}

func TestMCPToolDefProvenance(t *testing.T) {
	sess := &Session{name: "fixture"}
	def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo", Description: "echoes text"}, func() tool.ApprovalResponder { return nil }, func() bool { return false }, config.MCPServerConfig{Approval: "ask"}, config.LimitsConfig{})

	if def.Name != "mcp__fixture__echo" {
		t.Errorf("Name = %q, want %q", def.Name, "mcp__fixture__echo")
	}
	want := tool.MCPProvenance{Server: "fixture", ToolName: "echo"}
	if def.MCP != want {
		t.Errorf("MCP = %+v, want %+v", def.MCP, want)
	}
	if def.Handler == nil {
		t.Error("Handler is nil, want non-nil")
	}
	// Handler dispatch only: no subprocess fields.
	if def.ExecPath != "" || def.Subcommand != "" || def.Timeout != 0 {
		t.Errorf("ExecPath=%q Subcommand=%q Timeout=%v, want all zero", def.ExecPath, def.Subcommand, def.Timeout)
	}
}

func TestMCPHandlerFailClosed(t *testing.T) {
	t.Run("nil approver denies", func(t *testing.T) {
		sess := &Session{name: "fixture"}
		def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo"}, func() tool.ApprovalResponder { return nil }, func() bool { return false }, config.MCPServerConfig{Approval: "ask"}, config.LimitsConfig{})

		env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err)
	})

	t.Run("approver error denies", func(t *testing.T) {
		approver := tool.ApprovalResponderFunc(func(context.Context, tool.ApprovalRequest) error {
			return errors.New("approval transport down")
		})
		sess := &Session{name: "fixture"}
		def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo"}, func() tool.ApprovalResponder { return approver }, func() bool { return false }, config.MCPServerConfig{Approval: "ask"}, config.LimitsConfig{})

		env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err)
	})
}

// TestMCPHandlerPlanModeDynamic proves the handler reads plan mode from the
// closure at call time: toggling the mode changes approval behaviour without
// rebuilding the tool definition.
func TestMCPHandlerPlanModeDynamic(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())
	sess, err := ConnectSession(context.Background(), ServerSpec{Name: "fixture", Command: fixtureBin}, nil, io.Discard, 0)
	if err != nil {
		t.Fatalf("connect fixture: %v", err)
	}
	defer sess.Close() //nolint:errcheck

	planMode := false
	approver := &recordingApprover{allow: true}
	def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo"}, func() tool.ApprovalResponder { return approver }, func() bool { return planMode }, config.MCPServerConfig{Approval: "allow"}, config.LimitsConfig{})

	// Build mode: allow calls the tool without prompting.
	env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("build-mode call returned Go error %v, want nil", err)
	}
	envelope, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("build-mode result type = %T, want tool.JSONEnvelope", env)
	}
	if !envelope.OK || envelope.Result != "hi" {
		t.Errorf("build-mode result = %+v, want OK with result %q", envelope, "hi")
	}
	if len(approver.reqs) != 0 {
		t.Fatalf("RequestApproval invoked %d times in build mode, want 0", len(approver.reqs))
	}

	// Plan mode on the same def: allow downgrades to ask.
	planMode = true
	env, err = def.Handler(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("plan-mode call returned Go error %v, want nil", err)
	}
	envelope, ok = env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("plan-mode result type = %T, want tool.JSONEnvelope", env)
	}
	if !envelope.OK || envelope.Result != "hi" {
		t.Errorf("plan-mode result = %+v, want OK with result %q after approval", envelope, "hi")
	}
	if len(approver.reqs) != 1 {
		t.Fatalf("RequestApproval invoked %d times in plan mode, want 1", len(approver.reqs))
	}

	// Back to build mode: prompting stops without rebuilding the definition.
	planMode = false
	if _, err = def.Handler(context.Background(), map[string]any{"text": "hi"}); err != nil {
		t.Fatalf("build-mode call after toggle returned Go error %v, want nil", err)
	}
	if len(approver.reqs) != 1 {
		t.Fatalf("RequestApproval invoked %d times after toggling back to build mode, want still 1", len(approver.reqs))
	}
}

// recordingApprover records every request and answers with a fixed decision.
type recordingApprover struct {
	allow bool
	reqs  []tool.ApprovalRequest
}

func (r *recordingApprover) RequestApproval(_ context.Context, req tool.ApprovalRequest) error {
	r.reqs = append(r.reqs, req)
	req.Response <- tool.ApprovalResponse{Allow: r.allow}
	return nil
}

// TestApproval exercises the handler approval gate across the mode × planMode ×
// trust_annotations × annotations × approver matrix (step 7, D3/D6/D7).
func TestApproval(t *testing.T) {
	// A live fixture session lets the auto-allowed paths prove the tool call
	// actually reaches the server (echo returns OK with the input text).
	fixtureBin := buildFixture(t, t.TempDir())
	sess, err := ConnectSession(context.Background(), ServerSpec{Name: "fixture", Command: fixtureBin}, nil, io.Discard, 0)
	if err != nil {
		t.Fatalf("connect fixture: %v", err)
	}
	defer sess.Close() //nolint:errcheck

	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name        string
		srv         config.MCPServerConfig
		planMode    bool
		annotations *mcpsdk.ToolAnnotations
		approver    tool.ApprovalResponder // nil exercises the fail-closed path
		wantDenied  bool
		wantOK      bool
		wantResult  string
		wantPrompt  bool // RequestApproval must have been invoked
		wantPreview string
	}{
		{
			name:       "deny server denies immediately",
			srv:        config.MCPServerConfig{Approval: "deny"},
			wantDenied: true,
		},
		{
			name:       "allow server in build mode calls without approval",
			srv:        config.MCPServerConfig{Approval: "allow"},
			approver:   &recordingApprover{allow: true},
			wantOK:     true,
			wantResult: "hi",
		},
		{
			name:       "allow server in plan mode downgrades to ask",
			srv:        config.MCPServerConfig{Approval: "allow"},
			planMode:   true,
			approver:   &recordingApprover{allow: true},
			wantOK:     true,
			wantResult: "hi",
			wantPrompt: true,
		},
		{
			name:        "ask server prompts with formatted arguments",
			srv:         config.MCPServerConfig{Approval: "ask"},
			approver:    &recordingApprover{allow: true},
			wantOK:      true,
			wantResult:  "hi",
			wantPrompt:  true,
			wantPreview: "text: hi",
		},
		{
			name:       "empty approval mode defaults to ask",
			srv:        config.MCPServerConfig{},
			approver:   &recordingApprover{allow: true},
			wantOK:     true,
			wantResult: "hi",
			wantPrompt: true,
		},
		{
			name:       "declined prompt denies",
			srv:        config.MCPServerConfig{Approval: "ask"},
			approver:   &recordingApprover{allow: false},
			wantDenied: true,
			wantPrompt: true,
		},
		{
			name:        "trusted readOnlyHint skips approval",
			srv:         config.MCPServerConfig{Approval: "ask", TrustAnnotations: true},
			annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
			approver:    &recordingApprover{allow: true},
			wantOK:      true,
			wantResult:  "hi",
		},
		{
			name:        "trusted readOnlyHint skips approval even with a nil approver",
			srv:         config.MCPServerConfig{Approval: "ask", TrustAnnotations: true},
			annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
			wantOK:      true,
			wantResult:  "hi",
		},
		{
			name:        "trusted destructiveHint still prompts",
			srv:         config.MCPServerConfig{Approval: "ask", TrustAnnotations: true},
			annotations: &mcpsdk.ToolAnnotations{DestructiveHint: boolPtr(true)},
			approver:    &recordingApprover{allow: true},
			wantOK:      true,
			wantResult:  "hi",
			wantPrompt:  true,
		},
		{
			name:        "trusted non-destructive closed-world skips approval",
			srv:         config.MCPServerConfig{Approval: "ask", TrustAnnotations: true},
			annotations: &mcpsdk.ToolAnnotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
			approver:    &recordingApprover{allow: true},
			wantOK:      true,
			wantResult:  "hi",
		},
		{
			name:        "trusted empty annotations default to destructive and open world, prompting",
			srv:         config.MCPServerConfig{Approval: "ask", TrustAnnotations: true},
			annotations: &mcpsdk.ToolAnnotations{},
			approver:    &recordingApprover{allow: true},
			wantOK:      true,
			wantResult:  "hi",
			wantPrompt:  true,
		},
		{
			name:        "trust_annotations false ignores hints and prompts",
			srv:         config.MCPServerConfig{Approval: "ask", TrustAnnotations: false},
			annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true},
			approver:    &recordingApprover{allow: true},
			wantOK:      true,
			wantResult:  "hi",
			wantPrompt:  true,
		},
		{
			name:       "nil approver fails closed",
			srv:        config.MCPServerConfig{Approval: "ask"},
			wantDenied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo", Annotations: tt.annotations}, func() tool.ApprovalResponder { return tt.approver }, func() bool { return tt.planMode }, tt.srv, config.LimitsConfig{})
			env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})

			switch {
			case tt.wantDenied:
				assertDenial(t, env, err)
			case tt.wantOK:
				if err != nil {
					t.Fatalf("handler returned Go error %v, want nil", err)
				}
				envelope, ok := env.(tool.JSONEnvelope)
				if !ok {
					t.Fatalf("result type = %T, want tool.JSONEnvelope", env)
				}
				if !envelope.OK {
					t.Errorf("OK = false, want true (error: %+v)", envelope.Error)
				}
				if envelope.Result != tt.wantResult {
					t.Errorf("result = %q, want %q", envelope.Result, tt.wantResult)
				}
			default:
				t.Fatal("test case has no expected outcome")
			}

			var reqs []tool.ApprovalRequest
			if rec, ok := tt.approver.(*recordingApprover); ok {
				reqs = rec.reqs
			}
			gotPrompt := len(reqs) == 1
			if gotPrompt != tt.wantPrompt {
				t.Errorf("RequestApproval invoked = %v (recorded %d requests), want %v", gotPrompt, len(reqs), tt.wantPrompt)
			}
			if tt.wantPrompt && tt.wantPreview != "" && reqs[0].MCP.ArgumentsPreview != tt.wantPreview {
				t.Errorf("ArgumentsPreview = %q, want %q", reqs[0].MCP.ArgumentsPreview, tt.wantPreview)
			}
		})
	}
}

// TestMCPToolDefOutputTruncation proves D19's output bound: an oversized result
// is cut to ToolOutputMaxBytes and tagged with the Summary()-style marker whose
// total is the full pre-truncation size. A result under the limit passes through
// unchanged.
func TestMCPToolDefOutputTruncation(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())
	sess, err := ConnectSession(context.Background(), ServerSpec{Name: "fixture", Command: fixtureBin}, nil, io.Discard, 0)
	if err != nil {
		t.Fatalf("connect fixture: %v", err)
	}
	defer sess.Close() //nolint:errcheck

	limit := 1024
	def := mcpToolDef(sess, &mcpsdk.Tool{Name: "big_output"}, func() tool.ApprovalResponder { return nil }, func() bool { return false }, config.MCPServerConfig{Approval: "allow"}, config.LimitsConfig{ToolOutputMaxBytes: limit})

	env, err := def.Handler(context.Background(), map[string]any{"bytes": float64(200000)})
	if err != nil {
		t.Fatalf("big_output returned Go error %v, want nil", err)
	}
	envelope, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("big_output result type = %T, want tool.JSONEnvelope", env)
	}
	if !envelope.OK {
		t.Fatalf("big_output OK = false, want true (error: %+v)", envelope.Error)
	}
	result, ok := envelope.Result.(string)
	if !ok {
		t.Fatalf("big_output result type = %T, want string", envelope.Result)
	}
	wantMarker := fmt.Sprintf("\n<truncated output shown=%d total=%d>", limit, 200000)
	if !strings.HasSuffix(result, wantMarker) {
		t.Errorf("result does not end with marker %q: %q", wantMarker, result)
	}
	if got := len(result); got != limit+len(wantMarker) {
		t.Errorf("result length = %d, want %d (limit + marker)", got, limit+len(wantMarker))
	}

	// A result under the limit is returned verbatim, without a marker.
	env, err = def.Handler(context.Background(), map[string]any{"bytes": float64(32)})
	if err != nil {
		t.Fatalf("small big_output returned Go error %v, want nil", err)
	}
	envelope, ok = env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("small big_output result type = %T, want tool.JSONEnvelope", env)
	}
	if !envelope.OK || envelope.Result != strings.Repeat("X", 32) {
		t.Errorf("small result = %+v, want OK with 32 X's", envelope)
	}
}

// TestMCPToolDefTimeout proves D19's per-call timeout: a tool that outlives its
// budget fails with a deadline error wrapped as a Go error (never an envelope),
// and the call returns promptly instead of waiting for the server. The per-tool
// entry (keyed by the full registered tool name) wins over the default, and the
// default applies when no entry exists.
func TestMCPToolDefTimeout(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())

	tests := []struct {
		name   string
		limits config.LimitsConfig
	}{
		{
			name: "per-tool timeout beats the default",
			limits: config.LimitsConfig{
				ToolTimeoutDefault: config.MustDuration("60s"),
				ToolTimeouts:       map[string]config.Duration{ToolName("fixture", "sleep"): config.MustDuration("50ms")},
			},
		},
		{
			name: "default applies when no per-tool entry exists",
			limits: config.LimitsConfig{
				ToolTimeoutDefault: config.MustDuration("50ms"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, err := ConnectSession(context.Background(), ServerSpec{Name: "fixture", Command: fixtureBin}, nil, io.Discard, 0)
			if err != nil {
				t.Fatalf("connect fixture: %v", err)
			}
			defer sess.Close() //nolint:errcheck

			def := mcpToolDef(sess, &mcpsdk.Tool{Name: "sleep"}, func() tool.ApprovalResponder { return nil }, func() bool { return false }, config.MCPServerConfig{Approval: "allow"}, tt.limits)
			start := time.Now()
			_, err = def.Handler(context.Background(), map[string]any{"ms": float64(5000)})
			if err == nil {
				t.Fatal("sleep call returned nil error, want a timeout error")
			}
			if !strings.Contains(err.Error(), "deadline exceeded") {
				t.Errorf("sleep error %q does not report the deadline", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("sleep call took %v, want it bounded by the 50ms timeout", elapsed)
			}
		})
	}
}

// TestMCPToolDefIsErrorNoReconnect proves D19's distinct error channels: a
// tool-level failure (fixture boom sets isError) surfaces as a non-OK envelope
// with a nil Go error, so it is returned to the model and never mistaken for a
// transport failure that would trigger reconnect.
func TestMCPToolDefIsErrorNoReconnect(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())
	sess, err := ConnectSession(context.Background(), ServerSpec{Name: "fixture", Command: fixtureBin}, nil, io.Discard, 0)
	if err != nil {
		t.Fatalf("connect fixture: %v", err)
	}
	defer sess.Close() //nolint:errcheck

	def := mcpToolDef(sess, &mcpsdk.Tool{Name: "boom"}, func() tool.ApprovalResponder { return nil }, func() bool { return false }, config.MCPServerConfig{Approval: "allow"}, config.LimitsConfig{})

	env, err := def.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("boom returned Go error %v, want nil (tool-level failure)", err)
	}
	envelope, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("boom result type = %T, want tool.JSONEnvelope", env)
	}
	if envelope.OK {
		t.Error("OK = true, want false")
	}
	if envelope.Error == nil || envelope.Error.Kind != "mcp_tool_error" {
		t.Errorf("error = %+v, want kind mcp_tool_error", envelope.Error)
	}
	if envelope.Error == nil || envelope.Error.Message != "deliberate failure" {
		t.Errorf("error message = %+v, want %q", envelope.Error, "deliberate failure")
	}
}

func TestFormatArgumentsPreview(t *testing.T) {
	long := strings.Repeat("x", 65)
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{name: "no arguments", input: nil, want: "no arguments"},
		{name: "empty map", input: map[string]any{}, want: "no arguments"},
		{
			name:  "keys sorted",
			input: map[string]any{"b": float64(2), "a": float64(1)},
			want:  "a: 1\nb: 2",
		},
		{
			name:  "long string truncated",
			input: map[string]any{"text": long},
			want:  "text: " + strings.Repeat("x", 57) + "...",
		},
		{
			name:  "bool and nested value",
			input: map[string]any{"flag": true, "nested": map[string]any{"k": "v"}},
			want:  "flag: true\nnested: {...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatArgumentsPreview(tt.input); got != tt.want {
				t.Errorf("formatArgumentsPreview() = %q, want %q", got, tt.want)
			}
		})
	}
}
