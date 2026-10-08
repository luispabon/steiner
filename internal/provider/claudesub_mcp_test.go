package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const claudeSubMCPTestTimeout = 5 * time.Second

// claudeSubMCPBearerTransport adds the host bearer token to every loopback
// request. A zero token adds no header, modelling an unauthenticated client.
type claudeSubMCPBearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t claudeSubMCPBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r)
}

func claudeSubMCPNewHost(t *testing.T) *claudeSubMCPHost {
	t.Helper()
	h, err := newClaudeSubMCPHost()
	if err != nil {
		t.Fatalf("newClaudeSubMCPHost: %v", err)
	}
	// Keep the shutdown-deadline fallback fast in tests; production defaults to
	// claudeSubShutdownGrace.
	h.shutdownGrace = 100 * time.Millisecond
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func claudeSubMCPConnect(t *testing.T, h *claudeSubMCPHost, token string, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	if opts == nil {
		opts = &mcp.ClientOptions{}
	}
	httpClient := &http.Client{Transport: claudeSubMCPBearerTransport{token: token, base: http.DefaultTransport}}
	tr := &mcp.StreamableClientTransport{Endpoint: h.url, HTTPClient: httpClient}
	client := mcp.NewClient(&mcp.Implementation{Name: "claudesub-test", Version: "1"}, opts)
	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	cs, err := client.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatalf("connect mcp client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// claudeSubMCPWaitWaiter blocks until the host has a blocked call registered
// for id, so a test can resolve only after the CLI's tools/call arrived.
func claudeSubMCPWaitWaiter(t *testing.T, h *claudeSubMCPHost, id string) {
	t.Helper()
	deadline := time.Now().Add(claudeSubMCPTestTimeout)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		call := h.calls[id]
		waiting := call != nil && call.waiter != nil
		h.mu.Unlock()
		if waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiter for %q never registered", id)
}

// claudeSubMCPWaitRetired blocks until the host has retired (removed) the call
// for id after it was cancelled or consumed.
func claudeSubMCPWaitRetired(t *testing.T, h *claudeSubMCPHost, id string) {
	t.Helper()
	deadline := time.Now().Add(claudeSubMCPTestTimeout)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		_, live := h.calls[id]
		h.mu.Unlock()
		if !live {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%q was never retired", id)
}

func claudeSubMCPText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func claudeSubMCPObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties}
}

func TestClaudeSubMCPRejectsUnauthenticatedClients(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	const body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

	resp, err := http.Post(h.url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post without token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req, err := http.NewRequest(http.MethodPost, h.url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer not-the-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post with wrong token: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("status with wrong token = %d, want %d", resp2.StatusCode, http.StatusUnauthorized)
	}
}

func TestClaudeSubMCPListsToolsWithExactSchema(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	specs := []ToolSpec{
		{Type: "function", Function: ToolFunctionSpec{
			Name:        "read",
			Description: "Read a file",
			Parameters:  claudeSubMCPObjectSchema(map[string]any{"path": map[string]any{"type": "string"}}),
		}},
		{Type: "function", Function: ToolFunctionSpec{Name: "bash"}},
	}
	h.setTools(specs)
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(res.Tools))
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}

	read, ok := byName["read"]
	if !ok {
		t.Fatalf("read tool missing; got %v", byName)
	}
	if read.Description != "Read a file" {
		t.Errorf("read description = %q, want %q", read.Description, "Read a file")
	}
	if !reflect.DeepEqual(read.InputSchema, specs[0].Function.Parameters) {
		t.Errorf("read inputSchema = %#v, want %#v", read.InputSchema, specs[0].Function.Parameters)
	}
	if read.Meta["anthropic/alwaysLoad"] != true {
		t.Errorf("read _meta[anthropic/alwaysLoad] = %#v, want true", read.Meta["anthropic/alwaysLoad"])
	}

	bash, ok := byName["bash"]
	if !ok {
		t.Fatalf("bash tool missing; got %v", byName)
	}
	if !reflect.DeepEqual(bash.InputSchema, map[string]any{"type": "object"}) {
		t.Errorf("bash inputSchema = %#v, want {\"type\":\"object\"}", bash.InputSchema)
	}
	if bash.Meta["anthropic/alwaysLoad"] != true {
		t.Errorf("bash _meta[anthropic/alwaysLoad] = %#v, want true", bash.Meta["anthropic/alwaysLoad"])
	}
}

func TestClaudeSubMCPResolveBeforeCallReturnsImmediately(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	imageData := base64.StdEncoding.EncodeToString([]byte("abc"))
	h.resolve(h.beginCall("toolu_1"), claudeSubToolResult{
		Text:   "hello",
		Images: []ImageBlock{{MediaType: "image/png", Data: imageData}, {MediaType: "image/png", Data: "not base64"}},
	})

	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "read",
		Meta: mcp.Meta{"claudecode/toolUseId": "toolu_1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Error("CallTool reported IsError for a stored result")
	}
	if got := claudeSubMCPText(res); got != "hello" {
		t.Errorf("text = %q, want %q", got, "hello")
	}
	var images []*mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			images = append(images, ic)
		}
	}
	if len(images) != 1 {
		t.Fatalf("images = %d, want 1 (undecodable image skipped)", len(images))
	}
	if string(images[0].Data) != "abc" {
		t.Errorf("image data = %q, want %q", images[0].Data, "abc")
	}
	if images[0].MIMEType != "image/png" {
		t.Errorf("image mime = %q, want %q", images[0].MIMEType, "image/png")
	}

	h.mu.Lock()
	_, live := h.calls["toolu_1"]
	h.mu.Unlock()
	if live {
		t.Error("consumed call was not retired")
	}
}

func TestClaudeSubMCPResolveAfterCallBlocksUntilResult(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	results := make(chan *mcp.CallToolResult, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_2"},
		})
		if err != nil {
			errs <- err
			return
		}
		results <- res
	}()

	claudeSubMCPWaitWaiter(t, h, "toolu_2")
	select {
	case res := <-results:
		t.Fatalf("CallTool returned %q before resolve", claudeSubMCPText(res))
	case err := <-errs:
		t.Fatalf("CallTool failed before resolve: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	h.resolve(h.beginCall("toolu_2"), claudeSubToolResult{Text: "world"})
	select {
	case res := <-results:
		if got := claudeSubMCPText(res); got != "world" {
			t.Errorf("text = %q, want %q", got, "world")
		}
	case err := <-errs:
		t.Fatalf("CallTool after resolve: %v", err)
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("CallTool did not return after resolve")
	}
}

type claudeSubMCPProgressCollector struct {
	ch chan *mcp.ProgressNotificationParams
}

func (c *claudeSubMCPProgressCollector) handler(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
	select {
	case c.ch <- req.Params:
	default:
	}
}

func TestClaudeSubMCPEmitsProgressHeartbeatsWhileWaiting(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.heartbeat = 25 * time.Millisecond
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})

	collector := &claudeSubMCPProgressCollector{ch: make(chan *mcp.ProgressNotificationParams, 4)}
	cs := claudeSubMCPConnect(t, h, h.token, &mcp.ClientOptions{ProgressNotificationHandler: collector.handler})

	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	results := make(chan *mcp.CallToolResult, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_3", "progressToken": "tok-3"},
		})
		if err != nil {
			errs <- err
			return
		}
		results <- res
	}()

	select {
	case params := <-collector.ch:
		if params.Message != "steiner is running the tool" {
			t.Errorf("progress message = %q, want %q", params.Message, "steiner is running the tool")
		}
		if params.Progress <= 0 {
			t.Errorf("progress = %v, want > 0", params.Progress)
		}
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("no progress notification while the call waited")
	}

	h.resolve(h.beginCall("toolu_3"), claudeSubToolResult{Text: "done"})
	select {
	case res := <-results:
		if got := claudeSubMCPText(res); got != "done" {
			t.Errorf("text = %q, want %q", got, "done")
		}
	case err := <-errs:
		t.Fatalf("CallTool: %v", err)
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("CallTool did not return after resolve")
	}
}

func TestClaudeSubMCPCancelAllReleasesBlockedCall(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	results := make(chan *mcp.CallToolResult, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_4"},
		})
		if err != nil {
			errs <- err
			return
		}
		results <- res
	}()

	claudeSubMCPWaitWaiter(t, h, "toolu_4")
	h.cancelAll()

	select {
	case res := <-results:
		if !res.IsError {
			t.Error("cancelled call result IsError = false, want true")
		}
		if got := claudeSubMCPText(res); got != "tool call cancelled" {
			t.Errorf("cancelled call text = %q, want %q", got, "tool call cancelled")
		}
	case err := <-errs:
		t.Fatalf("cancelled CallTool error: %v", err)
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("cancelAll did not release the blocked call")
	}
}

func TestClaudeSubMCPCloseReleasesBlockedCall(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	results := make(chan *mcp.CallToolResult, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_5"},
		})
		if err != nil {
			errs <- err
			return
		}
		results <- res
	}()

	claudeSubMCPWaitWaiter(t, h, "toolu_5")
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case res := <-results:
		if !res.IsError {
			t.Error("closed call result IsError = false, want true")
		}
	case err := <-errs:
		t.Fatalf("closed CallTool error: %v", err)
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("Close did not release the blocked call")
	}
}

func TestClaudeSubMCPToolNameRoundTrip(t *testing.T) {
	if got := claudeSubToolName("read"); got != "read" {
		t.Errorf("claudeSubToolName(read) = %q, want %q", got, "read")
	}

	long := strings.Repeat("a", 100)
	short := claudeSubToolName(long)
	if short == long {
		t.Fatalf("long name was not shortened")
	}
	if len(claudeSubToolPrefix)+len(short) > 64 {
		t.Errorf("published name length = %d, want <= 64", len(claudeSubToolPrefix)+len(short))
	}

	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{
		{Function: ToolFunctionSpec{Name: long, Parameters: claudeSubMCPObjectSchema(nil)}},
		{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}},
	})
	if got := h.steinerToolName(claudeSubToolPrefix + short); got != long {
		t.Errorf("steinerToolName(shortened) = %q, want %q", got, long)
	}
	if got := h.steinerToolName(claudeSubToolPrefix + "read"); got != "read" {
		t.Errorf("steinerToolName(read) = %q, want %q", got, "read")
	}
}

func TestClaudeSubMCPWriteConfig(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}

	path, err := h.writeConfig(dir)
	if err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	if path != filepath.Join(dir, "mcp.json") {
		t.Errorf("path = %q, want %q", path, filepath.Join(dir, "mcp.json"))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0o600", info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	want := claudeSubMCPWantConfig(h)
	if string(data) != want {
		t.Errorf("config JSON = %s, want %s", data, want)
	}
}

func claudeSubMCPWantConfig(h *claudeSubMCPHost) string {
	return fmt.Sprintf(`{"mcpServers":{"steiner":{"type":"http","url":%q,"headers":{"Authorization":"Bearer %s"}}}}`, h.url, h.token)
}

func TestClaudeSubMCPRejectsDuplicateToolUseIDKeepsOriginal(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	first := make(chan *mcp.CallToolResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_dup"},
		})
		if err != nil {
			firstErr <- err
			return
		}
		first <- res
	}()
	claudeSubMCPWaitWaiter(t, h, "toolu_dup")

	dupCtx, dupCancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer dupCancel()
	dup, err := cs.CallTool(dupCtx, &mcp.CallToolParams{
		Name: "read",
		Meta: mcp.Meta{"claudecode/toolUseId": "toolu_dup"},
	})
	if err != nil {
		t.Fatalf("duplicate CallTool: %v", err)
	}
	if !dup.IsError {
		t.Error("duplicate result IsError = false, want true")
	}
	if got := claudeSubMCPText(dup); got != "duplicate tool use id" {
		t.Errorf("duplicate text = %q, want %q", got, "duplicate tool use id")
	}

	h.mu.Lock()
	call := h.calls["toolu_dup"]
	live := call != nil && call.owner
	h.mu.Unlock()
	if !live {
		t.Fatal("original call was clobbered by the duplicate call")
	}

	h.resolve(h.beginCall("toolu_dup"), claudeSubToolResult{Text: "original"})
	select {
	case res := <-first:
		if got := claudeSubMCPText(res); got != "original" {
			t.Errorf("original text = %q, want %q", got, "original")
		}
	case err := <-firstErr:
		t.Fatalf("original CallTool: %v", err)
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("resolve did not release the original call after a duplicate")
	}
}

func TestClaudeSubMCPDuplicateThenCancelAllReleasesOriginal(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	first := make(chan *mcp.CallToolResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_dup_cancel"},
		})
		if err != nil {
			firstErr <- err
			return
		}
		first <- res
	}()
	claudeSubMCPWaitWaiter(t, h, "toolu_dup_cancel")

	dupCtx, dupCancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer dupCancel()
	dup, err := cs.CallTool(dupCtx, &mcp.CallToolParams{
		Name: "read",
		Meta: mcp.Meta{"claudecode/toolUseId": "toolu_dup_cancel"},
	})
	if err != nil {
		t.Fatalf("duplicate CallTool: %v", err)
	}
	if !dup.IsError {
		t.Error("duplicate result IsError = false, want true")
	}

	h.cancelAll()
	select {
	case res := <-first:
		if !res.IsError {
			t.Error("cancelled original IsError = false, want true")
		}
		if got := claudeSubMCPText(res); got != "tool call cancelled" {
			t.Errorf("cancelled original text = %q, want %q", got, "tool call cancelled")
		}
	case err := <-firstErr:
		t.Fatalf("original CallTool: %v", err)
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("cancelAll did not release the original call after a duplicate")
	}
}

func TestClaudeSubMCPLateResolveAfterCancelIsDiscarded(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	call := h.beginCall("toolu_cancel")
	ctx, cancel := context.WithTimeout(context.Background(), claudeSubMCPTestTimeout)
	defer cancel()
	firstErr := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_cancel"},
		})
		firstErr <- err
	}()
	claudeSubMCPWaitWaiter(t, h, "toolu_cancel")
	cancel()
	select {
	case <-firstErr:
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("cancelled call did not return")
	}
	claudeSubMCPWaitRetired(t, h, "toolu_cancel")

	// A late result on the cancelled handle must not be stored or revive the id.
	h.resolve(call, claudeSubToolResult{Text: "stale"})
	h.mu.Lock()
	_, live := h.calls["toolu_cancel"]
	h.mu.Unlock()
	if live {
		t.Fatal("late resolve revived a cancelled id")
	}
}

// TestClaudeSubMCPDuplicateCannotStealResolvedResult pins the exact window the
// old waiter/result maps left open: resolve has delivered the result and
// released the waiter, but the original handler has not consumed it yet. It
// drives the same openCall/claimResult transitions the real handler uses, so the
// sequencing is deterministic rather than scheduler-dependent.
func TestClaudeSubMCPDuplicateCannotStealResolvedResult(t *testing.T) {
	h := claudeSubMCPNewHost(t)

	call := h.beginCall("toolu_win")
	original, waiter, state := h.openCall("toolu_win")
	if state != claudeSubCallAcquired || original != call || waiter == nil {
		t.Fatalf("original openCall = (%v, %v, %v), want the registered call and a waiter", original, waiter, state)
	}

	// resolve stores the result and releases the waiter, but the original has
	// not called claimResult yet.
	h.resolve(call, claudeSubToolResult{Text: "original"})

	dup, dupWaiter, dupState := h.openCall("toolu_win")
	if dupState != claudeSubCallDuplicate {
		t.Fatalf("duplicate openCall state = %v, want duplicate", dupState)
	}
	if dup != nil || dupWaiter != nil {
		t.Fatalf("duplicate openCall returned (%v, %v), want nils", dup, dupWaiter)
	}

	got, ok := h.claimResult(original)
	if !ok {
		t.Fatal("original lost its resolved result to the duplicate")
	}
	if got.Text != "original" {
		t.Errorf("original text = %q, want %q", got.Text, "original")
	}
}

// TestClaudeSubMCPBoundedStateSurvivesCapacityPressure proves the cancel/late
// result protection does not depend on a fixed-capacity terminal set: after far
// more cancellations than the old tombstone bound, a cancelled id still cannot
// receive a late result, the live call set stays empty, and reusing the raw id
// allocates a fresh call the retired handle cannot touch.
func TestClaudeSubMCPBoundedStateSurvivesCapacityPressure(t *testing.T) {
	h := claudeSubMCPNewHost(t)

	cancelled := h.beginCall("toolu_reuse")
	h.cancelCall(cancelled)

	for i := 0; i < 2048; i++ {
		h.cancelCall(h.beginCall(fmt.Sprintf("toolu_flood_%d", i)))
	}
	h.mu.Lock()
	live := len(h.calls)
	h.mu.Unlock()
	if live != 0 {
		t.Fatalf("live calls after cancellations = %d, want 0 (state is not bounded)", live)
	}

	// A late result on the retired handle is discarded.
	h.resolve(cancelled, claudeSubToolResult{Text: "stale"})
	h.mu.Lock()
	_, revived := h.calls["toolu_reuse"]
	h.mu.Unlock()
	if revived {
		t.Fatal("late resolve revived a cancelled id")
	}

	// Reusing the raw id allocates a fresh call; the retired handle cannot reach
	// it even though the raw id string is the same.
	reused := h.beginCall("toolu_reuse")
	if reused == cancelled {
		t.Fatal("reused id returned the retired call")
	}
	got, waiter, state := h.openCall("toolu_reuse")
	if state != claudeSubCallAcquired || got != reused || waiter == nil {
		t.Fatalf("reused openCall = (%v, %v, %v), want the fresh call and a waiter", got, waiter, state)
	}
	h.resolve(cancelled, claudeSubToolResult{Text: "still stale"})
	h.mu.Lock()
	stale := got.result != nil
	h.mu.Unlock()
	if stale {
		t.Fatal("reused id received a stale result from the retired handle")
	}

	// A resolve on the reused handle still works normally.
	h.resolve(reused, claudeSubToolResult{Text: "fresh"})
	r, ok := h.claimResult(got)
	if !ok || r.Text != "fresh" {
		t.Fatalf("reused claimResult = (%q, %v), want (fresh, true)", r.Text, ok)
	}
	h.mu.Lock()
	left := len(h.calls)
	h.mu.Unlock()
	if left != 0 {
		t.Fatalf("live calls after reuse = %d, want 0", left)
	}
}

func TestClaudeSubMCPConcurrentCallsResolveIndependently(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.setTools([]ToolSpec{{Function: ToolFunctionSpec{Name: "read", Parameters: claudeSubMCPObjectSchema(nil)}}})
	cs := claudeSubMCPConnect(t, h, h.token, nil)

	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	const n = 8
	results := make([]chan *mcp.CallToolResult, n)
	errs := make([]chan error, n)
	for i := 0; i < n; i++ {
		results[i] = make(chan *mcp.CallToolResult, 1)
		errs[i] = make(chan error, 1)
		go func(i int) {
			id := fmt.Sprintf("toolu_c%d", i)
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{
				Name: "read",
				Meta: mcp.Meta{"claudecode/toolUseId": id},
			})
			if err != nil {
				errs[i] <- err
				return
			}
			results[i] <- res
		}(i)
	}
	for i := 0; i < n; i++ {
		claudeSubMCPWaitWaiter(t, h, fmt.Sprintf("toolu_c%d", i))
	}
	for _, i := range []int{5, 1, 7, 3, 0, 6, 2, 4} {
		h.resolve(h.beginCall(fmt.Sprintf("toolu_c%d", i)), claudeSubToolResult{Text: fmt.Sprintf("r%d", i)})
	}
	for i := 0; i < n; i++ {
		select {
		case res := <-results[i]:
			if got, want := claudeSubMCPText(res), fmt.Sprintf("r%d", i); got != want {
				t.Errorf("call %d text = %q, want %q", i, got, want)
			}
		case err := <-errs[i]:
			t.Errorf("call %d: %v", i, err)
		case <-time.After(claudeSubMCPTestTimeout):
			t.Fatalf("call %d did not return", i)
		}
	}
}

func TestClaudeSubMCPCloseForceClosesStandaloneSSE(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	httpClient := &http.Client{Transport: claudeSubMCPBearerTransport{token: h.token, base: http.DefaultTransport}}
	tr := &mcp.StreamableClientTransport{Endpoint: h.url, HTTPClient: httpClient, MaxRetries: -1}
	client := mcp.NewClient(&mcp.Implementation{Name: "claudesub-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer cancel()
	cs, err := client.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatalf("connect mcp client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	// The default client holds a standalone SSE stream open, so Shutdown cannot
	// complete until Close's deadline fallback force-closes the connection.
	waited := make(chan struct{})
	go func() {
		_ = cs.Wait()
		close(waited)
	}()

	start := time.Now()
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed < h.shutdownGrace {
		t.Errorf("Close returned after %v, before the %v shutdown grace; the standalone SSE stream was not held open", elapsed, h.shutdownGrace)
	}
	select {
	case <-waited:
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("standalone SSE stream was not force-closed")
	}
}

func TestClaudeSubMCPWriteConfigReplacesPermissiveFile(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}

	got, err := h.writeConfig(dir)
	if err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0o600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if want := claudeSubMCPWantConfig(h); string(data) != want {
		t.Errorf("config JSON = %s, want %s", data, want)
	}
}

func TestClaudeSubMCPWriteConfigRejectsSymlink(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("secret-target"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "mcp.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := h.writeConfig(dir)
	if err == nil {
		t.Fatalf("writeConfig through a symlink succeeded with %q, want an error", got)
	}
	if strings.Contains(err.Error(), h.token) {
		t.Error("symlink rejection error leaked the bearer token")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "secret-target" {
		t.Errorf("symlink target was modified: %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced by writeConfig")
	}
}

func TestClaudeSubMCPWriteConfigRejectsNonRegularFile(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "mcp.json"), 0o700); err != nil {
		t.Fatalf("mkdir config path: %v", err)
	}

	if _, err := h.writeConfig(dir); err == nil {
		t.Error("writeConfig over a directory succeeded, want an error")
	}
}

// TestClaudeSubMCPWriteConfigStaysInOpenedRoot replaces the config directory
// path with a symlink after opening it as a Root and checks the config still
// lands in the directory the Root pinned, never in the symlink target. It
// exercises the same root-relative inspect/temp/rename path writeConfig uses.
func TestClaudeSubMCPWriteConfigStaysInOpenedRoot(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	attacker := filepath.Join(parent, "attacker")
	if err := os.Mkdir(attacker, 0o700); err != nil {
		t.Fatalf("mkdir attacker: %v", err)
	}
	moved := filepath.Join(parent, "cfg-moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Skipf("directory rename unavailable: %v", err)
	}
	if err := os.Symlink(attacker, dir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := claudeSubMCPReplaceConfig(root, "mcp.json", []byte("{}")); err != nil {
		t.Fatalf("replace config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "mcp.json")); err != nil {
		t.Errorf("config not written into the opened root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attacker, "mcp.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("config escaped the opened root into the symlink target (stat err = %v)", err)
	}
}
