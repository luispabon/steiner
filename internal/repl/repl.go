package repl

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

type Runner interface {
	Run(ctx context.Context, conversation []agent.Message, skillNames []string) (RunResult, error)
}

type RunResult struct {
	Conversation []agent.Message
	Reply        string
	Diagnostics  []output.Event
}

type Session struct {
	Runner       Runner
	In           io.Reader
	Out          *output.Stream
	Events       output.EventSink
	ToolNames    []string
	SkillNames   []string
	ActiveSkills []string
	Conversation []agent.Message
	Diagnostics  []output.Event
	Completer    Completer
	prompt       Prompter
}

func NewSession(runner Runner, in io.Reader, out *output.Stream, events output.EventSink, toolNames, skillNames []string) *Session {
	return &Session{
		Runner:       runner,
		In:           in,
		Out:          out,
		Events:       events,
		ToolNames:    append([]string(nil), toolNames...),
		SkillNames:   append([]string(nil), skillNames...),
		Completer:    Completer{Commands: BuiltinCommands(), Skills: append([]string(nil), skillNames...)},
		ActiveSkills: nil,
	}
}

func (s *Session) SetPrompter(prompt Prompter) {
	if s != nil {
		s.prompt = prompt
	}
}

func (s *Session) Run(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.prompt == nil {
		if s.In == nil {
			return fmt.Errorf("input is required")
		}
		s.prompt = newPrompter(s.In, s.Out, s.Completer)
	}
	if s.prompt == nil {
		return fmt.Errorf("input is required")
	}
	for {
		line, err := s.prompt.ReadLine(ctx)
		if err != nil && err != io.EOF {
			return err
		}
		if err == io.EOF && strings.TrimSpace(line) == "" {
			return nil
		}
		done, err := s.HandleLine(ctx, line)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (s *Session) HandleLine(ctx context.Context, line string) (bool, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return false, nil
	}

	if command, ok := ParseCommand(line); ok {
		return s.handleCommand(command)
	}

	if s.Runner == nil {
		return false, fmt.Errorf("runner is required")
	}

	user := agent.Message{Role: agent.MessageRoleUser, Content: line}
	if s.Events != nil {
		s.Events.Emit(output.NewUserInputEvent(line, "interactive"))
	}
	conversation := cloneMessages(s.Conversation)
	conversation = append(conversation, user)

	result, err := s.Runner.Run(ctx, conversation, append([]string(nil), s.ActiveSkills...))
	if err != nil {
		return false, err
	}

	if len(result.Conversation) > 0 {
		s.Conversation = cloneMessages(result.Conversation)
	} else {
		s.Conversation = conversation
	}
	s.Diagnostics = cloneEvents(result.Diagnostics)

	reply := strings.TrimSpace(result.Reply)
	if reply != "" {
		s.Println(output.ChannelAssistant, reply)
	}

	return false, nil
}

func (s *Session) handleCommand(command Command) (bool, error) {
	switch command.Name {
	case "help":
		s.printHelp()
	case "tools":
		s.printTools()
	case "skills":
		s.printSkills()
	case "history":
		s.printHistory()
	case "clear":
		s.Conversation = nil
		s.Diagnostics = nil
		s.Println(output.ChannelStatus, "conversation cleared")
	case "exit":
		return true, nil
	default:
		if s.toggleSkill(command.Name) {
			return false, nil
		}
		s.Printf(output.ChannelError, "unknown command: /%s\n", command.Name)
	}
	return false, nil
}

func (s *Session) Println(channel output.Channel, args ...any) {
	if s != nil && s.prompt != nil {
		s.prompt.Println(channel, args...)
		return
	}
	if s != nil && s.Out != nil {
		s.Out.Println(args...)
	}
}

func (s *Session) Printf(channel output.Channel, format string, args ...any) {
	if s != nil && s.prompt != nil {
		s.prompt.Printf(channel, format, args...)
		return
	}
	if s != nil && s.Out != nil {
		s.Out.Printf(format, args...)
	}
}

func (s *Session) printHelp() {
	s.Println(output.ChannelStatus, "commands:")
	for _, name := range BuiltinCommands() {
		s.Printf(output.ChannelStatus, "  /%s\n", name)
	}
	if len(s.SkillNames) > 0 {
		s.Println(output.ChannelStatus, "skills:")
		for _, name := range s.SkillNames {
			s.Printf(output.ChannelStatus, "  /%s\n", name)
		}
	}
}

func (s *Session) printTools() {
	if len(s.ToolNames) == 0 {
		s.Println(output.ChannelStatus, "no tools configured")
		return
	}
	s.Println(output.ChannelStatus, "tools:")
	for _, name := range s.ToolNames {
		s.Printf(output.ChannelStatus, "  %s\n", name)
	}
}

func (s *Session) printSkills() {
	if len(s.SkillNames) == 0 {
		s.Println(output.ChannelStatus, "no skills available")
		return
	}
	s.Println(output.ChannelStatus, "skills:")
	for _, name := range s.SkillNames {
		enabled := ""
		if containsString(s.ActiveSkills, name) {
			enabled = " [active]"
		}
		s.Printf(output.ChannelStatus, "  %s%s\n", name, enabled)
	}
}

func (s *Session) printHistory() {
	s.Printf(output.ChannelStatus, "history: conversation_messages=%d diagnostics=%d\n", len(s.Conversation), len(s.Diagnostics))
	if len(s.Diagnostics) == 0 {
		s.Println(output.ChannelStatus, "no context diagnostics recorded")
		return
	}

	s.Println(output.ChannelStatus, "context diagnostics:")
	start := 0
	if len(s.Diagnostics) > 5 {
		start = len(s.Diagnostics) - 5
		s.Printf(output.ChannelStatus, "showing latest %d of %d\n", len(s.Diagnostics)-start, len(s.Diagnostics))
	}
	for _, event := range s.Diagnostics[start:] {
		s.Printf(output.ChannelStatus, "  %s\n", output.FormatEvent(event))
	}
}

func (s *Session) toggleSkill(name string) bool {
	if !containsString(s.SkillNames, name) {
		return false
	}
	if idx := indexOfString(s.ActiveSkills, name); idx >= 0 {
		s.ActiveSkills = append(s.ActiveSkills[:idx], s.ActiveSkills[idx+1:]...)
		s.Printf(output.ChannelStatus, "skill disabled: %s\n", name)
		return true
	}
	s.ActiveSkills = append(s.ActiveSkills, name)
	s.Printf(output.ChannelStatus, "skill enabled: %s\n", name)
	return true
}

func cloneMessages(messages []agent.Message) []agent.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]agent.Message, len(messages))
	copy(out, messages)
	for i := range out {
		if len(out[i].ToolCalls) == 0 {
			continue
		}
		calls := make([]agent.ToolCall, len(out[i].ToolCalls))
		copy(calls, out[i].ToolCalls)
		for j := range calls {
			calls[j].Arguments = cloneInput(calls[j].Arguments)
		}
		out[i].ToolCalls = calls
	}
	return out
}

func cloneEvents(events []output.Event) []output.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]output.Event, len(events))
	copy(out, events)
	return out
}

func cloneInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneInput(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	default:
		return value
	}
}

func containsString(values []string, want string) bool {
	return indexOfString(values, want) >= 0
}

func indexOfString(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
