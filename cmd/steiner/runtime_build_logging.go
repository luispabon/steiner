package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func buildRuntimeEventSink(cfg config.Config, cmd *cobra.Command, flags *cliFlags) (output.EventSink, func() error, error) {
	events := output.EventSink(output.NoopSink{})
	if flags.exec {
		events = output.EventSink(output.NewStream(cmd.OutOrStdout()))
	}
	logFile := runtimeLogFile(cfg, flags)

	slogWriter, slogCloser, err := runtimeSlogWriter(logFile)
	if err != nil {
		return nil, nil, err
	}
	output.ConfigureLogger(slogWriter, cfg.Logging.Level)

	if strings.TrimSpace(logFile) == "" {
		return events, slogCloser, nil
	}
	fileSink, err := output.NewFileLogSink(logFile, output.FileLogOptions{
		ThinkingChunk:           cfg.Logging.ThinkingChunk,
		AssistantChunk:          cfg.Logging.AssistantChunk,
		BuildSHA:                commit,
		Dirty:                   buildDirty(),
		Version:                 version,
		CaptureAPIRequestBodies: cfg.Diagnostics.CaptureBodies,
	})
	if err != nil {
		return nil, nil, err
	}
	return output.NewMultiSink(events, fileSink), joinClosers(slogCloser, fileSink.Close), nil
}

// runtimeSlogWriter picks the destination for the process-wide slog handler:
// a sibling *.slog file derived from logFile when one is configured, or
// io.Discard otherwise. slog must never write to os.Stderr while the TUI is
// live, the same constraint documented on selectMCPStderr.
func runtimeSlogWriter(logFile string) (io.Writer, func() error, error) {
	logFile = strings.TrimSpace(logFile)
	if logFile == "" {
		return io.Discard, nil, nil
	}
	path := output.SlogPath(logFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create slog directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open slog file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("secure slog file: %w", err)
	}
	return f, f.Close, nil
}

func runtimeLogFile(cfg config.Config, flags *cliFlags) string {
	if flags.logFile != "" {
		return flags.logFile
	}
	if cfg.Logging.Enabled {
		return cfg.Logging.File
	}
	return ""
}

func runtimeCompactionLogFile(cfg config.Config, flags *cliFlags) string {
	if flags.compactionLogFile != "" {
		return flags.compactionLogFile
	}
	if cfg.Logging.CompactionLogFile != "" {
		return cfg.Logging.CompactionLogFile
	}
	return ""
}

func buildDelegationLogger(cfg config.Config, flags *cliFlags, diag *diagnostics.Writer) (*delegation.TraceLogger, error) {
	logPath := delegation.LogPath(runtimeLogFile(cfg, flags))
	return delegation.NewTraceLoggerWithDiagnostics(logPath, streamWriter(diag, diagnostics.KindTool))
}

func buildStreamErrorLogger(cfg config.Config, flags *cliFlags, diag *diagnostics.Writer) (*provider.StreamErrorLogger, error) {
	path := provider.StreamErrorLogPath(runtimeLogFile(cfg, flags))
	l, err := provider.NewStreamErrorLoggerWithDiagnostics(path, streamWriter(diag, diagnostics.KindProvider))
	if err != nil {
		return nil, fmt.Errorf("stream error logger: %w", err)
	}
	return l, nil
}

// buildRuntimeDiagnostics constructs the process diagnostics writer. Nothing
// is constructed when diagnostics are disabled: a nil *diagnostics.Writer is a
// no-op for every caller.
func buildRuntimeDiagnostics(cfg config.Config) (*diagnostics.Writer, error) {
	if !cfg.Diagnostics.Enabled {
		return nil, nil
	}
	w, err := diagnostics.New(diagnostics.Options{
		Dir:           cfg.Diagnostics.Dir,
		RetentionDays: cfg.Diagnostics.RetentionDays,
		Streams: diagnostics.Streams{
			Cache:    cfg.Diagnostics.Streams.Cache,
			Provider: cfg.Diagnostics.Streams.Provider,
			Tool:     cfg.Diagnostics.Streams.Tool,
		},
		CaptureBodies: cfg.Diagnostics.CaptureBodies,
		BuildSHA:      commit,
		Dirty:         buildDirty(),
	})
	if err != nil {
		return nil, fmt.Errorf("build diagnostics writer: %w", err)
	}
	return w, nil
}

// streamWriter returns diag only when kind's stream is enabled, so a sink
// reparented onto diagnostics (stream error log, delegation trace log) keeps
// writing to its own file when the stream it would feed is off.
func streamWriter(diag *diagnostics.Writer, kind diagnostics.Kind) *diagnostics.Writer {
	if !diag.Enabled(kind) {
		return nil
	}
	return diag
}

// buildDirty reports whether the running binary was built from a dirty working
// tree. The linker can only set strings, so main.dirty is a string.
func buildDirty() bool {
	return dirty == "true"
}
