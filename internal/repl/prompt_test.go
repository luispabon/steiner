package repl

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/nyaosorg/go-readline-ny"
)

func TestLinePrompterWritesAppOutputToStream(t *testing.T) {
	var out bytes.Buffer
	prompter := &linePrompter{
		out: output.NewStream(&out),
	}

	prompter.Printf(output.ChannelStatus, "status: %s", "ready")
	prompter.Println(output.ChannelAssistant, "assistant reply")

	got := out.String()
	for _, want := range []string{
		"status: ready",
		"assistant reply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	}
}

func TestIsPromptInterruptedMatchesReadlineCtrlC(t *testing.T) {
	if !IsPromptInterrupted(readline.CtrlC) {
		t.Fatal("IsPromptInterrupted(readline.CtrlC) = false, want true")
	}
	if IsPromptInterrupted(errors.New("other")) {
		t.Fatal("IsPromptInterrupted(other) = true, want false")
	}
}
