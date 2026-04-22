package repl

import (
	"context"
	"fmt"
	"io"
	"strconv"
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
		s.printHistory(command.Args)
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

func (s *Session) printHistory(args []string) {
	mode, limit, err := parseHistoryArgs(args)
	if err != nil {
		s.Printf(output.ChannelError, "history: %v\n", err)
		s.Println(output.ChannelStatus, "usage: /history [summary|context|recent [count]]")
		return
	}

	snapshot := output.SummarizeInspection(s.Diagnostics, limit)
	s.Printf(
		output.ChannelStatus,
		"history: conversation_messages=%d diagnostics=%d context_diagnostics=%d\n",
		len(s.Conversation),
		snapshot.TotalDiagnostics,
		snapshot.ContextDiagnostics,
	)

	if snapshot.TotalDiagnostics == 0 {
		s.Println(output.ChannelStatus, "no session diagnostics recorded")
		return
	}

	switch mode {
	case "summary":
		s.printHistorySummary(snapshot)
	case "context":
		s.printHistoryContext(snapshot, limit)
	case "recent":
		s.printHistoryRecent(snapshot.Recent, limit, snapshot.TotalDiagnostics, "recent diagnostics")
	}
}

func (s *Session) printHistorySummary(snapshot output.InspectionSnapshot) {
	if snapshot.LastStopReason != "" {
		s.Printf(output.ChannelStatus, "last stop: %s\n", snapshot.LastStopReason)
	}
	if snapshot.LastBudget != "" {
		s.Printf(output.ChannelStatus, "context fullness: %s\n", snapshot.LastBudget)
	}
	if snapshot.LastCompaction != "" {
		s.Printf(output.ChannelStatus, "recent compaction: %s\n", snapshot.LastCompaction)
	}
	if len(snapshot.Recent) == 0 {
		return
	}
	s.printHistoryRecent(snapshot.Recent, len(snapshot.Recent), snapshot.TotalDiagnostics, "recent diagnostics")
}

func (s *Session) printHistoryContext(snapshot output.InspectionSnapshot, limit int) {
	if snapshot.ContextDiagnostics == 0 {
		s.Println(output.ChannelStatus, "no context diagnostics recorded")
		return
	}
	if snapshot.LastBudget != "" {
		s.Printf(output.ChannelStatus, "context fullness: %s\n", snapshot.LastBudget)
	}
	if snapshot.LastCompaction != "" {
		s.Printf(output.ChannelStatus, "recent compaction: %s\n", snapshot.LastCompaction)
	}
	s.printHistoryRecent(snapshot.RecentContext, limit, snapshot.ContextDiagnostics, "recent context diagnostics")
}

func (s *Session) printHistoryRecent(lines []string, limit, total int, heading string) {
	if len(lines) == 0 {
		s.Printf(output.ChannelStatus, "%s: none\n", heading)
		return
	}
	if total > len(lines) && limit > 0 {
		s.Printf(output.ChannelStatus, "%s: showing latest %d of %d\n", heading, len(lines), total)
	} else {
		s.Printf(output.ChannelStatus, "%s:\n", heading)
	}
	for _, line := range lines {
		s.Printf(output.ChannelStatus, "  %s\n", line)
	}
}

func parseHistoryArgs(args []string) (mode string, limit int, err error) {
	mode = "summary"
	limit = 3
	if len(args) == 0 {
		return mode, limit, nil
	}

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "", "summary":
		mode = "summary"
	case "context":
		mode = "context"
	case "recent":
		mode = "recent"
		limit = 5
	default:
		return "", 0, fmt.Errorf("unknown view %q", args[0])
	}

	if len(args) == 1 {
		return mode, limit, nil
	}
	if mode != "recent" {
		return "", 0, fmt.Errorf("%s view does not take extra arguments", mode)
	}

	parsed, parseErr := strconv.Atoi(args[1])
	if parseErr != nil || parsed <= 0 {
		return "", 0, fmt.Errorf("recent count must be a positive integer")
	}
	if parsed > 10 {
		parsed = 10
	}
	return mode, parsed, nil
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
