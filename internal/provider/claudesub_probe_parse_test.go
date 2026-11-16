//go:build claudesub_probe

package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// claudeSubProbeInitExtras are the init event's load-reporting fields the probe
// records by name. The CLI's init event names what it loaded (plugins, MCP
// servers, slash commands, agents, skills and hooks), which is the only evidence
// of what a user configuration contributes to a steiner process.
var claudeSubProbeInitExtras = []string{
	"plugins",
	"mcp_servers",
	"slash_commands",
	"agents",
	"skills",
}

// claudeSubProbeNames is one init field reduced to the names it lists.
type claudeSubProbeNames struct {
	Present bool     `json:"present"`
	Names   []string `json:"names,omitempty"`
}

// claudeSubProbeInit is what the CLI's init event reports about the process.
type claudeSubProbeInit struct {
	AtMS           int64                          `json:"at_ms"`
	SessionID      string                         `json:"session_id,omitempty"`
	Model          string                         `json:"model,omitempty"`
	Tools          []string                       `json:"tools"`
	Capabilities   json.RawMessage                `json:"capabilities,omitempty"`
	CLIVersion     string                         `json:"cli_version,omitempty"`
	PermissionMode string                         `json:"permission_mode,omitempty"`
	Extras         map[string]claudeSubProbeNames `json:"extras,omitempty"`
}

// claudeSubProbeParseInbound reduces one stdout event to a recorded entry.
func claudeSubProbeParseInbound(ev claudeSubEvent, at int64) claudeSubProbeEntry {
	entry := claudeSubProbeEntry{AtMS: at, Dir: "in", Type: ev.Type, Subtype: ev.Subtype, UUID: ev.UUID, SessionID: ev.SessionID}
	var payload struct {
		ParentToolUseID *string              `json:"parent_tool_use_id"`
		TerminalReason  string               `json:"terminal_reason"`
		StopReason      string               `json:"stop_reason"`
		IsError         *bool                `json:"is_error"`
		Result          string               `json:"result"`
		Status          string               `json:"status"`
		Usage           *claudeSubProbeUsage `json:"usage"`
		RequestID       string               `json:"request_id"`
		Request         *struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Response *struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
			Error     string          `json:"error"`
		} `json:"response"`
		Event *struct {
			Type  string `json:"type"`
			Delta *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content_block"`
			Message *struct {
				Usage *claudeSubProbeUsage `json:"usage"`
			} `json:"message"`
			Usage *claudeSubProbeUsage `json:"usage"`
		} `json:"event"`
		Message *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				ID   string `json:"id"`
			} `json:"content"`
			Usage *claudeSubProbeUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(ev.Raw, &payload); err != nil {
		return entry
	}
	entry.ParentToolUseID = payload.ParentToolUseID
	entry.TerminalReason = payload.TerminalReason
	entry.StopReason = payload.StopReason
	entry.IsError = payload.IsError
	entry.Status = payload.Status
	entry.RequestID = payload.RequestID
	entry.Usage = payload.Usage
	if payload.Request != nil {
		entry.ControlSubtype = payload.Request.Subtype
	}
	if payload.Response != nil {
		entry.ResponseSubtype = payload.Response.Subtype
		if entry.RequestID == "" {
			entry.RequestID = payload.Response.RequestID
		}
		body := string(payload.Response.Response)
		if payload.Response.Error != "" {
			body = payload.Response.Error
		}
		entry.ResponseBody = claudeSubProbeExcerpt(body)
	}
	if payload.Event != nil {
		entry.StreamEvent = payload.Event.Type
		if payload.Event.Delta != nil {
			entry.DeltaType = payload.Event.Delta.Type
			if payload.Event.Delta.Text != "" {
				entry.Text = claudeSubProbeExcerpt(payload.Event.Delta.Text)
			}
		}
		if payload.Event.ContentBlock != nil {
			entry.ContentBlockType = payload.Event.ContentBlock.Type
			if payload.Event.ContentBlock.Type == "tool_use" {
				entry.ToolUseID = payload.Event.ContentBlock.ID
			}
		}
		// A message_start carries its usage under event.message, while a
		// message_delta carries it directly under event. Both are read here, the
		// way the production stream decoder reads them.
		if payload.Event.Usage != nil {
			entry.Usage = payload.Event.Usage
		} else if payload.Event.Message != nil && payload.Event.Message.Usage != nil {
			entry.Usage = payload.Event.Message.Usage
		}
	}
	if payload.Message != nil {
		if payload.Message.Usage != nil {
			entry.Usage = payload.Message.Usage
		}
		var text strings.Builder
		for _, block := range payload.Message.Content {
			if block.Type == "tool_use" && entry.ToolUseID == "" {
				entry.ToolUseID = block.ID
			}
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		entry.Text = claudeSubProbeExcerpt(text.String())
	}
	if entry.Type == "result" && payload.Result != "" {
		entry.Text = claudeSubProbeExcerpt(payload.Result)
	}
	return entry
}

// claudeSubProbeParseOutbound reduces one line the probe wrote to a recorded
// entry. Only synthetic text is kept.
func claudeSubProbeParseOutbound(line []byte, at int64) claudeSubProbeEntry {
	entry := claudeSubProbeEntry{AtMS: at, Dir: "out"}
	var payload struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   *struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Message *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return entry
	}
	entry.Type = payload.Type
	entry.RequestID = payload.RequestID
	if payload.Request != nil {
		entry.ControlSubtype = payload.Request.Subtype
	}
	if payload.Message != nil {
		var text strings.Builder
		for _, block := range payload.Message.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		entry.Text = claudeSubProbeExcerpt(text.String())
	}
	return entry
}

// claudeSubProbeParseInit extracts the init event's reported process shape,
// including the names of what it loaded.
func claudeSubProbeParseInit(ev claudeSubEvent, at int64) *claudeSubProbeInit {
	var payload struct {
		SessionID      string          `json:"session_id"`
		Model          string          `json:"model"`
		Tools          []string        `json:"tools"`
		Capabilities   json.RawMessage `json:"capabilities"`
		CLIVersion     string          `json:"claude_code_version"`
		Version        string          `json:"version"`
		PermissionMode string          `json:"permissionMode"`
	}
	init := &claudeSubProbeInit{AtMS: at, SessionID: ev.SessionID}
	if err := json.Unmarshal(ev.Raw, &payload); err != nil {
		return init
	}
	init.Model = payload.Model
	init.Tools = payload.Tools
	init.Capabilities = payload.Capabilities
	init.CLIVersion = payload.CLIVersion
	if init.CLIVersion == "" {
		init.CLIVersion = payload.Version
	}
	init.PermissionMode = payload.PermissionMode
	if init.SessionID == "" {
		init.SessionID = payload.SessionID
	}
	init.Extras = claudeSubProbeParseInitExtras(ev.Raw)
	return init
}

// claudeSubProbeParseInitExtras reduces the init event's load-reporting fields to
// their names. Hook fields are matched by name, since the CLI adds them as it
// gains features.
func claudeSubProbeParseInitExtras(raw json.RawMessage) map[string]claudeSubProbeNames {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if strings.Contains(strings.ToLower(key), "hook") {
			keys = append(keys, key)
			continue
		}
		for _, want := range claudeSubProbeInitExtras {
			if key == want {
				keys = append(keys, key)
				break
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	extras := make(map[string]claudeSubProbeNames, len(keys))
	for _, key := range keys {
		extras[key] = claudeSubProbeNames{Present: true, Names: claudeSubProbeNamesOf(fields[key])}
	}
	return extras
}

// claudeSubProbeNamesOf lists the names in one init field: strings as they are,
// object keys (hooks are keyed by event), and for arrays of objects the first of
// name, id, server_name or type that each element carries.
func claudeSubProbeNamesOf(raw json.RawMessage) []string {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		names := make([]string, 0, len(list))
		for _, item := range list {
			names = append(names, claudeSubProbeNameOf(item))
		}
		return names
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		names := make([]string, 0, len(object))
		for key := range object {
			names = append(names, key)
		}
		sort.Strings(names)
		return names
	}
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return []string{scalar}
	}
	return nil
}

// claudeSubProbeNameOf extracts one element's name.
func claudeSubProbeNameOf(raw json.RawMessage) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		var scalar string
		if json.Unmarshal(raw, &scalar) == nil {
			return scalar
		}
		return string(raw)
	}
	for _, key := range []string{"name", "id", "server_name", "type"} {
		if value, ok := object[key]; ok {
			var name string
			if json.Unmarshal(value, &name) == nil {
				return name
			}
		}
	}
	return "<unnamed>"
}

// claudeSubProbeValidateInit enforces the probe's precondition: exactly the
// synthetic probe tool, the requested model, and no MCP server other than
// steiner.
func claudeSubProbeValidateInit(init *claudeSubProbeInit, model string) error {
	if init.Model == "" {
		return errors.New("probe init event reported no model")
	}
	if !claudeSubProbeModelMatches(model, init.Model) {
		return fmt.Errorf("probe init model %q does not match the requested model %q", init.Model, model)
	}
	want := claudeSubToolPrefix + claudeSubProbeToolName
	if len(init.Tools) != 1 || init.Tools[0] != want {
		return fmt.Errorf("probe init tools are %v, want exactly [%s]", init.Tools, want)
	}
	if servers, ok := init.Extras["mcp_servers"]; ok {
		for _, name := range servers.Names {
			if name != claudeSubMCPServerName {
				return fmt.Errorf("probe init reports MCP server %q, want only %q", name, claudeSubMCPServerName)
			}
		}
	}
	return nil
}

// claudeSubProbeModelMatches accepts the requested model, its resolved id, and a
// CLI alias that resolves to an id containing the alias.
func claudeSubProbeModelMatches(requested, reported string) bool {
	if requested == "" {
		return false
	}
	if strings.EqualFold(requested, reported) {
		return true
	}
	return strings.Contains(strings.ToLower(reported), strings.ToLower(requested))
}

// claudeSubProbeReportedFlags are the argv switches the probe reports as present
// or absent, because they decide what the CLI loads from the user configuration.
var claudeSubProbeReportedFlags = []string{"--setting-sources", "--strict-mcp-config", "--tools", "--allowedTools"}

// claudeSubProbeArgvFlags reports which of the reported switches an argv carries.
func claudeSubProbeArgvFlags(argv []string) map[string]bool {
	flags := make(map[string]bool, len(claudeSubProbeReportedFlags))
	for _, flag := range claudeSubProbeReportedFlags {
		for _, arg := range argv {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				flags[flag] = true
				break
			}
		}
	}
	return flags
}

// claudeSubProbeFlagSummary renders an argv flag report as "flag=present" or
// "flag=absent" lines.
func claudeSubProbeFlagSummary(flags map[string]bool) []string {
	out := make([]string, 0, len(claudeSubProbeReportedFlags))
	for _, flag := range claudeSubProbeReportedFlags {
		state := "absent"
		if flags[flag] {
			state = "present"
		}
		out = append(out, flag+"="+state)
	}
	return out
}

// claudeSubProbeEnvSummary reports the child environment's shape: every key and
// the values of the probe-relevant non-secret keys only.
func claudeSubProbeEnvSummary(env []string) *claudeSubProbeEnv {
	summary := &claudeSubProbeEnv{Values: map[string]string{}}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		summary.Keys = append(summary.Keys, key)
		for _, open := range claudeSubProbeOpenEnvKeys {
			if key == open {
				summary.Values[key] = value
			}
		}
	}
	sort.Strings(summary.Keys)
	for _, open := range claudeSubProbeOpenEnvKeys {
		if _, ok := summary.Values[open]; !ok {
			summary.Values[open] = "(unset)"
		}
	}
	return summary
}

// claudeSubProbeOpenEnvKeys are the child environment variables whose values the
// report may state. Every other value is redacted.
var claudeSubProbeOpenEnvKeys = []string{
	"CLAUDE_CODE_RESUME_INTERRUPTED_TURN",
	"CLAUDE_CODE_RESUME_SHOWS_CUT_OFF_CALLS",
	"CLAUDE_CODE_RESUME_PROMPT",
	"CLAUDE_CONFIG_DIR",
	"HOME",
}

// claudeSubProbeIsInit matches the process's init event.
func claudeSubProbeIsInit(entry claudeSubProbeEntry) bool {
	return entry.Dir == "in" && entry.Type == "system" && entry.Subtype == "init"
}

// claudeSubProbeIsResult matches a terminal result event.
func claudeSubProbeIsResult(entry claudeSubProbeEntry) bool {
	return entry.Dir == "in" && entry.Type == "result"
}

// claudeSubProbeIsTextDelta matches the first streamed text of a message.
func claudeSubProbeIsTextDelta(entry claudeSubProbeEntry) bool {
	return entry.Dir == "in" && entry.DeltaType == "text_delta"
}

// claudeSubProbeIsToolUseStart matches a streamed tool_use content block start,
// which is window A of the interrupt experiments.
func claudeSubProbeIsToolUseStart(entry claudeSubProbeEntry) bool {
	return entry.Dir == "in" && entry.StreamEvent == "content_block_start" && entry.ContentBlockType == "tool_use" && entry.ToolUseID != ""
}

// claudeSubProbeIsToolUse matches either a streamed tool_use start or an
// assistant echo carrying a tool_use id.
func claudeSubProbeIsToolUse(entry claudeSubProbeEntry) bool {
	if claudeSubProbeIsToolUseStart(entry) {
		return true
	}
	return entry.Dir == "in" && entry.Type == "assistant" && entry.ToolUseID != ""
}

// claudeSubProbeExcerpt cuts s to the probe's text cap at a rune boundary and
// marks the cut.
func claudeSubProbeExcerpt(s string) string {
	if utf8.RuneCountInString(s) <= claudeSubProbeTextMaxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:claudeSubProbeTextMaxRunes]) + "..."
}

func claudeSubProbeErrorString(err error) string {
	if err == nil {
		return ""
	}
	return claudeSubProbeExcerpt(err.Error())
}

func claudeSubProbeUsageString(u *claudeSubProbeUsage) string {
	if u == nil {
		return "none"
	}
	return fmt.Sprintf("input=%d cache_creation=%d cache_read=%d output=%d", u.InputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens, u.OutputTokens)
}
