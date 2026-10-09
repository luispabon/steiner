package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClaudeSubControlRequestShapes(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *claudeSubControl) (json.RawMessage, error)
		want map[string]any
	}{
		{
			name: "interrupt",
			call: func(ctx context.Context, c *claudeSubControl) (json.RawMessage, error) { return c.interrupt(ctx) },
			want: map[string]any{"subtype": "interrupt"},
		},
		{
			name: "get usage",
			call: func(ctx context.Context, c *claudeSubControl) (json.RawMessage, error) { return c.getUsage(ctx) },
			want: map[string]any{"subtype": "get_usage", "skip_behaviors": true},
		},
		{
			name: "set model",
			call: func(ctx context.Context, c *claudeSubControl) (json.RawMessage, error) {
				return c.setModel(ctx, "claude-haiku-5-5")
			},
			want: map[string]any{"subtype": "set_model", "model": "claude-haiku-5-5"},
		},
		{
			name: "set effort",
			call: func(ctx context.Context, c *claudeSubControl) (json.RawMessage, error) {
				return c.setEffort(ctx, "high")
			},
			want: map[string]any{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": "high"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newClaudeSubFakeConn()
			control := newClaudeSubTestControl(t, fake)
			done := make(chan error, 1)
			go func() {
				_, err := tc.call(context.Background(), control)
				done <- err
			}()

			line := fake.waitSent(t, 1)[0]
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				t.Fatalf("decode sent line %q: %v", line, err)
			}
			if envelope.Type != "control_request" {
				t.Errorf("sent type = %q, want control_request", envelope.Type)
			}
			id, request := claudeSubSentControlRequest(t, line)
			if !strings.HasPrefix(id, "steiner-") {
				t.Errorf("request id = %q, want steiner- prefix", id)
			}
			if !reflect.DeepEqual(request, tc.want) {
				t.Errorf("request = %#v, want %#v", request, tc.want)
			}

			control.dispatch(claudeSubSuccessEvent(id, nil))
			if err := <-done; err != nil {
				t.Fatalf("control call error = %v, want nil", err)
			}
		})
	}
}

func TestClaudeSubControlRoundTrip(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	type result struct {
		raw json.RawMessage
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := control.request(context.Background(), "get_usage", nil)
		done <- result{raw, err}
	}()

	id, _ := claudeSubSentControlRequest(t, fake.waitSent(t, 1)[0])
	control.dispatch(claudeSubSuccessEvent(id, json.RawMessage(`{"subscription_type":"pro"}`)))

	r := <-done
	if r.err != nil {
		t.Fatalf("request error = %v, want nil", r.err)
	}
	if got := string(r.raw); got != `{"subscription_type":"pro"}` {
		t.Errorf("response = %s, want %s", got, `{"subscription_type":"pro"}`)
	}
}

func TestClaudeSubControlErrorResponse(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	done := make(chan error, 1)
	go func() {
		_, err := control.setModel(context.Background(), "claude-haiku-5-5")
		done <- err
	}()

	id, _ := claudeSubSentControlRequest(t, fake.waitSent(t, 1)[0])
	control.dispatch(claudeSubErrorEvent(id, "model not found"))

	err := <-done
	if err == nil || !strings.Contains(err.Error(), "claude CLI set_model: model not found") {
		t.Fatalf("error = %v, want claude CLI set_model: model not found", err)
	}
}

func TestClaudeSubControlUnknownResponseSubtype(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	done := make(chan error, 1)
	go func() {
		_, err := control.getUsage(context.Background())
		done <- err
	}()

	id, _ := claudeSubSentControlRequest(t, fake.waitSent(t, 1)[0])
	control.dispatch(claudeSubSubtypeEvent(id, "partial"))

	err := <-done
	if err == nil || !strings.Contains(err.Error(), "unknown control response subtype") {
		t.Fatalf("error = %v, want unknown control response subtype", err)
	}
}

func TestClaudeSubControlTimeout(t *testing.T) {
	old := claudeSubControlTimeout
	claudeSubControlTimeout = 30 * time.Millisecond
	defer func() { claudeSubControlTimeout = old }()

	control := newClaudeSubTestControl(t, newClaudeSubFakeConn())
	_, err := control.request(context.Background(), "interrupt", nil)
	if err == nil || !strings.Contains(err.Error(), "no response within") {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestClaudeSubControlContextCancel(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := control.request(ctx, "interrupt", nil)
		done <- err
	}()

	fake.waitSent(t, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClaudeSubControlReleasedOnConnectionExit(t *testing.T) {
	fake := newClaudeSubFakeConn()
	fake.err = errors.New("claude CLI process exited: signal: killed")
	control := newClaudeSubTestControl(t, fake)

	done := make(chan error, 1)
	go func() {
		_, err := control.request(context.Background(), "get_usage", nil)
		done <- err
	}()
	fake.waitSent(t, 1)

	// The router watches Events and releases pending requests when it closes.
	routerDone := make(chan struct{})
	go func() {
		claudeSubRoute(fake.Events(), nil, control, nil)
		close(routerDone)
	}()
	_ = fake.Close(context.Background())

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "signal: killed") {
			t.Fatalf("error = %v, want the connection exit cause", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request not released when Events closed (would wait for the 30s timeout)")
	}
	select {
	case <-routerDone:
	case <-time.After(time.Second):
		t.Fatal("router did not stop after Events closed")
	}
}

func TestClaudeSubControlSendFailureTerminates(t *testing.T) {
	fake := newClaudeSubFakeConn()
	fake.sendErr = errors.New("pipe closed")
	control := newClaudeSubTestControl(t, fake)

	done := make(chan error, 1)
	go func() {
		_, err := control.request(context.Background(), "get_usage", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "pipe closed") {
			t.Fatalf("error = %v, want the send failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request not failed when the control send failed")
	}

	// Future requests are rejected, and the terminal state came from the send
	// failure, not from Events closing.
	if _, err := control.request(context.Background(), "get_usage", nil); err == nil {
		t.Error("request after send failure = nil, want an error")
	}
	if fake.isClosed() {
		t.Error("Events closed; the send-failure path was not what terminated the control")
	}
}

// TestClaudeSubControlSendFailureShutsDownTransport proves a control send
// failure hands off to the transport's non-blocking shutdown exactly once, so a
// control channel that can no longer write cannot leave a live CLI.
func TestClaudeSubControlSendFailureShutsDownTransport(t *testing.T) {
	fake := newClaudeSubFakeConn()
	fake.sendErr = errors.New("pipe closed")
	control := newClaudeSubTestControl(t, fake)

	done := make(chan error, 1)
	go func() {
		_, err := control.request(context.Background(), "get_usage", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "pipe closed") {
			t.Fatalf("error = %v, want the send failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request not failed when the control send failed")
	}

	// The handoff runs after the pending request is failed, so join the writer
	// before asserting it happened.
	select {
	case <-control.writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("control writer did not stop after the send failure")
	}
	shutdowns := fake.shutdownRequests()
	if len(shutdowns) != 1 {
		t.Fatalf("transport shutdown handoffs = %d, want exactly 1", len(shutdowns))
	}
	if shutdowns[0] == nil || !strings.Contains(shutdowns[0].Error(), "pipe closed") {
		t.Fatalf("shutdown cause = %v, want the send failure", shutdowns[0])
	}
}

func TestClaudeSubControlUnknownRequestID(t *testing.T) {
	control := newClaudeSubTestControl(t, newClaudeSubFakeConn())
	if !control.dispatch(claudeSubSuccessEvent("stale", nil)) {
		t.Error("success response for unknown id was not consumed")
	}
	if !control.dispatch(claudeSubErrorEvent("stale", "late")) {
		t.Error("error response for unknown id was not consumed")
	}
}

func TestClaudeSubControlRejectsCLIRequest(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	raw := json.RawMessage(`{"type":"control_request","request_id":"cli-7","request":{"subtype":"mcp_message"}}`)
	if !control.dispatch(claudeSubEvent{Type: "control_request", Raw: raw}) {
		t.Fatal("CLI control_request was not consumed")
	}

	var envelope struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(fake.waitSent(t, 1)[0]), &envelope); err != nil {
		t.Fatalf("decode rejection: %v", err)
	}
	if envelope.Type != "control_response" {
		t.Errorf("type = %q, want control_response", envelope.Type)
	}
	if envelope.Response.Subtype != "error" || envelope.Response.RequestID != "cli-7" || envelope.Response.Error != "unsupported" {
		t.Errorf("response = %+v, want error/cli-7/unsupported", envelope.Response)
	}
}

func TestClaudeSubControlIgnoresNonControlEvents(t *testing.T) {
	control := newClaudeSubTestControl(t, newClaudeSubFakeConn())
	for _, ev := range []claudeSubEvent{{Type: "assistant"}, {Type: "result"}, {Type: "system"}} {
		if control.dispatch(ev) {
			t.Errorf("dispatch(%q) = true, want false", ev.Type)
		}
	}
}

func TestClaudeSubWriteUser(t *testing.T) {
	fake := newClaudeSubFakeConn()
	blocks := []map[string]any{{"type": "text", "text": "hi"}}
	if err := writeClaudeSubUser(fake, blocks); err != nil {
		t.Fatalf("writeClaudeSubUser() error = %v", err)
	}
	line := fake.waitSent(t, 1)[0]
	if !strings.Contains(line, `"parent_tool_use_id":null`) {
		t.Errorf("line = %s, want parent_tool_use_id null", line)
	}
	var got struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Message   struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"message"`
		ParentToolUseID any `json:"parent_tool_use_id"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("decode user line: %v", err)
	}
	if got.Type != "user" || got.SessionID != "" || got.Message.Role != "user" {
		t.Errorf("envelope = %+v, want type user, empty session, role user", got)
	}
	if got.ParentToolUseID != nil {
		t.Errorf("parent_tool_use_id = %v, want null", got.ParentToolUseID)
	}
	if !reflect.DeepEqual(got.Message.Content, blocks) {
		t.Errorf("content = %#v, want %#v", got.Message.Content, blocks)
	}
}

func TestClaudeSubWriteUserSendError(t *testing.T) {
	fake := newClaudeSubFakeConn()
	fake.sendErr = errors.New("pipe closed")
	if err := writeClaudeSubUser(fake, nil); err == nil || !strings.Contains(err.Error(), "pipe closed") {
		t.Fatalf("error = %v, want pipe closed", err)
	}
}

func TestClaudeSubQueueFIFO(t *testing.T) {
	q := newClaudeSubQueue()
	q.push(claudeSubEvent{Type: "a"})
	q.push(claudeSubEvent{Type: "b"})
	for i, want := range []string{"a", "b"} {
		ev, ok := q.pop(context.Background())
		if !ok || ev.Type != want {
			t.Errorf("pop %d = %q, %v; want %q, true", i, ev.Type, ok, want)
		}
	}
}

func TestClaudeSubQueueBlocksUntilPush(t *testing.T) {
	q := newClaudeSubQueue()
	got := make(chan claudeSubEvent, 1)
	go func() {
		if ev, ok := q.pop(context.Background()); ok {
			got <- ev
		}
	}()
	time.Sleep(20 * time.Millisecond)
	q.push(claudeSubEvent{Type: "late"})
	select {
	case ev := <-got:
		if ev.Type != "late" {
			t.Errorf("pop = %q, want late", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("pop did not wake on push")
	}
}

func TestClaudeSubQueueCloseWakesPop(t *testing.T) {
	q := newClaudeSubQueue()
	done := make(chan bool, 1)
	go func() {
		_, ok := q.pop(context.Background())
		done <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	q.close()
	select {
	case ok := <-done:
		if ok {
			t.Error("pop reported an event after close")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake pop")
	}
}

func TestClaudeSubQueueCancelWakesPop(t *testing.T) {
	q := newClaudeSubQueue()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := q.pop(ctx)
		done <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Error("pop reported an event after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not wake pop")
	}
}

func TestClaudeSubQueueDrainAndPushAfterClose(t *testing.T) {
	q := newClaudeSubQueue()
	q.push(claudeSubEvent{Type: "a"})
	q.push(claudeSubEvent{Type: "b"})
	q.close()
	q.push(claudeSubEvent{Type: "dropped"})

	if got := q.drain(); len(got) != 2 || got[0].Type != "a" || got[1].Type != "b" {
		t.Errorf("drain() = %#v, want a then b", got)
	}
	if got := q.drain(); got != nil {
		t.Errorf("second drain() = %#v, want nil", got)
	}
	if _, ok := q.pop(context.Background()); ok {
		t.Error("pop after close/drain reported an event")
	}
}

func TestClaudeSubRoute(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	q := newClaudeSubQueue()

	fake.push(claudeSubEvent{Type: "assistant"})
	fake.push(claudeSubSuccessEvent("unknown", nil)) // consumed, not queued
	fake.push(claudeSubEvent{Type: "result"})

	done := make(chan struct{})
	go func() {
		claudeSubRoute(fake.Events(), nil, control, q)
		close(done)
	}()

	var got []string
	for len(got) < 2 {
		ev, ok := q.pop(context.Background())
		if !ok {
			t.Fatalf("queue closed after %d events", len(got))
		}
		got = append(got, ev.Type)
	}
	if !reflect.DeepEqual(got, []string{"assistant", "result"}) {
		t.Errorf("queued = %#v, want [assistant result]", got)
	}

	_ = fake.Close(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router did not stop after events closed")
	}
}

func TestClaudeSubRouteNilQueue(t *testing.T) {
	fake := newClaudeSubFakeConn()
	control := newClaudeSubTestControl(t, fake)
	done := make(chan struct{})
	go func() {
		claudeSubRoute(fake.Events(), nil, control, nil)
		close(done)
	}()
	fake.push(claudeSubEvent{Type: "assistant"})
	_ = fake.Close(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router did not stop with a nil queue")
	}
}

// TestClaudeSubRouteNotStarvedByBlockedSend proves an inbound CLI control_request
// whose unsupported-response send is stalled does not wedge the router: other
// events are still queued and a pending request's response is still delivered.
func TestClaudeSubRouteNotStarvedByBlockedSend(t *testing.T) {
	fake := newClaudeSubFakeConn()
	block := make(chan struct{})
	fake.sendBlock = block
	control := newClaudeSubTestControl(t, fake)
	q := newClaudeSubQueue()

	done := make(chan struct{})
	go func() {
		claudeSubRoute(fake.Events(), nil, control, q)
		close(done)
	}()

	inbound, _ := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": "cli-1",
		"request":    map[string]any{"subtype": "hook_callback"},
	})
	fake.push(claudeSubEvent{Type: "control_request", Raw: inbound})
	fake.push(claudeSubEvent{Type: "assistant"})

	// Non-control events keep flowing while the send is stalled.
	popCtx, popCancel := context.WithTimeout(context.Background(), time.Second)
	defer popCancel()
	ev, ok := q.pop(popCtx)
	if !ok || ev.Type != "assistant" {
		t.Fatalf("router starved by blocked send: pop = %v, %v", ev.Type, ok)
	}

	// A pending control request is still answered while the send is stalled.
	done2 := make(chan error, 1)
	go func() {
		_, err := control.request(context.Background(), "get_usage", nil)
		done2 <- err
	}()
	id := claudeSubWaitPendingID(t, control)
	fake.push(claudeSubSuccessEvent(id, nil))
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("pending request error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("control response starved by blocked send")
	}

	// Unblocking releases the queued unsupported response.
	close(block)
	lines := fake.waitSent(t, 2)
	var sentUnsupported bool
	for _, line := range lines {
		if strings.Contains(line, `"error":"unsupported"`) && strings.Contains(line, `"request_id":"cli-1"`) {
			sentUnsupported = true
		}
	}
	if !sentUnsupported {
		t.Errorf("unsupported response was never sent: %v", lines)
	}

	_ = fake.Close(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router did not stop after events closed")
	}
}

// claudeSubWaitPendingID waits for exactly one pending control request and
// returns its id.
func claudeSubWaitPendingID(t *testing.T, control *claudeSubControl) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		control.mu.Lock()
		ids := make([]string, 0, len(control.pending))
		for id := range control.pending {
			ids = append(ids, id)
		}
		control.mu.Unlock()
		if len(ids) == 1 {
			return ids[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending requests = %v, want exactly one", ids)
		}
		time.Sleep(time.Millisecond)
	}
}
