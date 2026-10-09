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
	// claudeSubRetiredMax bounds how many completed call ids the host remembers
	// to answer a repeat tools/call immediately. The oldest id is forgotten first.
	claudeSubRetiredMax = 1024
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

// claudeSubCall is the per-tool-use state the host allocates for one tool_use
// id. Ownership lives on this value rather than in maps keyed by the raw id, so
// resolve can only target the exact call it was handed: a cancelled or reused id
// can never be delivered a stale result, and the host never has to remember
// unbounded terminal ids. A call leaves the live set once its handler consumes
// the result or it is cancelled, so the set stays bounded by in-flight calls.
type claudeSubCall struct {
	id         string
	waiter     chan struct{}        // non-nil while a tools/call handler blocks
	result     *claudeSubToolResult // set once by resolve, taken by the handler
	owner      bool                 // a tools/call handler currently owns the call
	done       bool                 // terminal: consumed, cancelled, or host closed
	registered bool                 // beginCall announced the id (the model issued a tool_use)
}

// claudeSubCallState is the outcome of a tools/call trying to own a call.
type claudeSubCallState int

const (
	// claudeSubCallAcquired means the handler now owns the call.
	claudeSubCallAcquired claudeSubCallState = iota
	// claudeSubCallDuplicate means another handler already owns the id.
	claudeSubCallDuplicate
	// claudeSubCallRetired means the id already completed and is not reopened.
	claudeSubCallRetired
	// claudeSubCallClosed means the host is shutting down.
	claudeSubCallClosed
)

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
	// calls holds only live calls keyed by tool-use id; tests observe it to
	// follow registration and retirement.
	calls   map[string]*claudeSubCall
	names   map[string]string // published CLI name (prefix stripped) -> steiner tool name
	toolKey string            // fingerprint of the currently published specs
	closed  bool
	closeCh chan struct{}

	// retired remembers ids whose calls completed, oldest first in retiredOrder,
	// so a repeat tools/call is answered at once. Bounded by claudeSubRetiredMax.
	retired      map[string]struct{}
	retiredOrder []string
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
		calls:         map[string]*claudeSubCall{},
		names:         map[string]string{},
		retired:       map[string]struct{}{},
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
// shortened here and recorded for the reverse mapping. Every tool is built and
// checked before the live set changes, so an invalid schema returns an error and
// leaves the previously published tools in place.
func (h *claudeSubMCPHost) setTools(specs []ToolSpec) error {
	key := claudeSubToolsKey(specs)
	h.mu.Lock()
	defer h.mu.Unlock()
	if key == h.toolKey {
		return nil
	}
	tools := make([]*mcp.Tool, 0, len(specs))
	names := make(map[string]string, len(specs))
	for _, spec := range specs {
		tool, err := claudeSubPublishedTool(spec)
		if err != nil {
			return fmt.Errorf("publish claude_subscription tool %q: %w", spec.Function.Name, err)
		}
		tools = append(tools, tool)
		names[tool.Name] = spec.Function.Name
	}
	old := make([]string, 0, len(h.names))
	for name := range h.names {
		old = append(old, name)
	}
	h.srv.RemoveTools(old...)
	h.names = names
	for _, tool := range tools {
		h.srv.AddTool(tool, h.callTool)
	}
	h.toolKey = key
	return nil
}

// claudeSubPublishedTool builds the MCP tool for spec with its own copy of the
// input schema. The go-sdk rejects a bad schema by panicking inside AddTool, so
// the tool is first added to a scratch server and that panic becomes an error.
func claudeSubPublishedTool(spec ToolSpec) (tool *mcp.Tool, err error) {
	schema, err := claudeSubToolSchema(spec.Function.Parameters)
	if err != nil {
		return nil, err
	}
	tool = &mcp.Tool{
		Name:        claudeSubToolName(spec.Function.Name),
		Description: spec.Function.Description,
		InputSchema: schema,
		Meta:        mcp.Meta{"anthropic/alwaysLoad": true},
	}
	defer func() {
		if r := recover(); r != nil {
			tool, err = nil, fmt.Errorf("go-sdk rejected input schema: %v", r)
		}
	}()
	mcp.NewServer(&mcp.Implementation{Name: claudeSubMCPServerName, Version: "1"}, nil).AddTool(tool, nil)
	return tool, nil
}

// claudeSubToolSchema returns a copy of a tool's input schema. A missing type
// becomes "object"; any other type is an error, since the go-sdk requires an
// object schema. The copy keeps the registry's map from being modified.
func claudeSubToolSchema(params map[string]any) (map[string]any, error) {
	if params == nil {
		return map[string]any{"type": "object"}, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal input schema: %w", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("copy input schema: %w", err)
	}
	typ, ok := schema["type"]
	if !ok {
		schema["type"] = "object"
		return schema, nil
	}
	if typ != "object" {
		return nil, fmt.Errorf("input schema type must be \"object\", got %v", typ)
	}
	return schema, nil
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

	call, waiter, state := h.openCall(id)
	switch state {
	case claudeSubCallClosed:
		return claudeSubMCPTextResult("tool call cancelled", true), nil
	case claudeSubCallDuplicate:
		// Another handler still owns this id, either waiting or holding a
		// resolved result it has not consumed yet; it must not be stolen.
		return claudeSubMCPTextResult("duplicate tool use id", true), nil
	case claudeSubCallRetired:
		// The id already completed. A repeat (CLI retry) is answered now rather
		// than held open for a result that will never be resolved.
		return claudeSubMCPTextResult("tool call already completed", true), nil
	}
	if waiter == nil {
		// resolve ran before this tools/call; hand the stored result back.
		if r, ok := h.claimResult(call); ok {
			return claudeSubMCPResult(r), nil
		}
		return claudeSubMCPTextResult("tool call cancelled", true), nil
	}

	timer := time.NewTimer(h.heartbeat)
	defer timer.Stop()
	elapsed := time.Duration(0)
	for {
		select {
		case <-waiter:
			if r, ok := h.claimResult(call); ok {
				return claudeSubMCPResult(r), nil
			}
			return claudeSubMCPTextResult("tool call cancelled", true), nil
		case <-ctx.Done():
			h.cancelCall(call)
			return claudeSubMCPTextResult("tool call cancelled", true), nil
		case <-h.closeCh:
			h.cancelCall(call)
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

// beginCall allocates the per-call state for a tool-use id. steiner calls it as
// soon as the CLI emits a tool_use block, before the matching tools/call may
// arrive, and holds the returned call to resolve the result later, so resolve
// works whether it runs before or after the tools/call (D18). Registration is
// the only way to obtain a resolvable call, so a result is never stored for an
// id that was not explicitly opened, and a reused id gets a fresh call rather
// than the retired one.
func (h *claudeSubMCPHost) beginCall(id string) *claudeSubCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	if call, ok := h.calls[id]; ok {
		call.registered = true
		return call
	}
	call := &claudeSubCall{id: id, registered: true}
	h.calls[id] = call
	return call
}

// openCall gives this tools/call handler ownership of the call for id, creating
// the call when the CLI's tools/call arrived before steiner registered it. A
// call already owned by another handler is a duplicate: ownership persists from
// registration until the handler consumes a resolved result or retires the
// call, so a duplicate cannot race in and claim the original's result. When a
// result is already stored it returns a nil waiter and the caller must take the
// result with claimResult; otherwise it returns the waiter to block on. The
// ownership check and the waiter install share one lock, so a resolve can never
// slip between the readiness check and the block.
func (h *claudeSubMCPHost) openCall(id string) (*claudeSubCall, chan struct{}, claudeSubCallState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, claudeSubCallClosed
	}
	call, ok := h.calls[id]
	if !ok {
		// A retired id is not reopened: the call it named already completed.
		if _, retired := h.retired[id]; retired {
			return nil, nil, claudeSubCallRetired
		}
		call = &claudeSubCall{id: id}
		h.calls[id] = call
	}
	if call.owner {
		return nil, nil, claudeSubCallDuplicate
	}
	call.owner = true
	if call.result != nil {
		return call, nil, claudeSubCallAcquired
	}
	waiter := make(chan struct{})
	call.waiter = waiter
	return call, waiter, claudeSubCallAcquired
}

// claimResult consumes the result stored on a call the handler owns and retires
// the call. It reports whether a result was present; false means the call was
// cancelled or the host closed.
func (h *claudeSubMCPHost) claimResult(call *claudeSubCall) (claudeSubToolResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := call.result
	h.retireLocked(call)
	if r == nil {
		return claudeSubToolResult{}, false
	}
	return *r, true
}

// resolve stores a tool result on the call and wakes a blocked handler, if any.
// It works whether it runs before or after the CLI's tools/call arrives. Only a
// call this host currently owns and is live accepts a result: a handle from
// another host, or one already retired or replaced by a reused id, is ignored
// without touching its waiter, so a late or foreign result can never be
// delivered to a cancelled or reused id.
func (h *claudeSubMCPHost) resolve(call *claudeSubCall, r claudeSubToolResult) {
	if call == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if call.done || h.calls[call.id] != call || call.result != nil {
		return
	}
	call.result = &r
	if call.waiter != nil {
		close(call.waiter)
		call.waiter = nil
	}
}

// cancelAll releases every live call as cancelled and drops stored results.
func (h *claudeSubMCPHost) cancelAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, call := range h.calls {
		h.retireLocked(call)
	}
}

// cancelCall retires a call whose handler is no longer waiting (context done or
// host closed) and wakes it so it returns a cancelled result.
func (h *claudeSubMCPHost) cancelCall(call *claudeSubCall) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.retireLocked(call)
}

// retireLocked terminally ends a call: it clears any stored result, drops the
// call from the live set, and wakes a blocked handler. Once retired the call can
// never accept a result, so a late resolve on its handle is discarded.
func (h *claudeSubMCPHost) retireLocked(call *claudeSubCall) {
	call.done = true
	call.result = nil
	if h.calls[call.id] == call {
		delete(h.calls, call.id)
		h.rememberRetiredLocked(call.id)
	}
	if call.waiter != nil {
		close(call.waiter)
		call.waiter = nil
	}
}

// rememberRetiredLocked records a completed id, forgetting the oldest one once
// claudeSubRetiredMax ids are held.
func (h *claudeSubMCPHost) rememberRetiredLocked(id string) {
	if _, ok := h.retired[id]; ok {
		return
	}
	if len(h.retiredOrder) == claudeSubRetiredMax {
		delete(h.retired, h.retiredOrder[0])
		h.retiredOrder = h.retiredOrder[1:]
	}
	h.retired[id] = struct{}{}
	h.retiredOrder = append(h.retiredOrder, id)
}

// settle fails every call that a tools/call opened for an id the model never
// issued. It runs once the turn's assistant message is fully decoded (or the
// turn ends without tool calls), so every tool_use the model issued has already
// been registered through beginCall.
//
// Race: the CLI sends the HTTP tools/call and the stdout tool_use on separate
// channels, so a tools/call for a legitimate id can open its call before
// beginCall runs. That call is left alone here, because its tool_use is
// registered during streaming, before the message is decoded. Only calls still
// unregistered at settle time are failed, so an early legitimate call is never
// failed. A failed call is retired, and its handler returns the failure at once.
func (h *claudeSubMCPHost) settle() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, call := range h.calls {
		if call.registered {
			continue
		}
		h.retireLocked(call)
		// retireLocked clears any stored result, so the failure is set after it.
		// claimResult reads it when the woken handler takes the result.
		call.result = &claudeSubToolResult{Text: "tool call id was not issued by the model", IsError: true}
	}
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
	if err := claudeSubMCPReplaceConfig(root, name, data); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// claudeSubMCPReplaceConfig writes data to name beneath root as an exact-0o600
// regular file, replacing any existing regular file. Every step (inspect, temp
// create, rename, cleanup) uses a root-relative operation, so the write stays
// anchored to the directory the Root was opened on even if that path is later
// swapped for a symlink; the token is never written through a symlink. A symlink
// or non-regular target is refused.
func claudeSubMCPReplaceConfig(root *os.Root, name string, data []byte) error {
	if info, err := root.Lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to write claude_subscription mcp config: %s is a symlink", name)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to write claude_subscription mcp config: %s is not a regular file", name)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect claude_subscription mcp config: %w", err)
	}
	// Write a fresh 0o600 temp file and atomically rename it into place, so any
	// pre-existing permissive file is replaced with the exact mode.
	tmpName, tmp, err := claudeSubMCPCreateTemp(root, name)
	if err != nil {
		return fmt.Errorf("create claude_subscription mcp config: %w", err)
	}
	defer func() { _ = root.Remove(tmpName) }() // no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set claude_subscription mcp config mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write claude_subscription mcp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close claude_subscription mcp config: %w", err)
	}
	if err := root.Rename(tmpName, name); err != nil {
		return fmt.Errorf("replace claude_subscription mcp config: %w", err)
	}
	return nil
}

// claudeSubMCPCreateTemp creates a fresh 0o600 file inside root named after the
// target, using only root-relative operations so the temp file cannot be
// redirected outside the directory. os.Root has no CreateTemp in Go 1.26, so it
// retries an O_EXCL open with a random suffix rather than falling back to raw
// os.CreateTemp.
func claudeSubMCPCreateTemp(root *os.Root, name string) (string, *os.File, error) {
	for i := 0; i < 100; i++ {
		tmpName := name + ".tmp-" + rand.Text()
		f, err := root.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return tmpName, f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("claude_subscription mcp config: could not allocate a temp file")
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
