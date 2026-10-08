package provider

import (
	"context"
	"encoding/base64"
	"fmt"
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
		_, ok := h.waiters[id]
		h.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiter for %q never registered", id)
}

// claudeSubMCPWaitCancelled blocks until the host has tombstoned id after a
// cancelled or closed call.
func claudeSubMCPWaitCancelled(t *testing.T, h *claudeSubMCPHost, id string) {
	t.Helper()
	deadline := time.Now().Add(claudeSubMCPTestTimeout)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		_, done := h.cancelled[id]
		h.mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%q was never tombstoned", id)
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
	h.resolve("toolu_1", claudeSubToolResult{
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
	_, stillStored := h.results["toolu_1"]
	h.mu.Unlock()
	if stillStored {
		t.Error("consumed result was not deleted")
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

	h.resolve("toolu_2", claudeSubToolResult{Text: "world"})
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

	h.resolve("toolu_3", claudeSubToolResult{Text: "done"})
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
	_, live := h.waiters["toolu_dup"]
	h.mu.Unlock()
	if !live {
		t.Fatal("original waiter was clobbered by the duplicate call")
	}

	h.resolve("toolu_dup", claudeSubToolResult{Text: "original"})
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

	ctx, cancel := context.WithTimeout(context.Background(), claudeSubMCPTestTimeout)
	defer cancel()
	firstErr := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_reuse"},
		})
		firstErr <- err
	}()
	claudeSubMCPWaitWaiter(t, h, "toolu_reuse")
	cancel()
	select {
	case <-firstErr:
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("cancelled call did not return")
	}
	claudeSubMCPWaitCancelled(t, h, "toolu_reuse")

	// A late result for the cancelled call must not be stored.
	h.resolve("toolu_reuse", claudeSubToolResult{Text: "stale"})
	h.mu.Lock()
	_, stored := h.results["toolu_reuse"]
	h.mu.Unlock()
	if stored {
		t.Fatal("late resolve stored a result for a cancelled id")
	}

	// A reused id must not receive the stale result, nor a later late resolve.
	reuseCtx, reuseCancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer reuseCancel()
	reused := make(chan *mcp.CallToolResult, 1)
	reusedErr := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(reuseCtx, &mcp.CallToolParams{
			Name: "read",
			Meta: mcp.Meta{"claudecode/toolUseId": "toolu_reuse"},
		})
		if err != nil {
			reusedErr <- err
			return
		}
		reused <- res
	}()
	claudeSubMCPWaitWaiter(t, h, "toolu_reuse")
	h.resolve("toolu_reuse", claudeSubToolResult{Text: "still stale"})
	select {
	case res := <-reused:
		t.Fatalf("reused id received stale result %q", claudeSubMCPText(res))
	case err := <-reusedErr:
		t.Fatalf("reused CallTool: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	reuseCancel()
	select {
	case <-reusedErr:
	case res := <-reused:
		t.Fatalf("reused id unexpectedly resolved with %q", claudeSubMCPText(res))
	case <-time.After(claudeSubMCPTestTimeout):
		t.Fatal("reused call was not released by cancellation")
	}

	// A never-cancelled id still supports resolve-before-call.
	h.resolve("toolu_fresh", claudeSubToolResult{Text: "fresh"})
	freshCtx, freshCancel := context.WithTimeout(t.Context(), claudeSubMCPTestTimeout)
	defer freshCancel()
	res, err := cs.CallTool(freshCtx, &mcp.CallToolParams{
		Name: "read",
		Meta: mcp.Meta{"claudecode/toolUseId": "toolu_fresh"},
	})
	if err != nil {
		t.Fatalf("fresh CallTool: %v", err)
	}
	if got := claudeSubMCPText(res); got != "fresh" {
		t.Errorf("fresh text = %q, want %q", got, "fresh")
	}
}

func TestClaudeSubMCPTombstonesAreBounded(t *testing.T) {
	h := claudeSubMCPNewHost(t)
	h.maxTombstones = 2
	h.cancelCall("a")
	h.cancelCall("b")
	h.cancelCall("c") // evicts the oldest tombstone, "a"

	h.resolve("a", claudeSubToolResult{Text: "reopened"})
	h.mu.Lock()
	_, storedA := h.results["a"]
	h.mu.Unlock()
	if !storedA {
		t.Error("evicted tombstone still discarded resolve; the set is not bounded")
	}

	h.resolve("b", claudeSubToolResult{Text: "discarded"})
	h.mu.Lock()
	_, storedB := h.results["b"]
	h.mu.Unlock()
	if storedB {
		t.Error("live tombstone did not discard resolve")
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
		h.resolve(fmt.Sprintf("toolu_c%d", i), claudeSubToolResult{Text: fmt.Sprintf("r%d", i)})
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
