package delegation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/provider"
)

func populatedResult() Result {
	return Result{
		AgentID:          "agent-123",
		Status:           StatusPartial,
		Output:           "test output",
		Summary:          "condensed findings",
		TurnCount:        5,
		TokenCount:       1000,
		StopReason:       "max_turns",
		ToolCallCount:    7,
		FollowUpCount:    2,
		SessionResumable: true,
	}
}

func marshalKeys(t *testing.T, v any) (map[string]json.RawMessage, []byte) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("unmarshal %T into key map: %v", v, err)
	}
	return keys, data
}

func assertKeys(t *testing.T, keys map[string]json.RawMessage, data []byte, want, absent []string) {
	t.Helper()
	for _, key := range want {
		if _, ok := keys[key]; !ok {
			t.Errorf("missing %q in %s", key, data)
		}
	}
	for _, key := range absent {
		if _, ok := keys[key]; ok {
			t.Errorf("unexpected %q in %s", key, data)
		}
	}
	if len(keys) != len(want) {
		t.Errorf("key count = %d, want %d: %s", len(keys), len(want), data)
	}
}

func TestResultJSONRoundTrip(t *testing.T) {
	t.Run("populated result keeps every wire field", func(t *testing.T) {
		result := populatedResult()
		keys, data := marshalKeys(t, result)
		assertKeys(t, keys, data, []string{
			"agent_id",
			"status",
			"output",
			"summary",
			"turn_count",
			"token_count",
			"stop_reason",
			"tool_call_count",
			"follow_up_count",
			"session_resumable",
		}, []string{"trace"})

		var decoded Result
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if !reflect.DeepEqual(decoded, result) {
			t.Errorf("round-tripped result = %+v, want %+v", decoded, result)
		}
	})

	t.Run("optional result fields are omitted when empty", func(t *testing.T) {
		keys, data := marshalKeys(t, Result{
			AgentID: "agent-123",
			Status:  StatusComplete,
			Output:  "test output",
		})
		assertKeys(t, keys, data, []string{
			"agent_id",
			"status",
			"output",
			"turn_count",
			"token_count",
			"tool_call_count",
		}, []string{
			"summary",
			"stop_reason",
			"follow_up_count",
			"session_resumable",
			"trace",
			"worktree_path",
			"worktree_branch",
			"warnings",
		})
	})

	t.Run("code result with worktree includes path, branch, and warnings", func(t *testing.T) {
		result := Result{
			AgentID:        "agent-code",
			Status:         StatusComplete,
			Output:         "test output",
			WorktreePath:   "/tmp/work/.steiner/worktrees/agent-code",
			WorktreeBranch: "delegate-agent-code",
			Warnings:       []string{"parent tree has changes"},
		}
		keys, data := marshalKeys(t, result)
		assertKeys(t, keys, data, []string{
			"agent_id",
			"status",
			"output",
			"turn_count",
			"token_count",
			"tool_call_count",
			"worktree_path",
			"worktree_branch",
			"warnings",
		}, []string{
			"summary",
			"stop_reason",
			"follow_up_count",
			"session_resumable",
			"trace",
		})

		var decoded Result
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if !reflect.DeepEqual(decoded, result) {
			t.Errorf("round-tripped result = %+v, want %+v", decoded, result)
		}
	})

	// touched_files was removed from the contract; the wire format must not
	// grow it back under any field name.
	t.Run("touched_files is absent from the wire format", func(t *testing.T) {
		for _, result := range []Result{{}, populatedResult()} {
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			if strings.Contains(string(data), "touched_files") {
				t.Errorf("touched_files reappeared in the wire format: %s", data)
			}
		}
	})

	// Per-tool-call traces and their counters are host-side diagnostics
	// (debug log, TUI, offline scripts), never provider-visible.
	t.Run("no trace key or trace path in the wire format", func(t *testing.T) {
		for _, result := range []Result{{}, populatedResult()} {
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(data, &keys); err != nil {
				t.Fatalf("unmarshal into key map: %v", err)
			}
			if _, ok := keys["trace"]; ok {
				t.Errorf("unexpected %q key in wire format: %s", "trace", data)
			}
			if strings.Contains(string(data), ".steiner/traces") {
				t.Errorf(".steiner/traces path leaked into wire format: %s", data)
			}
		}
	})
}

func TestSpecJSONRoundTrip(t *testing.T) {
	t.Run("populated spec keeps every wire field", func(t *testing.T) {
		spec := Spec{
			Task:         "test task",
			Context:      "test context",
			SystemPrompt: "test prompt",
			Images:       []provider.ImageBlock{{MediaType: "image/png", Data: "aGk="}},
			AgentType:    AgentTypeCode,
			Limits: Limits{
				MaxTurns:          10,
				OutputLimitTokens: 50000,
				Timeout:           60 * time.Second,
			},
			AgentID: "agent-123",
		}
		keys, data := marshalKeys(t, spec)
		assertKeys(t, keys, data, []string{
			"task",
			"context",
			"system_prompt",
			"images",
			"limits",
			"agent_id",
			"agent_type",
		}, nil)

		var decoded Spec
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal spec: %v", err)
		}
		if !reflect.DeepEqual(decoded, spec) {
			t.Errorf("round-tripped spec = %+v, want %+v", decoded, spec)
		}
	})

	t.Run("optional spec fields are omitted when empty", func(t *testing.T) {
		keys, data := marshalKeys(t, Spec{Task: "test task", AgentID: "agent-123"})
		assertKeys(t, keys, data, []string{
			"task",
			"limits",
			"agent_id",
			"agent_type",
		}, []string{
			"context",
			"system_prompt",
			"images",
		})
	})
}
