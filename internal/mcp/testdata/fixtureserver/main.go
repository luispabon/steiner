// Command fixtureserver is a hand-rolled JSON-RPC 2.0 MCP server used by the
// internal/mcp integration tests. It deliberately does not import the MCP SDK:
// the tests exercise the SDK client against a plain stdio JSON-RPC peer.
//
// It reads newline-delimited JSON requests from stdin and writes newline-
// delimited JSON responses to stdout. Only JSON-RPC is written to stdout;
// diagnostics go to stderr via the log package.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func main() {
	recordPath := os.Getenv("STEINER_FIXTURE_RECORD")
	touchPath := os.Getenv("STEINER_FIXTURE_TOUCH")
	spawnChild := os.Getenv("STEINER_FIXTURE_SPAWN_CHILD") == "1"
	childPIDFile := os.Getenv("STEINER_FIXTURE_CHILD_PID_FILE")
	stallHandshake := os.Getenv("STEINER_FIXTURE_STALL_HANDSHAKE") == "1"
	dieEnabled := os.Getenv("STEINER_FIXTURE_ENABLE_DIE") == "1"
	startLogPath := os.Getenv("STEINER_FIXTURE_START_LOG")

	appendStartupLog(startLogPath)

	if touchPath != "" {
		err := os.WriteFile(touchPath, []byte("touch\n"), 0o644)
		value := "ok"
		if err != nil {
			value = err.Error()
			log.Printf("touch %s: %v", touchPath, err)
		}
		appendRecord(recordPath, "touch_error="+value)
	}

	if spawnChild {
		child := exec.Command("sleep", "600")
		if err := child.Start(); err != nil {
			log.Printf("spawn child: %v", err)
		} else {
			// Intentionally not waited on: the child must survive the server so
			// tests can prove process-group reaping kills it.
			if childPIDFile != "" {
				if err := os.WriteFile(childPIDFile, []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
					log.Printf("write child pid file: %v", err)
				}
			}
		}
	}

	// toolCallCounts backs the per-tool request_<tool>=<counter> record lines;
	// it must outlive individual requests.
	toolCallCounts := map[string]int{}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var req jsonRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			log.Printf("decode request: %v", err)
			continue
		}
		if req.ID == nil {
			// Notification: consume and do not respond.
			log.Printf("notification: %s", req.Method)
			continue
		}
		if stallHandshake && req.Method == "initialize" {
			log.Printf("stalling handshake on initialize")
			// Block without responding; the client must time out. A bare
			// select{} would trip the runtime deadlock detector and crash.
			time.Sleep(time.Hour)
		}
		resp := handle(req, recordPath, dieEnabled, toolCallCounts)
		out, err := json.Marshal(resp)
		if err != nil {
			log.Printf("encode response: %v", err)
			continue
		}
		fmt.Println(string(out))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read stdin: %v", err)
	}
	// Exit cleanly (code 0) on stdin EOF.
}

type jsonRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonError      `json:"error,omitempty"`
}

type jsonError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func handle(req jsonRequest, recordPath string, dieEnabled bool, toolCallCounts map[string]int) jsonResponse {
	base := jsonResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "server/discover":
		// Force the SDK down the legacy initialize path.
		base.Error = &jsonError{Code: -32601, Message: "Method not found"}
		return base
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params) // ignore: echo what we got
		if recordPath != "" {
			appendRecord(recordPath, "protocol_version="+params.ProtocolVersion)
		}
		base.Result = map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fixture", "version": "0.0.1"},
		}
		return base
	case "tools/list":
		base.Result = map[string]any{
			"tools": []map[string]any{
				{
					"name":        "echo",
					"description": "echoes text",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required":   []string{"text"},
					},
				},
				{
					"name":        "boom",
					"description": "always fails",
					"inputSchema": map[string]any{"type": "object"},
				},
				{
					"name":        "readonly_echo",
					"description": "echoes text (read-only)",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required":   []string{"text"},
					},
					"annotations": map[string]any{
						"readOnlyHint": true,
					},
				},
				{
					"name":        "die",
					"description": "exits the process before responding (only when STEINER_FIXTURE_ENABLE_DIE=1)",
					"inputSchema": map[string]any{"type": "object"},
				},
				{
					"name":        "sleep",
					"description": "sleeps for the given number of milliseconds before responding",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"ms": map[string]any{"type": "integer"}},
						"required":   []string{"ms"},
					},
				},
				{
					"name":        "big_output",
					"description": "returns a text blob of the given number of bytes",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"bytes": map[string]any{"type": "integer"}},
						"required":   []string{"bytes"},
					},
				},
			},
		}
		return base
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		// Record every tools/call before dispatch so even calls that kill the
		// server (die) or time out (sleep) are observed in the record file.
		recordToolCall(recordPath, params.Name, toolCallCounts)
		switch params.Name {
		case "echo":
			text, _ := params.Arguments["text"].(string)
			base.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			}
		case "readonly_echo":
			text, _ := params.Arguments["text"].(string)
			base.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			}
		case "boom":
			base.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": "deliberate failure"}},
				"isError": true,
			}
		case "die":
			if !dieEnabled {
				base.Result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "die tool disabled"}},
				}
				return base
			}
			// Exit before responding so the in-flight call fails with a raw EOF,
			// matching a server dying mid-request. Sync flushes stdout first; on
			// a pipe it may return EINVAL, but the response bytes were already
			// written by fmt.Println, so that error is harmless.
			_ = os.Stdout.Sync()
			os.Exit(0)
		case "sleep":
			ms, err := argInt(params.Arguments, "ms")
			if err != nil {
				base.Error = &jsonError{Code: -32602, Message: err.Error()}
				return base
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			base.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("slept %dms", ms)}},
			}
		case "big_output":
			n, err := argInt(params.Arguments, "bytes")
			if err != nil {
				base.Error = &jsonError{Code: -32602, Message: err.Error()}
				return base
			}
			if n < 0 {
				base.Error = &jsonError{Code: -32602, Message: "bytes must be non-negative"}
				return base
			}
			base.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": strings.Repeat("X", n)}},
			}
		default:
			base.Error = &jsonError{Code: -32602, Message: "unknown tool: " + params.Name}
		}
		return base
	case "ping":
		base.Result = map[string]any{}
		return base
	default:
		base.Error = &jsonError{Code: -32601, Message: "Method not found"}
		return base
	}
}

// appendRecord appends one key=value line to the record file, creating it if
// needed. Errors are logged; recording must never take the server down.
func appendRecord(recordPath, line string) {
	if recordPath == "" {
		return
	}
	f, err := os.OpenFile(recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("open record file: %v", err)
		return
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, line); err != nil {
		log.Printf("write record file: %v", err)
	}
}

// recordToolCall appends one request_<tool>=<counter> line per tools/call,
// with a monotonic counter per tool. Counters increment even when no record
// file is configured so they stay monotonic across requests.
func recordToolCall(recordPath, tool string, toolCallCounts map[string]int) {
	toolCallCounts[tool]++
	appendRecord(recordPath, "request_"+tool+"="+strconv.Itoa(toolCallCounts[tool]))
}

// appendStartupLog appends one startup=<counter> line to the start log. The
// counter is one more than the number of existing startup= lines, so every
// process start is distinguishable. A missing file simply starts at 1.
func appendStartupLog(path string) {
	if path == "" {
		return
	}
	n := 0
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "startup=") {
				n++
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("read start log %s: %v", path, err)
	}
	appendRecord(path, "startup="+strconv.Itoa(n+1))
}

// argInt reads an integer tool argument. JSON numbers decode into any as
// float64, so the type assertion targets float64.
func argInt(args map[string]any, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing %q argument", key)
	}
	f, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("%q argument must be a number", key)
	}
	return int(f), nil
}
