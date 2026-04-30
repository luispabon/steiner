package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/prefs"
)

type ApprovalDecision string

const (
	ApprovalDecisionAllowOnce   ApprovalDecision = "allow_once"
	ApprovalDecisionAlwaysAllow ApprovalDecision = "always_allow"
	ApprovalDecisionDeny        ApprovalDecision = "deny"
)

type ApprovalSubmission struct {
	Tool     string
	Mode     string
	Decision ApprovalDecision
}

type Config struct {
	Model            string
	ModelNames       []string
	ModelContexts    map[string]int
	ProviderBaseURL  string
	HomeDir          string
	WorkingDir       string
	MaxTurns         int
	SkillNames       []string
	Theme            string
	AccentPreset     string
	ShowThinking     bool
	OnSubmit         func(string)
	OnContextInspect func()
	OnConfigInspect  func()
	OnApproval       func(ApprovalSubmission)
	OnInterrupt      func()
	OnExitRequested  func()
	OnSkillToggle    func(string, bool)
	OnModelSwitch    func(string) (string, bool)
	OnClear          func()
	OnCompact        func()
}

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
	return &App{
		cfg:    cfg,
		bridge: newEventBridge(256),
	}
}

func (a *App) Subscriber() output.Subscriber {
	if a == nil {
		return noopSubscriber{}
	}
	return a.bridge
}

func (a *App) EventSink() output.EventSink {
	return a.bridge
}

func (a *App) NewProgram(options ...tea.ProgramOption) *tea.Program {
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
	opts = append(opts, options...)
	return tea.NewProgram(newModel(a.cfg, a.bridge.Messages()), opts...)
}

func (a *App) Run(options ...tea.ProgramOption) error {
	_, err := a.NewProgram(options...).Run()
	return err
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
	msg := runtimeEventMsg{Event: event}
	select {
	case b.ch <- msg:
	default:
		go func() {
			b.ch <- msg
		}()
	}
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
