package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/prefs"
)

// ApprovalDecision is the selected outcome from an approval prompt.
type ApprovalDecision string

const (
	// ApprovalDecisionAllowOnce approves the tool call once.
	ApprovalDecisionAllowOnce ApprovalDecision = "allow_once"
	// ApprovalDecisionAlwaysAllow approves the tool call and persists the choice.
	ApprovalDecisionAlwaysAllow ApprovalDecision = "always_allow"
	// ApprovalDecisionDeny rejects the tool call.
	ApprovalDecisionDeny ApprovalDecision = "deny"
)

// ApprovalSubmission describes a submitted approval response.
type ApprovalSubmission struct {
	Tool     string
	Mode     string
	Decision ApprovalDecision
}

// Config holds the runtime configuration for the TUI application.
type Config struct {
	Model             string
	ModelNames        []string
	ModelContexts     map[string]int
	ModelBaseURLs     map[string]string
	ProviderBaseURL   string
	HomeDir           string
	WorkingDir        string
	MaxTurns          int
	SkillNames        []string
	SkillDescriptions map[string]string // skill name -> short summary
	SkillSources      map[string]string // skill name -> "project"/"user"/"global"
	Theme             string
	AccentPreset      string
	ShowThinking      bool
	SidebarPosition   string
	Version           string
	Controller        interactive.Controller
	SessionStore      SessionLister
}

// App wires the TUI runtime and event bridge.
type App struct {
	cfg    Config
	bridge *eventBridge
}

// NewApp creates a new TUI application with the given configuration.
func NewApp(cfg Config) *App {
	// Load prefs; non-fatal on error
	p, err := prefs.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "steiner: failed to load prefs: %v\n", err)
	}
	if cfg.AccentPreset == "" {
		cfg.AccentPreset = p.Accent
	}
	// ShowThinking defaults to true; only override if prefs explicitly sets false
	if !cfg.ShowThinking {
		cfg.ShowThinking = p.ShowThinking
	}
	if cfg.SidebarPosition == "" {
		cfg.SidebarPosition = p.SidebarPosition
	}
	return &App{
		cfg:    cfg,
		bridge: newEventBridge(256),
	}
}

// Subscriber returns the event subscriber exposed to the runtime.
func (a *App) Subscriber() output.Subscriber {
	if a == nil {
		return noopSubscriber{}
	}
	return a.bridge
}

// EventSink returns the event sink used by the runtime and TUI bridge.
func (a *App) EventSink() output.EventSink {
	return a.bridge
}

// NewProgram constructs the Bubble Tea program for the TUI.
func (a *App) NewProgram(options ...tea.ProgramOption) *tea.Program {
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
	opts = append(opts, options...)
	return tea.NewProgram(newModel(a.cfg, a.bridge.Messages()), opts...)
}

// Run starts the TUI program and waits for it to exit.
func (a *App) Run(options ...tea.ProgramOption) error {
	defer a.Cleanup()
	_, err := a.NewProgram(options...).Run()
	return err
}

// Cleanup disables terminal mode 1000 (normal mouse tracking) which Init enables
// but bubbletea does not restore on exit (it only cleans up mode 1002/1003).
// Call this after the bubbletea program has fully exited.
func (a *App) Cleanup() {
	_, _ = os.Stdout.WriteString("\x1b[?1000l")
}

type runtimeEventMsg struct {
	Event output.Event
}

type bridgeClosedMsg struct{}

type eventBridge struct {
	ch chan tea.Msg
}

func (b *eventBridge) Emit(event output.Event) {
	if b == nil {
		return
	}
	b.ch <- runtimeEventMsg{Event: event}
}

type noopSubscriber struct{}

func (noopSubscriber) OnEvent(output.Event) {}

func newEventBridge(buffer int) *eventBridge {
	if buffer < 1 {
		buffer = 1
	}
	return &eventBridge{ch: make(chan tea.Msg, buffer)}
}

func (b *eventBridge) Messages() <-chan tea.Msg {
	if b == nil {
		return nil
	}
	return b.ch
}

func (b *eventBridge) OnEvent(event output.Event) {
	b.Emit(event)
}
