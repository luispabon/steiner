package lsp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"go.lsp.dev/jsonrpc2"
)

type serverErrorKind uint8

const (
	serverErrorNone serverErrorKind = iota
	serverErrorNoServer
	serverErrorExited
)

func classifyServerError(err error) serverErrorKind {
	switch {
	case errors.Is(err, errNoServer):
		return serverErrorNoServer
	case errors.Is(err, errServerExited):
		return serverErrorExited
	default:
		return serverErrorNone
	}
}

// handleSymbolsError processes errors from DocumentSymbols or WorkspaceSymbols,
// converting unavailability cases to readable result strings and returning an
// error for genuine failures. Unlike handleNavigationError, file may be empty
// (workspace mode), in which case there is no extension to report and
// findServerNameForFile/findFailedServer are not consulted.
func handleSymbolsError(m *Manager, file string, err error) (any, error) {
	if err == nil {
		return nil, nil
	}

	switch classifyServerError(err) {
	case serverErrorNoServer:
		if file == "" {
			return "No language server is enabled. Configure one under `lsp.servers` to enable this tool.", nil
		}
		ext := strings.ToLower(filepath.Ext(file))
		return fmt.Sprintf("No language server is configured for %s. Configure one under `lsp.servers` to enable this tool.", ext), nil
	case serverErrorExited:
		if file == "" {
			return nil, err
		}
		serverName := findServerNameForFile(m, file)
		if serverName == "" {
			return "Language server exited unexpectedly.", nil
		}
		return fmt.Sprintf("Language server %s exited unexpectedly.", serverName), nil
	}

	if file == "" {
		return nil, err
	}

	failedServer := findFailedServer(m, file)
	if failedServer != nil {
		return fmt.Sprintf("Language server %s failed to start: %v.", failedServer.Name, failedServer.Err), nil
	}

	return nil, err
}

// handleNavigationError processes errors from Definitions or References, converting
// unavailability cases to readable result strings and returning an error for genuine failures.
func handleNavigationError(m *Manager, file string, err error) (any, error) {
	if err == nil {
		return nil, nil
	}

	switch classifyServerError(err) {
	case serverErrorNoServer:
		ext := strings.ToLower(filepath.Ext(file))
		return fmt.Sprintf("No language server is configured for %s. Configure one under `lsp.servers` to enable this tool.", ext), nil
	case serverErrorExited:
		serverName := findServerNameForFile(m, file)
		if serverName == "" {
			return "Language server exited unexpectedly.", nil
		}
		return fmt.Sprintf("Language server %s exited unexpectedly.", serverName), nil
	}

	// Some methods (notably textDocument/implementation and
	// textDocument/typeDefinition) are optional in LSP; a healthy server may
	// answer with MethodNotFound rather than results.
	if errors.Is(err, jsonrpc2.ErrMethodNotFound) {
		serverName := findServerNameForFile(m, file)
		if serverName == "" {
			return "The language server does not support this request.", nil
		}
		return fmt.Sprintf("Language server %s does not support this request.", serverName), nil
	}

	failedServer := findFailedServer(m, file)
	if failedServer != nil {
		return fmt.Sprintf("Language server %s failed to start: %v.", failedServer.Name, failedServer.Err), nil
	}

	return nil, err
}

// handleDiagnosticsError processes errors from Diagnostics, converting unavailability
// cases to readable result strings and returning an error for genuine failures.
func handleDiagnosticsError(m *Manager, file string, err error) (any, error) {
	if err == nil {
		return nil, nil
	}

	switch classifyServerError(err) {
	case serverErrorNoServer:
		ext := strings.ToLower(filepath.Ext(file))
		return fmt.Sprintf("No language server is configured for %s. Configure one under `lsp.servers` to enable this tool.", ext), nil
	case serverErrorExited:
		serverName := findServerNameForFile(m, file)
		if serverName == "" {
			return "Language server exited unexpectedly.", nil
		}
		return fmt.Sprintf("Language server %s exited unexpectedly.", serverName), nil
	}

	failedServer := findFailedServer(m, file)
	if failedServer != nil {
		return fmt.Sprintf("Language server %s failed to start: %v.", failedServer.Name, failedServer.Err), nil
	}

	return nil, err
}

// findServerNameForFile returns the server name for the given file's extension,
// or an empty string if no server is configured.
func findServerNameForFile(m *Manager, file string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serverForExtension(file)
}

// findFailedServer scans ServerStates for an entry whose status is Failed and
// whose server handles the file's extension. Returns nil if none found.
func findFailedServer(m *Manager, file string) *ServerState {
	m.mu.Lock()
	serverName := m.serverForExtension(file)
	m.mu.Unlock()

	if serverName == "" {
		return nil
	}

	states := m.ServerStates()
	for i := range states {
		if states[i].Name == serverName && states[i].Status == ServerStatusFailed {
			return &states[i]
		}
	}

	return nil
}
