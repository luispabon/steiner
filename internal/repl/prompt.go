package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/reeflective/readline"
)

type Prompter interface {
	ReadLine(context.Context) (string, error)
	Printf(output.Channel, string, ...any)
	Println(output.Channel, ...any)
}

func NewPrompter(in io.Reader, out *output.Stream, completer Completer) Prompter {
	return newPrompter(in, out, completer)
}

func NewPromptEventSink(prompter Prompter) output.EventSink {
	return output.SinkFunc(func(event output.Event) {
		if prompter == nil {
			return
		}
		channel := channelForEvent(event)
		prompter.Println(channel, output.FormatEvent(event))
	})
}

func channelForEvent(event output.Event) output.Channel {
	switch event.Type {
	case output.EventTypeToolCallStarted, output.EventTypeToolCallFinished:
		return output.ChannelTool
	case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied, output.EventTypeApprovalRequested:
		return output.ChannelApproval
	default:
		return output.ChannelStatus
	}
}

func newPrompter(in io.Reader, out *output.Stream, completer Completer) Prompter {
	if file, ok := in.(*os.File); ok && file == os.Stdin {
		return newReadlinePrompter(completer, out)
	}
	return &linePrompter{
		reader: asBufioReader(in),
		out:    out,
	}
}

type linePrompter struct {
	reader *bufio.Reader
	out    *output.Stream
}

func (p *linePrompter) ReadLine(ctx context.Context) (string, error) {
	_ = ctx
	if p == nil || p.reader == nil {
		return "", io.EOF
	}
	if p.out != nil {
		p.out.Printf("> ")
	}
	line, err := p.reader.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err == io.EOF && line != "" {
		return line, nil
	}
	return line, err
}

func (p *linePrompter) Printf(_ output.Channel, format string, args ...any) {
	if p != nil && p.out != nil {
		p.out.Printf(format, args...)
	}
}

func (p *linePrompter) Println(_ output.Channel, args ...any) {
	if p != nil && p.out != nil {
		p.out.Println(args...)
	}
}

type readlinePrompter struct {
	shell *readline.Shell
	out   *output.Stream
}

func newReadlinePrompter(completer Completer, out *output.Stream) Prompter {
	shell := readline.NewShell()
	shell.Prompt.Primary(func() string { return "> " })
	shell.Completer = func(line []rune, cursor int) readline.Completions {
		return completeWithReadline(completer, line, cursor)
	}
	return &readlinePrompter{
		shell: shell,
		out:   out,
	}
}

func (p *readlinePrompter) ReadLine(ctx context.Context) (string, error) {
	_ = ctx
	if p == nil || p.shell == nil {
		return "", io.EOF
	}
	line, err := p.shell.Readline()
	return strings.TrimRight(line, "\r\n"), err
}

func (p *readlinePrompter) Printf(channel output.Channel, format string, args ...any) {
	if p == nil || p.shell == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.TrimRight(message, "\n")

	if p.out != nil {
		message = p.out.Themed(channel, message)
	}

	p.shell.Printf("%s\n", message)
}

func (p *readlinePrompter) Println(channel output.Channel, args ...any) {
	if p == nil || p.shell == nil {
		return
	}
	message := strings.TrimRight(fmt.Sprintln(args...), "\n")

	if p.out != nil {
		message = p.out.Themed(channel, message)
	}

	p.shell.Printf("%s\n", message)
}

func completeWithReadline(completer Completer, line []rune, cursor int) readline.Completions {
	prefix := completionPrefix(line, cursor)
	matches := completer.Complete(prefix)
	if len(matches) == 0 {
		return readline.Completions{}
	}
	return readline.CompleteValues(matches...).NoSpace()
}

func completionPrefix(line []rune, cursor int) string {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}
	text := string(line[:cursor])
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	if idx := strings.IndexAny(text, " \t"); idx >= 0 {
		text = text[:idx]
	}
	return text
}

func asBufioReader(in io.Reader) *bufio.Reader {
	if reader, ok := in.(*bufio.Reader); ok {
		return reader
	}
	return bufio.NewReader(in)
}
