package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// claudeSubMCPServerName is the MCP server name the claude CLI sees. The CLI
	// prefixes tool names with "mcp__<server>__", so this fixes the prefix below.
	claudeSubMCPServerName = "steiner"
	// claudeSubToolPrefix is the prefix the claude CLI adds to every tool name
	// published by a server named claudeSubMCPServerName.
	claudeSubToolPrefix = "mcp__" + claudeSubMCPServerName + "__"
	// claudeSubToolNameMax is the MCP tool-name length limit including the prefix.
	claudeSubToolNameMax = 64
	// claudeSubHeartbeat is the default progress-notification interval while a
	// tool call waits for steiner to produce the result.
	claudeSubHeartbeat = 20 * time.Second
	// claudeSubShutdownGrace bounds HTTP-server shutdown before lingering
	// connections (such as a client's standalone SSE stream) are force-closed.
	claudeSubShutdownGrace = 2 * time.Second
	// claudeSubMCPMaxTombstones caps how many cancelled tool-use ids are kept so
	// a late resolve cannot re-store a cancelled result.
	claudeSubMCPMaxTombstones = 1024
)

// claudeSubToolName returns the name the claude CLI publishes for a steiner tool.
// Names that fit the 64-character limit with the mcp__steiner__ prefix are kept
// verbatim; longer names are deterministically shortened with a sha256 suffix.
func claudeSubToolName(name string) string {
	if len(claudeSubToolPrefix)+len(name) <= claudeSubToolNameMax {
		return name
	}
	keep := claudeSubToolNameMax - len(claudeSubToolPrefix) - len("_") - 8
	sum := sha256.Sum256([]byte(name))
	return name[:keep] + "_" + hex.EncodeToString(sum[:4])
}

// claudeSubToolResult is a tool result steiner stores for the claude CLI.
type claudeSubToolResult struct {
	Text    string
	Images  []ImageBlock
	IsError bool
}

// claudeSubMCPHost is one per-session in-process MCP server. It publishes
// steiner's tool specs over loopback streamable HTTP and answers tools/call from
// results steiner stores with resolve, holding calls open with progress
// heartbeats until the result arrives (D6, D18).
type claudeSubMCPHost struct {
	srv     *mcp.Server
	httpSrv *http.Server
	ln      net.Listener

	url   string
	token string

	// heartbeat is the progress-notification interval; tests shorten it.
	heartbeat time.Duration
	// shutdownGrace bounds HTTP-server shutdown before force-closing; tests
	// shorten it.
	shutdownGrace time.Duration

	mu sync.Mutex
	// maxTombstones caps the cancelled-id set; tests may lower it.
	maxTombstones int
	names         map[string]string // published CLI name (prefix stripped) -> steiner tool name
	toolKey       string            // fingerprint of the currently published specs
	results       map[string]claudeSubToolResult
	waiters       map[string]chan struct{}
	cancelled     map[string]struct{} // terminal tool-use ids; resolve discards them
	cancelOrder   []string            // bounded FIFO ring backing cancelled
	cancelNext    int
	closed        bool
	closeCh       chan struct{}
}

// newClaudeSubMCPHost starts a loopback MCP host with a random bearer token.
// The token is only ever exposed through writeConfig; it never reaches argv,
// logs, or returned errors.
func newClaudeSubMCPHost() (*claudeSubMCPHost, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate claude_subscription mcp token: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen claude_subscription mcp host: %w", err)
	}
	h := &claudeSubMCPHost{
		token:         hex.EncodeToString(tokenBytes),
		ln:            ln,
		heartbeat:     claudeSubHeartbeat,
		shutdownGrace: claudeSubShutdownGrace,
		maxTombstones: claudeSubMCPMaxTombstones,
		names:         map[string]string{},
		results:       map[string]claudeSubToolResult{},
		waiters:       map[string]chan struct{}{},
		cancelled:     map[string]struct{}{},
		closeCh:       make(chan struct{}),
	}
	h.srv = mcp.NewServer(&mcp.Implementation{Name: claudeSubMCPServerName, Version: "1"}, nil)
	h.url = "http://" + ln.Addr().String() + "/mcp"
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return h.srv }, &mcp.StreamableHTTPOptions{})
	mux := http.NewServeMux()
	mux.Handle("/mcp", h.authorize(stream))
	h.httpSrv = &http.Server{Handler: mux}
	go func() {
		// Serve returns ErrServerClosed after Shutdown; the error carries no token.
		_ = h.httpSrv.Serve(ln)
	}()
	return h, nil
}

// authorize rejects any request whose bearer token does not match with a
// constant-time comparison.
func (h *claudeSubMCPHost) authorize(next http.Handler) http.Handler {
	want := []byte("Bearer " + h.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setTools republishes the server's tools when the spec set changes. The claude
// CLI always applies the mcp__steiner__ prefix itself, so long names are
// shortened here and recorded for the reverse mapping.
func (h *claudeSubMCPHost) setTools(specs []ToolSpec) {
	key := claudeSubToolsKey(specs)
	h.mu.Lock()
	defer h.mu.Unlock()
	if key == h.toolKey {
		return
	}
	old := make([]string, 0, len(h.names))
	for name := range h.names {
		old = append(old, name)
	}
	h.srv.RemoveTools(old...)
	h.names = make(map[string]string, len(specs))
	for _, spec := range specs {
		short := claudeSubToolName(spec.Function.Name)
		h.names[short] = spec.Function.Name
		params := spec.Function.Parameters
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		h.srv.AddTool(&mcp.Tool{
			Name:        short,
			Description: spec.Function.Description,
			InputSchema: params,
			Meta:        mcp.Meta{"anthropic/alwaysLoad": true},
		}, h.callTool)
	}
	h.toolKey = key
}

// steinerToolName maps a CLI-published tool name back to the steiner tool name.
func (h *claudeSubMCPHost) steinerToolName(cliName string) string {
	stripped := strings.TrimPrefix(cliName, claudeSubToolPrefix)
	h.mu.Lock()
	defer h.mu.Unlock()
	if name, ok := h.names[stripped]; ok {
		return name
	}
	return stripped
}

// callTool answers one tools/call. The claude CLI tags each call with the
// tool_use id in _meta["claudecode/toolUseId"]; the result may have been stored
// before this handler ran or may arrive later via resolve (D18). While waiting
// it emits progress heartbeats so the CLI keeps the SSE response open.
func (h *claudeSubMCPHost) callTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, _ := req.Params.Meta["claudecode/toolUseId"].(string)
	if id == "" {
		return claudeSubMCPTextResult("missing tool use id", true), nil
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return claudeSubMCPTextResult("tool call cancelled", true), nil
	}
	if _, live := h.waiters[id]; live {
		// A tools/call reusing an in-flight id must not clobber the original
		// waiter; reject the duplicate and leave the original blocked.
		h.mu.Unlock()
		return claudeSubMCPTextResult("duplicate tool use id", true), nil
	}
	if r, ok := h.results[id]; ok {
		delete(h.results, id)
		h.mu.Unlock()
		return claudeSubMCPResult(r), nil
	}
	waiter := make(chan struct{})
	h.waiters[id] = waiter
	h.mu.Unlock()

	timer := time.NewTimer(h.heartbeat)
	defer timer.Stop()
	elapsed := time.Duration(0)
	for {
		select {
		case <-waiter:
			h.mu.Lock()
			r, ok := h.results[id]
			delete(h.results, id)
			h.mu.Unlock()
			if !ok {
				return claudeSubMCPTextResult("tool call cancelled", true), nil
			}
			return claudeSubMCPResult(r), nil
		case <-ctx.Done():
			h.cancelCall(id)
			return claudeSubMCPTextResult("tool call cancelled", true), nil
		case <-h.closeCh:
			h.cancelCall(id)
			return claudeSubMCPTextResult("tool call cancelled", true), nil
		case <-timer.C:
			elapsed += h.heartbeat
			if token := req.Params.GetProgressToken(); token != nil {
				params := &mcp.ProgressNotificationParams{
					ProgressToken: token,
					Progress:      elapsed.Seconds(),
					Message:       "steiner is running the tool",
				}
				// Best effort: a failed heartbeat must not end the tool call.
				_ = req.Session.NotifyProgress(ctx, params)
			}
			timer.Reset(h.heartbeat)
		}
	}
}

// resolve stores a tool result and wakes the blocked call, if any. It works
// whether it runs before or after the CLI's tools/call arrives. A result for a
// cancelled id is discarded so a reused id can never be handed a stale result.
func (h *claudeSubMCPHost) resolve(id string, r claudeSubToolResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if _, done := h.cancelled[id]; done {
		return
	}
	h.results[id] = r
	if waiter, ok := h.waiters[id]; ok {
		delete(h.waiters, id)
		close(waiter)
	}
}

// cancelAll releases every blocked call as cancelled and drops stored results.
func (h *claudeSubMCPHost) cancelAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.results {
		h.markCancelledLocked(id)
	}
	for id, waiter := range h.waiters {
		delete(h.waiters, id)
		h.markCancelledLocked(id)
		close(waiter)
	}
	h.results = map[string]claudeSubToolResult{}
}

// cancelCall drops one waiter that is no longer waiting (context done or host
// closed) and tombstones its id so a late resolve is discarded.
func (h *claudeSubMCPHost) cancelCall(id string) {
	h.mu.Lock()
	delete(h.waiters, id)
	delete(h.results, id)
	h.markCancelledLocked(id)
	h.mu.Unlock()
}

// markCancelledLocked records id as terminal so resolve discards it. The set is
// a bounded FIFO: once it is full, the oldest tombstone is evicted, so a
// long-lived host cannot grow it without limit. Tool-use ids are unique per call
// in practice, so eviction only re-opens pathological reuse.
func (h *claudeSubMCPHost) markCancelledLocked(id string) {
	if h.maxTombstones <= 0 {
		return
	}
	if _, ok := h.cancelled[id]; ok {
		return
	}
	if len(h.cancelOrder) < h.maxTombstones {
		h.cancelOrder = append(h.cancelOrder, id)
	} else {
		delete(h.cancelled, h.cancelOrder[h.cancelNext])
		h.cancelOrder[h.cancelNext] = id
		h.cancelNext = (h.cancelNext + 1) % h.maxTombstones
	}
	h.cancelled[id] = struct{}{}
}

// writeConfig writes the claude CLI --mcp-config file into dir (which the caller
// provides private and 0o700) as dir/mcp.json with mode 0o600.
func (h *claudeSubMCPHost) writeConfig(dir string) (string, error) {
	cfg := claudeSubMCPConfig{MCPServers: map[string]claudeSubMCPServerConfig{
		claudeSubMCPServerName: {
			Type:    "http",
			URL:     h.url,
			Headers: map[string]string{"Authorization": "Bearer " + h.token},
		},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal claude_subscription mcp config: %w", err)
	}
	const name = "mcp.json"
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open claude_subscription mcp dir: %w", err)
	}
	defer root.Close()
	// Refuse to write the token through a symlink or any other non-regular path
	// in the private directory.
	if info, err := root.Lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refuse to write claude_subscription mcp config: %s is a symlink", name)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("refuse to write claude_subscription mcp config: %s is not a regular file", name)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect claude_subscription mcp config: %w", err)
	}
	// Write a fresh 0o600 temp file and atomically replace the target, so the
	// token is never written through a symlink and any pre-existing permissive
	// file is replaced with the exact mode.
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create claude_subscription mcp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("set claude_subscription mcp config mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write claude_subscription mcp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close claude_subscription mcp config: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("replace claude_subscription mcp config: %w", err)
	}
	return path, nil
}

// Close shuts the HTTP server down and releases every blocked call.
func (h *claudeSubMCPHost) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()
	h.cancelAll()
	close(h.closeCh)
	ctx, cancel := context.WithTimeout(context.Background(), h.shutdownGrace)
	defer cancel()
	err := h.httpSrv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		// A client can hold a standalone SSE stream open, keeping Shutdown
		// waiting; force any remaining connections closed.
		return h.httpSrv.Close()
	}
	return err
}

// claudeSubMCPConfig is the exact JSON shape of the CLI's --mcp-config file.
type claudeSubMCPConfig struct {
	MCPServers map[string]claudeSubMCPServerConfig `json:"mcpServers"`
}

type claudeSubMCPServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// claudeSubMCPResult converts a stored result into MCP content: one text block
// (empty when Text is empty) plus one image block per decodable base64 image.
func claudeSubMCPResult(r claudeSubToolResult) *mcp.CallToolResult {
	content := make([]mcp.Content, 0, 1+len(r.Images))
	content = append(content, &mcp.TextContent{Text: r.Text})
	for _, img := range r.Images {
		raw, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			// Image blocks carry base64; skip anything that is not valid base64.
			continue
		}
		content = append(content, &mcp.ImageContent{MIMEType: img.MediaType, Data: raw})
	}
	return &mcp.CallToolResult{IsError: r.IsError, Content: content}
}

func claudeSubMCPTextResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// claudeSubToolsKey fingerprints a spec set so setTools can skip no-op updates.
// The schema marshal is deterministic (encoding/json sorts map keys).
func claudeSubToolsKey(specs []ToolSpec) string {
	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		schema, _ := json.Marshal(spec.Function.Parameters)
		parts = append(parts, spec.Function.Name+"\x00"+spec.Function.Description+"\x00"+string(schema))
	}
	sort.Strings(parts)
	return "v1:" + strings.Join(parts, "\x01")
}
