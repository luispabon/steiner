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
	want := fmt.Sprintf(`{"mcpServers":{"steiner":{"type":"http","url":%q,"headers":{"Authorization":"Bearer %s"}}}}`, h.url, h.token)
	if string(data) != want {
		t.Errorf("config JSON = %s, want %s", data, want)
	}
}
