package advisor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

const (
	advisorSystemPrompt = "You are Steiner's internal advisor. Review the conversation and give concise strategic guidance for the main agent's next response. Do not call tools, do not invent tool results, and do not address the end user directly."
	advisorUserPrompt   = "Analyze the conversation above and return a short advisory note for the main agent. Focus on risks, missing reasoning, and the most useful next move."

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

// renderAdvisorFiles renders the caller-supplied files as labelled fenced
// code blocks, capping each file and the aggregate payload independently so
// the advisor suffix stays bounded regardless of how large the underlying
// files are.
func renderAdvisorFiles(files []advisorFile) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	remaining := maxAdvisorFilesTotalBytes
	for _, f := range files {
		content := elideKnownTotal(f.Content, maxAdvisorFileBytes, f.TotalBytes)
		if len(content) > remaining {
			content = elide(content, remaining)
		}
		remaining -= len(content)
		if remaining < 0 {
			remaining = 0
		}
		fmt.Fprintf(&sb, "File: %s\n```\n%s\n```\n\n", f.DisplayPath, content)
	}
	return sb.String()
}

// buildMessages assembles the advisor request. The conversation snapshot is
// the cached prefix; caller-supplied files and question form a bounded
// suffix appended strictly after it so the prefix stays position-stable
// across calls regardless of what artifacts a caller passes.
func buildMessages(snapshot []provider.Message, question string, files []advisorFile) []provider.Message {
	messages := make([]provider.Message, 0, len(snapshot)+2)
	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleSystem,
		Content: advisorSystemPrompt,
	})
	messages = append(messages, provider.CloneMessages(flattenToolMessages(snapshot))...)

	var suffix strings.Builder
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
