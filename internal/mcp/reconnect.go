package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// reconnectWorker runs sequential fresh-connect attempts until one succeeds or
// the consecutive-failure cap is reached. At most one worker runs per server at
// a time (kickReconnectLocked guarantees it). The tool set is never re-listed
// (D14) and the failed call is never replayed (D16).
func (s *Session) reconnectWorker() {
	s.reportStatus(ServerStatusReconnecting, nil)

	for {
		if s.managerCtx.Err() != nil {
			// The owning manager is closed; abandon the reconnect and release
			// every caller blocked on the outcome future.
			s.resolveReconnect(fmt.Errorf("reconnect aborted: manager closed: %w", s.managerCtx.Err()), ServerStatusUnavailable)
			return
		}

		newSDK, newCmd, err := s.reconnectOnce()
		if err == nil {
			s.mu.Lock()
			if s.managerCtx.Err() != nil || s.closed {
				s.mu.Unlock()
				_ = newSDK.Close()
				abortErr := errors.New("reconnect aborted: session closed")
				if ctxErr := s.managerCtx.Err(); ctxErr != nil {
					abortErr = fmt.Errorf("reconnect aborted: manager closed: %w", ctxErr)
				}
				s.resolveReconnect(abortErr, ServerStatusUnavailable)
				return
			}
			old := s.sdk
			s.sdk = newSDK
			s.cmd = newCmd
			s.protocolVersion = newSDK.InitializeResult().ProtocolVersion
			s.consecutiveFailures = 0 // a successful reconnect resets the cap
			s.reconnecting = false
			s.reconnectOutcome = nil
			close(s.reconnectDone)
			s.mu.Unlock()
			if old != nil {
				closeOldSession(old)
			}
			s.reportStatus(ServerStatusConnected, nil)
			return
		}

		s.mu.Lock()
		s.consecutiveFailures++
		reachedCap := s.consecutiveFailures >= maxReconnectAttempts
		s.mu.Unlock()
		if reachedCap {
			s.resolveReconnect(fmt.Errorf("mcp server %q unavailable after %d consecutive failed reconnect attempts: %w", s.name, maxReconnectAttempts, err), ServerStatusUnavailable)
			return
		}
	}
}

// reconnectOnce performs one reconnect attempt: a fresh transport built with the
// original spec and process-group wrapping, then a new ClientSession via
// client.Connect re-running the handshake. It never calls ListTools (D14).
func (s *Session) reconnectOnce() (*mcpsdk.ClientSession, *exec.Cmd, error) {
	// The transport is bound to the manager context (mirroring ConnectSession),
	// not the attempt's timeout context, so a respawned stdio server process
	// outlives the handshake deadline.
	var transport mcpsdk.Transport
	var cmd *exec.Cmd
	var releaseCommand func()
	switch s.spec.Transport {
	case "http":
		t, err := newHTTPTransport(s.spec)
		if err != nil {
			return nil, nil, fmt.Errorf("reconnect to mcp server %q: %w", s.name, err)
		}
		transport = t
	case "", "stdio":
		var err error
		transport, cmd, releaseCommand, err = newStdioTransport(s.managerCtx, s.spec, s.wrap, s.release, s.stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("reconnect to mcp server %q: %w", s.name, err)
		}
		defer releaseCommand()
	default:
		return nil, nil, fmt.Errorf("reconnect to mcp server %q: unsupported transport %q", s.name, s.spec.Transport)
	}

	connectCtx, cancel := context.WithTimeout(s.managerCtx, s.timeout)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "steiner", Version: clientVersion}, nil)
	sdk, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, cmd, fmt.Errorf("reconnect to mcp server %q: %w", s.name, err)
	}
	return sdk, cmd, nil
}
