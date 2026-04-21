package repl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestReadlinePrompterWritesAppOutputToStream(t *testing.T) {
	var out bytes.Buffer
	prompter := &readlinePrompter{
		out: output.NewStream(&out),
	}

	prompter.Printf("status: %s", "ready")
	prompter.Println("assistant reply")

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
