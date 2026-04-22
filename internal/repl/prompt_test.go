package repl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
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
