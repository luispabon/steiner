package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/nyaosorg/go-readline-ny"
	"github.com/nyaosorg/go-readline-ny/completion"
	"github.com/nyaosorg/go-readline-ny/keys"
)

var ErrPromptInterrupted = errors.New("prompt interrupted")

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
	editor    *readline.Editor
	completer Completer
	out       *output.Stream
}

func newReadlinePrompter(completer Completer, out *output.Stream) Prompter {
	editor := &readline.Editor{
		PromptWriter: func(w io.Writer) (int, error) {
			return io.WriteString(w, "> ")
		},
		Writer: os.Stdout,
	}

	if len(completer.Commands) > 0 || len(completer.Skills) > 0 {
		editor.BindKey(keys.CtrlI, &completion.CmdCompletionOrList2{
			Delimiter: " ",
			Enclosure: "",
			Postfix:   "",
			Candidates: func(field []string) (c, d []string) {
				prefix := completionPrefixForNy(field)
				matches := completer.Complete(prefix)
				if len(matches) == 0 {
					return nil, nil
				}
				return matches, matches
			},
		})
	}

	return &readlinePrompter{
		editor:    editor,
		completer: completer,
		out:       out,
	}
}

func (p *readlinePrompter) ReadLine(ctx context.Context) (string, error) {
	if p == nil || p.editor == nil {
		return "", io.EOF
	}
	line, err := p.editor.ReadLine(ctx)
	if errors.Is(err, readline.CtrlC) {
		return "", ErrPromptInterrupted
	}
	return strings.TrimRight(line, "\r\n"), err
}

func (p *readlinePrompter) Printf(channel output.Channel, format string, args ...any) {
	if p == nil || p.out == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.TrimRight(message, "\n")
	message = p.out.Themed(channel, message)
	fmt.Fprintln(os.Stdout, message)
}

func (p *readlinePrompter) Println(channel output.Channel, args ...any) {
	if p == nil || p.out == nil {
		return
	}
	message := strings.TrimRight(fmt.Sprintln(args...), "\n")
	message = p.out.Themed(channel, message)
	fmt.Fprintln(os.Stdout, message)
}

func completionPrefixForNy(field []string) string {
	if len(field) == 0 {
		return ""
	}
	text := field[len(field)-1]
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	if idx := strings.IndexAny(text, " \t"); idx >= 0 {
		text = text[:idx]
	}
	return text
}

func CompletionPrefix(line []rune, cursor int) string {
	return completionPrefixForNy([]string{string(line[:cursor])})
}

func asBufioReader(in io.Reader) *bufio.Reader {
	if reader, ok := in.(*bufio.Reader); ok {
		return reader
	}
	return bufio.NewReader(in)
}

func IsPromptInterrupted(err error) bool {
	return errors.Is(err, ErrPromptInterrupted) || errors.Is(err, readline.CtrlC)
}
