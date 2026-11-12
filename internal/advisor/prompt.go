package advisor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

const (
	advisorSystemPrompt = "You are Steiner's internal advisor. Review the conversation and give concise strategic guidance for the main agent's next response. Do not call tools, do not invent tool results, and do not address the end user directly."
	advisorUserPrompt   = "Analyze the conversation above and return a short advisory note for the main agent. Focus on risks, missing reasoning, and the most useful next move."

	// inFlightLabel introduces the latest assistant message that issued the
	// advisor call still being served. That message has no tool result yet, so
	// it is rendered at the head of the uncached final user message instead of
	// the transcript. Keeping it out of the transcript makes the cacheable
	// prefix identical across consecutive advisor calls: once its advisor tool
	// result exists, the same message flattens differently (it gains a
	// [tool_call: advisor ...] line) and would invalidate the cached prefix.
	inFlightLabel = "The agent's latest message, still in progress (it issued this advisor call):\n"

	// maxToolArgStringLen bounds any single string value inside a flattened
	// tool call's arguments. Longer strings (e.g. mutate's whole-file
	// content) are truncated with a size-preserving elision marker so the
	// advisor payload stays bounded without depending on conversation
	// position or budget, which would break prompt caching.
	maxToolArgStringLen = 1000

	// Caller-supplied files and question are appended strictly after the
	// conversation snapshot (see buildMessages), so the cached prefix never
	// shifts when a caller passes different artifacts across calls. These
	// bounds are fixed constants, not derived from conversation state,
	// keeping the suffix itself bounded and predictable.
	maxAdvisorFiles           = 8         // max number of files a single call may pass
	maxAdvisorFileBytes       = 32 * 1024 // per-file cap before truncation
	maxAdvisorFilesTotalBytes = 96 * 1024 // aggregate cap across all files before truncation
	maxAdvisorQuestionBytes   = 4000      // question cap before truncation
)

// elide truncates s to a max-byte prefix plus a size-preserving elision
// marker when s exceeds max bytes; otherwise it returns s unchanged.
func elide(s string, max int) string {
	return elideKnownTotal(s, max, len(s))
}

// elideKnownTotal is like elide but takes the true total byte length
// separately from s. Used when s was already capped before this call (e.g.
// a file read through a limited reader), so len(s) alone would misreport
// the real size in the elision marker.
func elideKnownTotal(s string, max, total int) string {
	if total <= max {
		return s
	}
	if len(s) > max {
		s = s[:max]
	}
	return fmt.Sprintf("%s…[elided, %d bytes total]", s, total)
}

// capToolArgStrings returns a deep copy of v with any string longer than
// maxToolArgStringLen replaced by a truncated prefix plus an elision marker
// that records the original byte length. Maps and slices are recursed into;
// all other values are returned unchanged.
func capToolArgStrings(v any) any {
	switch val := v.(type) {
	case string:
		return elide(val, maxToolArgStringLen)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, elem := range val {
			out[k] = capToolArgStrings(elem)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = capToolArgStrings(elem)
		}
		return out
	default:
		return val
	}
}

// flattenToolMessages converts tool_use/tool_result messages to plain text so
// the advisor request requires no toolConfig on the provider side.
func flattenToolMessages(messages []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case provider.MessageRoleTool:
			content := m.Content
			if content == "" {
				content = "(empty)"
			}
			var msgContent string
			if m.Name == ToolName {
				msgContent = fmt.Sprintf("Your earlier note (update if circumstances have changed):\n%s", content)
			} else {
				msgContent = fmt.Sprintf("[tool_result: %s]\n%s", m.Name, content)
			}
			out = append(out, provider.Message{
				Role:    provider.MessageRoleUser,
				Content: msgContent,
			})
		case provider.MessageRoleAssistant:
			if len(m.ToolCalls) == 0 {
				m.ReasoningContent = ""
				m.ProviderMetadata = nil
				out = append(out, m)
				continue
			}
			var sb strings.Builder
			if m.Content != "" {
				sb.WriteString(m.Content)
				sb.WriteByte('\n')
			}
			for _, tc := range m.ToolCalls {
				argsJSON, err := json.Marshal(capToolArgStrings(tc.Arguments))
				if err != nil {
					argsJSON = []byte(fmt.Sprintf("%v", tc.Arguments))
				}
				fmt.Fprintf(&sb, "[tool_call: %s %s]", tc.Name, argsJSON)
				sb.WriteByte('\n')
			}
			out = append(out, provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: strings.TrimSuffix(sb.String(), "\n"),
			})
		default:
			m.ReasoningContent = ""
			m.ProviderMetadata = nil
			out = append(out, m)
		}
	}
	return out
}

// splitTrailingAssistant separates a trailing assistant message from the
// stable transcript before it. That message is the parent assistant turn still
// in flight while an advisor call runs, so callers render it separately.
func splitTrailingAssistant(messages []provider.Message) ([]provider.Message, *provider.Message) {
	if n := len(messages); n > 0 && messages[n-1].Role == provider.MessageRoleAssistant {
		inFlight := messages[n-1]
		return messages[:n-1], &inFlight
	}
	return messages, nil
}

// renderAdvisorFiles renders the caller-supplied files as labelled fenced
// code blocks, capping each file and the aggregate payload independently so
// the advisor suffix stays bounded regardless of how large the underlying
// files are.
func renderAdvisorFiles(files []advisorFile) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	writeAdvisorFiles(&sb, files)
	return sb.String()
}

// advisorFilesRenderedLen returns the exact byte length renderAdvisorFiles
// would produce without allocating the rendered payload.
func advisorFilesRenderedLen(files []advisorFile) int {
	if len(files) == 0 {
		return 0
	}
	var counter countingWriter
	writeAdvisorFiles(&counter, files)
	return counter.n
}

// countingWriter discards written bytes and counts them.
type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

// writeAdvisorFiles writes the rendered files through w so rendering and
// length probing share the exact formatting, caps, and elision logic. Both
// sinks are in-memory and cannot fail, so write errors are intentionally
// ignored.
func writeAdvisorFiles(w io.Writer, files []advisorFile) {
	remaining := maxAdvisorFilesTotalBytes
	for _, f := range files {
		if f.Deduped {
			_, _ = fmt.Fprintf(w, "File: %s (already fully present above from an earlier read in this conversation; content unchanged)\n\n", f.DisplayPath)
			continue
		}
		content := elideKnownTotal(f.Content, maxAdvisorFileBytes, f.TotalBytes)
		if len(content) > remaining {
			content = elide(content, remaining)
		}
		remaining -= len(content)
		if remaining < 0 {
			remaining = 0
		}
		_, _ = fmt.Fprintf(w, "File: %s\n```\n%s\n```\n\n", f.DisplayPath, content)
	}
}

// buildMessages assembles the advisor request. The conversation snapshot is
// the cached prefix; the in-flight assistant message text, the
// caller-supplied files and question (each capped by the maxAdvisor*
// constants), and the advisor user prompt form a suffix appended strictly
// after it so the prefix stays position-stable across calls regardless of
// what artifacts a caller passes.
func buildMessages(snapshot []provider.Message, question string, files []advisorFile) []provider.Message {
	transcript, inFlightMessage := splitTrailingAssistant(flattenToolMessages(snapshot))

	// The in-flight assistant message text moves to the final user message so
	// the cached prefix ends on a message whose rendering never changes
	// between advisor calls.
	var inFlight string
	if inFlightMessage != nil {
		inFlight = strings.TrimSpace(inFlightMessage.Content)
	}

	messages := make([]provider.Message, 0, len(transcript)+2)
	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleSystem,
		Content: advisorSystemPrompt,
	})
	messages = append(messages, provider.CloneMessages(transcript)...)

	var suffix strings.Builder
	if inFlight != "" {
		suffix.WriteString(inFlightLabel)
		suffix.WriteString(inFlight)
		suffix.WriteString("\n\n")
	}
	suffix.WriteString(renderAdvisorFiles(files))
	if q := strings.TrimSpace(question); q != "" {
		suffix.WriteString(elide(q, maxAdvisorQuestionBytes))
		suffix.WriteString("\n\n")
	}
	suffix.WriteString(advisorUserPrompt)

	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleUser,
		Content: suffix.String(),
	})
	return messages
}
