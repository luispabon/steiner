package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func claudeSubReadFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "claudesub", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return json.RawMessage(data)
}

// claudeSubStubDiscovery overrides the discovery locate and spawn seams so a
// test drives discovery with an in-memory connection.
func claudeSubStubDiscovery(t *testing.T, conn claudeSubConn) {
	t.Helper()
	oldLocate := claudeSubDiscoveryLocate
	oldSpawn := claudeSubDiscoverySpawn
	claudeSubDiscoveryLocate = func(context.Context) (claudeSubCLI, error) {
		return claudeSubCLI{Path: "/fake/claude", Version: "2.1.294"}, nil
	}
	claudeSubDiscoverySpawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() {
		claudeSubDiscoveryLocate = oldLocate
		claudeSubDiscoverySpawn = oldSpawn
	})
}

func claudeSubInitializeLine(requestID string, response json.RawMessage) []byte {
	line, _ := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		},
	})
	return line
}

func claudeSubErrorLine(requestID, msg string) []byte {
	line, _ := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      msg,
		},
	})
	return line
}

func TestClaudeSubDiscoveryArgs(t *testing.T) {
	want := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources=",
		"--no-session-persistence",
	}
	if got := claudeSubDiscoveryArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("claudeSubDiscoveryArgs() = %#v, want %#v", got, want)
	}
}

func TestClaudeSubDiscover(t *testing.T) {
	fixture := claudeSubReadFixture(t, "initialize_response.json")
	tests := []struct {
		name    string
		respond func(requestID string) []byte
		want    []ClaudeSubscriptionModel
		wantErr string
	}{
		{
			name:    "parses fixture, skips default and dedupes",
			respond: func(id string) []byte { return claudeSubInitializeLine(id, fixture) },
			want: []ClaudeSubscriptionModel{
				{ID: "claude-opus-5-5", DisplayName: "Opus 5.5", Description: "Best for everyday, complex tasks", SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"}},
				{ID: "claude-haiku-5-5", DisplayName: "Haiku 5.5", Description: "Fastest for quick answers", SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"}},
				{ID: "claude-haiku-4-5-20251001", DisplayName: "Haiku 4.5", Description: "Fastest for quick answers"},
			},
		},
		{
			name:    "missing models key",
			respond: func(id string) []byte { return claudeSubInitializeLine(id, json.RawMessage(`{}`)) },
			wantErr: "listed no models",
		},
		{
			name:    "malformed models",
			respond: func(id string) []byte { return claudeSubInitializeLine(id, json.RawMessage(`{"models":"nope"}`)) },
			wantErr: "parse claude CLI initialize response",
		},
		{
			name: "model without resolved id",
			respond: func(id string) []byte {
				return claudeSubInitializeLine(id, json.RawMessage(`{"models":[{"value":"x","resolvedModel":"","displayName":"X"}]}`))
			},
			wantErr: "without a resolved model",
		},
		{
			name: "only default entries",
			respond: func(id string) []byte {
				return claudeSubInitializeLine(id, json.RawMessage(`{"models":[{"value":"default","resolvedModel":"claude-opus-5-5"}]}`))
			},
			wantErr: "listed no usable models",
		},
		{
			name: "success without response body",
			respond: func(id string) []byte {
				line, _ := json.Marshal(map[string]any{
					"type":     "control_response",
					"response": map[string]any{"subtype": "success", "request_id": id},
				})
				return line
			},
			wantErr: "carried no model list",
		},
		{
			name:    "cli error response",
			respond: func(id string) []byte { return claudeSubErrorLine(id, "initialize failed") },
			wantErr: "initialize failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn := newClaudeSubFakeConn()
			conn.responder = func(line []byte) []claudeSubEvent {
				var envelope struct {
					RequestID string         `json:"request_id"`
					Request   map[string]any `json:"request"`
				}
				if err := json.Unmarshal(line, &envelope); err != nil {
					t.Errorf("decode sent line %q: %v", line, err)
					return nil
				}
				if envelope.Request["subtype"] != "initialize" {
					t.Errorf("request subtype = %v, want initialize", envelope.Request["subtype"])
				}
				return []claudeSubEvent{{Type: "control_response", Raw: tc.respond(envelope.RequestID)}}
			}
			claudeSubStubDiscovery(t, conn)

			models, err := DiscoverClaudeSubscriptionModels(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DiscoverClaudeSubscriptionModels() error = %v", err)
			}
			if !reflect.DeepEqual(models, tc.want) {
				t.Errorf("models = %#v, want %#v", models, tc.want)
			}
			if !conn.isClosed() {
				t.Error("discovery did not close the connection")
			}
		})
	}
}

func TestClaudeSubDiscoverLocateError(t *testing.T) {
	old := claudeSubDiscoveryLocate
	claudeSubDiscoveryLocate = func(context.Context) (claudeSubCLI, error) { return claudeSubCLI{}, errors.New("no claude") }
	t.Cleanup(func() { claudeSubDiscoveryLocate = old })

	_, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no claude") {
		t.Fatalf("error = %v, want no claude", err)
	}
}

func TestClaudeSubDiscoverSpawnError(t *testing.T) {
	oldLocate := claudeSubDiscoveryLocate
	oldSpawn := claudeSubDiscoverySpawn
	claudeSubDiscoveryLocate = func(context.Context) (claudeSubCLI, error) { return claudeSubCLI{Path: "/fake"}, nil }
	claudeSubDiscoverySpawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() {
		claudeSubDiscoveryLocate = oldLocate
		claudeSubDiscoverySpawn = oldSpawn
	})

	_, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start claude CLI for model discovery") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want wrapped spawn error", err)
	}
}

// TestClaudeSubDiscoverRefusesWithoutReserve proves discovery never spawns work
// when the caller leaves less than the reserved teardown window.
func TestClaudeSubDiscoverRefusesWithoutReserve(t *testing.T) {
	oldTimeout := claudeSubDiscoveryTimeout
	claudeSubDiscoveryTimeout = 100 * time.Millisecond
	t.Cleanup(func() { claudeSubDiscoveryTimeout = oldTimeout })

	spawned := false
	oldLocate := claudeSubDiscoveryLocate
	oldSpawn := claudeSubDiscoverySpawn
	claudeSubDiscoveryLocate = func(context.Context) (claudeSubCLI, error) { return claudeSubCLI{Path: "/fake"}, nil }
	claudeSubDiscoverySpawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		spawned = true
		return newClaudeSubFakeConn(), nil
	}
	t.Cleanup(func() {
		claudeSubDiscoveryLocate = oldLocate
		claudeSubDiscoverySpawn = oldSpawn
	})

	_, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reserved for cleanup") {
		t.Fatalf("error = %v, want a reserve refusal", err)
	}
	if spawned {
		t.Error("discovery spawned work without room for the teardown reserve")
	}
}

// TestClaudeSubDiscoverReleasesPendingOnExit checks that discovery does not wait
// out its timeout when the process exits before answering initialize.
func TestClaudeSubDiscoverReleasesPendingOnExit(t *testing.T) {
	oldTimeout := claudeSubDiscoveryWorkTimeout
	claudeSubDiscoveryWorkTimeout = 5 * time.Second
	t.Cleanup(func() { claudeSubDiscoveryWorkTimeout = oldTimeout })

	conn := newClaudeSubFakeConn()
	conn.err = errors.New("claude CLI process exited: signal: killed")
	// Simulate the process exiting as soon as the initialize line is written.
	conn.responder = func([]byte) []claudeSubEvent {
		go func() { _ = conn.Close(context.Background()) }()
		return nil
	}
	claudeSubStubDiscovery(t, conn)

	start := time.Now()
	_, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("error = %v, want the connection exit cause", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("discovery took %s, want prompt release on connection exit", elapsed)
	}
}

// TestClaudeSubDiscoverPropagatesCloseFailure checks that discovery treats a
// failed terminal cleanup as a failure and does not return models.
func TestClaudeSubDiscoverPropagatesCloseFailure(t *testing.T) {
	conn := newClaudeSubFakeConn()
	conn.closeErr = errors.New("close boom")
	conn.responder = func(line []byte) []claudeSubEvent {
		var envelope struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(line, &envelope)
		return []claudeSubEvent{{Type: "control_response", Raw: claudeSubInitializeLine(envelope.RequestID, json.RawMessage(`{"models":[{"value":"x","resolvedModel":"claude-test-1"}]}`))}}
	}
	claudeSubStubDiscovery(t, conn)

	models, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cleanup did not complete") || !strings.Contains(err.Error(), "close boom") {
		t.Fatalf("error = %v, want a propagated cleanup failure", err)
	}
	if models != nil {
		t.Errorf("models = %#v, want nil on cleanup failure", models)
	}
}

// TestClaudeSubDiscoverEnforcesWorkBudget fails if the work phase is not bounded
// by the outer budget minus the reserve: the work timeout is set far above the
// total, so only the outer deadline can bound it.
func TestClaudeSubDiscoverEnforcesWorkBudget(t *testing.T) {
	oldTotal := claudeSubDiscoveryTimeout
	oldWork := claudeSubDiscoveryWorkTimeout
	oldReserve := claudeSubDiscoveryReserve
	claudeSubDiscoveryTimeout = 200 * time.Millisecond
	claudeSubDiscoveryWorkTimeout = 10 * time.Second
	claudeSubDiscoveryReserve = 20 * time.Millisecond
	t.Cleanup(func() {
		claudeSubDiscoveryTimeout = oldTotal
		claudeSubDiscoveryWorkTimeout = oldWork
		claudeSubDiscoveryReserve = oldReserve
	})

	conn := newClaudeSubFakeConn()
	claudeSubStubDiscovery(t, conn)

	start := time.Now()
	_, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("discovery took %s; the %s work budget was not enforced", elapsed, claudeSubDiscoveryTimeout)
	}
}

// TestClaudeSubDiscoverTeardownBounded proves discovery joins the router and the
// connection's terminal cleanup before returning, within the reserved budget:
// no router, writer or cleanup task is active once it returns.
func TestClaudeSubDiscoverTeardownBounded(t *testing.T) {
	oldTotal := claudeSubDiscoveryTimeout
	oldWork := claudeSubDiscoveryWorkTimeout
	claudeSubDiscoveryTimeout = 20 * time.Second
	claudeSubDiscoveryWorkTimeout = 150 * time.Millisecond
	t.Cleanup(func() {
		claudeSubDiscoveryTimeout = oldTotal
		claudeSubDiscoveryWorkTimeout = oldWork
	})

	conn := newClaudeSubFakeConn()
	// Production-like Close: bounded, and it closes Events.
	conn.closeDelay = 50 * time.Millisecond
	// No responder: initialize is never answered, so the work phase hits its
	// deadline and cleanup then runs within the reserved budget.
	claudeSubStubDiscovery(t, conn)

	start := time.Now()
	_, err := DiscoverClaudeSubscriptionModels(context.Background())
	if err == nil {
		t.Fatal("want a work-deadline error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("discovery took %s, want the work deadline plus bounded teardown", elapsed)
	}

	// Discovery joins the router and Close before returning, so these must
	// already be settled now, not eventually.
	if !conn.isClosed() {
		t.Error("cleanup was still active when discovery returned")
	}
	select {
	case <-conn.drained:
	default:
		t.Error("router was still active when discovery returned")
	}
}

func TestClaudeSubBoundedBuffer(t *testing.T) {
	b := newClaudeSubBoundedBuffer(4)
	if _, err := b.Write([]byte("ab")); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if _, err := b.Write([]byte("cdef")); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got := string(b.bytes()); got != "abcd" {
		t.Errorf("bytes() = %q, want abcd", got)
	}
	if !b.overflowed() {
		t.Error("overflowed() = false, want true")
	}

	within := newClaudeSubBoundedBuffer(4)
	_, _ = within.Write([]byte("ab"))
	if within.overflowed() {
		t.Error("overflowed() = true for in-bounds output")
	}
}
