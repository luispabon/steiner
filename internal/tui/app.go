package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/output"
)

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
	OnSubmit         func(string)
	OnContextInspect func()
	OnApproval       func(bool)
	OnSkillToggle    func(string, bool)
	OnModelSwitch    func(string)
}

type App struct {
	cfg    Config
	bridge *eventBridge
}

func NewApp(cfg Config) *App {
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
	opts := []tea.ProgramOption{tea.WithMouseCellMotion()}
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
