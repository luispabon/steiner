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
	Printf(string, ...any)
	Println(...any)
}

func NewPrompter(in io.Reader, out *output.Stream, completer Completer) Prompter {
	return newPrompter(in, out, completer)
}

func NewPromptEventSink(prompter Prompter) output.EventSink {
	return output.SinkFunc(func(event output.Event) {
		if prompter == nil {
			return
		}
		prompter.Println(output.FormatEvent(event))
	})
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

func (p *linePrompter) Printf(format string, args ...any) {
	if p != nil && p.out != nil {
		p.out.Printf(format, args...)
	}
}

func (p *linePrompter) Println(args ...any) {
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

func (p *readlinePrompter) Printf(format string, args ...any) {
	if p == nil {
		return
	}
	message := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	if p.out != nil {
		p.out.Printf("%s\n", message)
	}
	p.refreshPrompt()
}

func (p *readlinePrompter) Println(args ...any) {
	if p == nil {
		return
	}
	message := strings.TrimRight(fmt.Sprintln(args...), "\n")
	if p.out != nil {
		p.out.Println(message)
	}
	p.refreshPrompt()
}

func (p *readlinePrompter) refreshPrompt() {
	if p == nil || p.shell == nil || p.shell.Display == nil {
		return
	}
	p.shell.Display.RefreshTransient()
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
