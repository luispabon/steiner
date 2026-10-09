package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ClaudeSubscriptionModel is one model the signed-in claude CLI reports for the
// account. ID is the full resolved model id (for example claude-haiku-5-5).
type ClaudeSubscriptionModel struct {
	ID               string
	DisplayName      string
	Description      string
	SupportedEfforts []string
}

const claudeSubDiscoveryToolOutputBytes = 65536

// The discovery budget is split into an absolute outer deadline and a reserved
// teardown window. The work phase (locate, spawn, initialize) gets at most
// claudeSubDiscoveryWorkTimeout and never more than the outer budget minus
// claudeSubDiscoveryReserve, so teardown always has room. All are variables so
// tests can drive the bounds directly.
var (
	claudeSubDiscoveryTimeout     = 20 * time.Second
	claudeSubDiscoveryWorkTimeout = 12 * time.Second
	claudeSubDiscoveryReserve     = 8 * time.Second
)

// ClaudeSubscriptionDiscoveryTimeout returns the total budget for one Claude
// subscription discovery, including CLI teardown. Callers must not end discovery
// earlier than this budget.
func ClaudeSubscriptionDiscoveryTimeout() time.Duration {
	return claudeSubDiscoveryTimeout
}

// claudeSubLocatorWaitDelay bounds how long a locator command's Wait may spend
// on a descendant that inherited and holds its output pipe.
var claudeSubLocatorWaitDelay = time.Second

// claudeSubLocatorOutputMaxBytes bounds the captured output of one locator
// command; a locator never produces more than a version string or a small JSON
// document.
var claudeSubLocatorOutputMaxBytes = 1 << 20

// Seams overridden in tests so discovery never runs a real claude CLI. The plan
// fixes the spawn seam; the locate seam is needed because locating the CLI runs
// `claude --version` and `claude auth status` via exec, which tests must avoid.
var (
	claudeSubDiscoveryLocate = func(ctx context.Context) (claudeSubCLI, error) {
		return locateClaudeSubCLI(ctx, exec.LookPath, claudeSubExecRunner)
	}
	claudeSubDiscoverySpawn = spawnClaudeSubProcess
)

// claudeSubBoundedBuffer captures up to max bytes and records whether more were
// written, so a runaway locator fails closed instead of filling memory.
type claudeSubBoundedBuffer struct {
	mu       sync.Mutex
	max      int
	buf      []byte
	overflow bool
}

func newClaudeSubBoundedBuffer(max int) *claudeSubBoundedBuffer {
	return &claudeSubBoundedBuffer{max: max}
}

func (b *claudeSubBoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - len(b.buf)
	if room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	if len(p) > room {
		b.overflow = true
	}
	return len(p), nil
}

func (b *claudeSubBoundedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}

func (b *claudeSubBoundedBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}

// claudeSubDiscoveryArgs is the argv for the short-lived model discovery
// process. It is a subset of claudeSubArgs: discovery sends no user turn, so it
// carries no model, thinking, settings or permission flags.
func claudeSubDiscoveryArgs() []string {
	return []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources=",
		"--no-session-persistence",
	}
}

// DiscoverClaudeSubscriptionModels lists the account's models through the claude
// CLI's initialize control message, without a model call. Any failure returns an
// error so callers can fall back to a static list. It never returns models
// unless the router and the connection's terminal cleanup both completed inside
// the outer deadline.
func DiscoverClaudeSubscriptionModels(ctx context.Context) ([]ClaudeSubscriptionModel, error) {
	// The absolute budget: the caller's context capped at claudeSubDiscoveryTimeout.
	outerCtx, cancelOuter := context.WithTimeout(ctx, claudeSubDiscoveryTimeout)
	defer cancelOuter()
	outerDeadline, _ := outerCtx.Deadline()

	// Refuse to start if the caller leaves no room for the reserved teardown
	// window: spawning then would risk abandoning a child past the deadline.
	remaining := time.Until(outerDeadline)
	if remaining < claudeSubDiscoveryReserve {
		return nil, fmt.Errorf("claude CLI model discovery: only %s of budget left, need %s reserved for cleanup", remaining.Round(time.Millisecond), claudeSubDiscoveryReserve)
	}

	// The work phase is capped at the work timeout and by whatever is left of
	// the outer budget after the reserved teardown window.
	workTimeout := claudeSubDiscoveryWorkTimeout
	if room := remaining - claudeSubDiscoveryReserve; room < workTimeout {
		workTimeout = room
	}
	workCtx, cancelWork := context.WithTimeout(outerCtx, workTimeout)
	defer cancelWork()

	cli, err := claudeSubDiscoveryLocate(workCtx)
	if err != nil {
		return nil, err
	}

	conn, err := claudeSubDiscoverySpawn(workCtx, cli.Path, claudeSubDiscoveryArgs(), claudeSubChildEnv(os.Environ(), claudeSubDiscoveryToolOutputBytes), "")
	if err != nil {
		return nil, fmt.Errorf("start claude CLI for model discovery: %w", err)
	}

	control := newClaudeSubControl(conn)
	stop := make(chan struct{})
	routerDone := make(chan struct{})
	go func() {
		defer close(routerDone)
		claudeSubRoute(conn.Events(), stop, control, nil)
	}()

	raw, reqErr := control.request(workCtx, "initialize", nil)

	// Cleanup always runs and is bounded by the remaining outer budget. It is
	// independent of the caller's cancellation so a cancelled caller still gets
	// a clean shutdown, and it never extends past the outer deadline.
	cleanupCtx, cancelCleanup := context.WithDeadline(context.WithoutCancel(ctx), outerDeadline)
	defer cancelCleanup()

	// Stop the router's pump. The router releases pending control requests as
	// it exits; that must not gate the terminal connection shutdown below.
	close(stop)

	// Terminal connection shutdown is unconditional and comes before the router
	// join: a router or control writer that never settles must not leave the CLI
	// alive. ForceClose skips the coordinator's grace intervals so teardown fits
	// the reserved budget instead of spending two grace periods past it.
	closeErr := conn.ForceClose(cleanupCtx)

	// Join the router and the control writer with whatever budget remains. The
	// router terminates the control and joins its writer as it exits; joining
	// both here keeps the join bounded by the cleanup budget so a stalled router
	// cannot extend discovery past the outer deadline.
	routerJoined := claudeSubJoined(cleanupCtx, routerDone)
	writerJoined := claudeSubJoined(cleanupCtx, control.writerDone)

	if closeErr != nil {
		return nil, fmt.Errorf("claude CLI model discovery: cleanup did not complete: %w", closeErr)
	}
	if !routerJoined || !writerJoined {
		return nil, fmt.Errorf("claude CLI model discovery: cleanup did not complete before the deadline: %w", cleanupCtx.Err())
	}

	if reqErr != nil {
		return nil, fmt.Errorf("claude CLI model discovery: %w", reqErr)
	}
	return claudeSubParseModels(raw)
}

// claudeSubJoined reports whether done closed before ctx expired.
func claudeSubJoined(ctx context.Context, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// claudeSubInitializeResponse is the model list carried by an initialize
// control response.
type claudeSubInitializeResponse struct {
	Models []claudeSubInitializeModel `json:"models"`
}

type claudeSubInitializeModel struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// claudeSubParseModels converts an initialize response into deduplicated models.
// The "default" alias is skipped and the first entry for each resolved model id
// wins.
func claudeSubParseModels(raw json.RawMessage) ([]ClaudeSubscriptionModel, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("claude CLI initialize response carried no model list")
	}
	var payload claudeSubInitializeResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse claude CLI initialize response: %w", err)
	}
	if len(payload.Models) == 0 {
		return nil, fmt.Errorf("claude CLI initialize response listed no models")
	}

	models := make([]ClaudeSubscriptionModel, 0, len(payload.Models))
	seen := make(map[string]struct{}, len(payload.Models))
	for _, m := range payload.Models {
		if m.Value == "default" {
			continue
		}
		if m.ResolvedModel == "" {
			return nil, fmt.Errorf("claude CLI initialize response has a model without a resolved model: value %q", m.Value)
		}
		if _, dup := seen[m.ResolvedModel]; dup {
			continue
		}
		seen[m.ResolvedModel] = struct{}{}
		models = append(models, ClaudeSubscriptionModel{
			ID:               m.ResolvedModel,
			DisplayName:      m.DisplayName,
			Description:      m.Description,
			SupportedEfforts: m.SupportedEffortLevels,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("claude CLI initialize response listed no usable models")
	}
	return models, nil
}
