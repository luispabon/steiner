package provider

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func claudeSubSyncContents(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Content)
	}
	return out
}

func claudeSubSyncUserMsg(content string) Message {
	return Message{Role: MessageRoleUser, Content: content}
}

func claudeSubSyncAssistantMsg(content string, ids ...string) Message {
	m := Message{Role: MessageRoleAssistant, Content: content}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, ToolCall{ID: id})
	}
	return m
}

func claudeSubSyncToolMsg(id, content string) Message {
	return Message{Role: MessageRoleTool, ToolCallID: id, Content: content}
}

func claudeSubSyncUserImage(content string, images ...ImageBlock) Message {
	return Message{Role: MessageRoleUser, Content: content, Images: images}
}

func claudeSubSyncToolImage(id, content string, images ...ImageBlock) Message {
	return Message{Role: MessageRoleTool, ToolCallID: id, Content: content, Images: images}
}

// claudeSubSyncStartedSync returns a sync whose record already holds content,
// committed through the normal plan/commitSent path.
func claudeSubSyncStartedSync(t *testing.T, msgs ...Message) *claudeSubSync {
	t.Helper()
	s := &claudeSubSync{}
	delta, err := s.plan(ChatRequest{Messages: msgs})
	if err != nil {
		t.Fatalf("setup plan: %v", err)
	}
	s.commitSent(delta)
	return s
}

func TestClaudeSubSyncFirstRequest(t *testing.T) {
	// A realistic steiner first request: the static system prefix, the user
	// session-date block, the system AGENTS block, project context and prompt.
	req := ChatRequest{Messages: []Message{
		{Role: MessageRoleSystem, Content: "steiner preamble"},
		claudeSubSyncUserMsg("Today's date is 2026-10-08."),
		{Role: MessageRoleSystem, Content: "AGENTS.md contents"},
		claudeSubSyncUserMsg("Project context: ..."),
		claudeSubSyncUserMsg("do the thing"),
	}}
	var s claudeSubSync
	delta, err := s.plan(req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantUser := []string{"Today's date is 2026-10-08.", "Project context: ...", "do the thing"}
	if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, wantUser) {
		t.Fatalf("delta user = %q, want %q", got, wantUser)
	}
	if len(delta.ToolResults) != 0 {
		t.Fatalf("delta tool results = %+v, want none", delta.ToolResults)
	}
	wantSys := "steiner preamble\n\nAGENTS.md contents\n\n" + claudeSubToolNote
	if s.systemPrompt != wantSys {
		t.Fatalf("system prompt = %q, want %q", s.systemPrompt, wantSys)
	}
	if !strings.HasSuffix(s.systemPrompt, claudeSubToolNote) {
		t.Fatalf("system prompt %q does not end with the tool note", s.systemPrompt)
	}
	if s.started {
		t.Fatal("sync started before commitSent")
	}
}

func TestClaudeSubSyncFirstRequestRejectsHistory(t *testing.T) {
	tests := []struct {
		name string
		msgs []Message
	}{
		{name: "assistant history", msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("noted")}},
		{name: "tool history", msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncToolMsg("c1", "result")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s claudeSubSync
			_, err := s.plan(ChatRequest{Messages: tc.msgs})
			if !errors.Is(err, errClaudeSubHistoryChanged) {
				t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
			}
		})
	}
}

func TestClaudeSubSyncAppendToolAndUser(t *testing.T) {
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
	assistant := claudeSubSyncAssistantMsg("working", "c1", "c2")
	s.commitAssistant(assistant)

	req := ChatRequest{Messages: []Message{
		claudeSubSyncUserMsg("hi"),
		assistant,
		claudeSubSyncToolMsg("c1", "result one"),
		claudeSubSyncToolMsg("c2", "result two"),
		claudeSubSyncUserMsg("steer"),
	}}
	delta, err := s.plan(req)
	if err != nil {
		t.Fatalf("plan second: %v", err)
	}
	wantTools := []string{"result one", "result two"}
	if got := claudeSubSyncContents(delta.ToolResults); !reflect.DeepEqual(got, wantTools) {
		t.Fatalf("tool results = %q, want %q", got, wantTools)
	}
	if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, []string{"steer"}) {
		t.Fatalf("user = %q, want [steer]", got)
	}
}

// claudeSubSyncInterruptedBase builds a sync whose record holds one user message
// and one assistant message with two tool calls, then marks it interrupted when
// mark is true.
func claudeSubSyncInterruptedBase(t *testing.T, mark bool) *claudeSubSync {
	t.Helper()
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
	s.commitAssistant(claudeSubSyncAssistantMsg("a1", "c1", "c2"))
	if mark {
		s.markInterrupted()
	}
	return s
}

func TestClaudeSubSyncInterruptTolerance(t *testing.T) {
	tests := []struct {
		name    string
		msgs    []Message
		wantErr bool
		check   func(t *testing.T, s *claudeSubSync, delta claudeSubDelta)
	}{
		{
			name: "subset of tool calls",
			msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1")},
			check: func(t *testing.T, s *claudeSubSync, delta claudeSubDelta) {
				if len(delta.User) != 0 || len(delta.ToolResults) != 0 {
					t.Fatalf("delta = %+v, want empty", delta)
				}
				if got := s.entries[1].ToolCallIDs; !reflect.DeepEqual(got, []string{"c1"}) {
					t.Fatalf("recorded ids = %v, want [c1]", got)
				}
			},
		},
		{
			name: "no tool calls",
			msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1")},
			check: func(t *testing.T, s *claudeSubSync, delta claudeSubDelta) {
				if s.entries[1].ToolCallIDs != nil {
					t.Fatalf("recorded ids = %v, want none", s.entries[1].ToolCallIDs)
				}
			},
		},
		{
			name: "assistant omitted entirely",
			msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncUserMsg("steer")},
			check: func(t *testing.T, s *claudeSubSync, delta claudeSubDelta) {
				if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, []string{"steer"}) {
					t.Fatalf("user = %q, want [steer]", got)
				}
				if len(s.entries) != 1 || s.entries[0].Role != MessageRoleUser {
					t.Fatalf("entries = %+v, want the single user entry", s.entries)
				}
			},
		},
		{
			name: "one partial assistant recorded not sent",
			msgs: []Message{
				claudeSubSyncUserMsg("hi"),
				claudeSubSyncAssistantMsg("a1", "c1"),
				claudeSubSyncAssistantMsg("partial text"),
			},
			check: func(t *testing.T, s *claudeSubSync, delta claudeSubDelta) {
				if len(delta.User) != 0 || len(delta.ToolResults) != 0 {
					t.Fatalf("delta = %+v, want empty", delta)
				}
				if len(s.entries) != 3 {
					t.Fatalf("entries = %d, want 3", len(s.entries))
				}
				last := s.entries[2]
				if last.Role != MessageRoleAssistant || last.Digest != claudeSubDigest(claudeSubSyncAssistantMsg("partial text")) {
					t.Fatalf("last entry = %+v, want the recorded partial assistant", last)
				}
			},
		},
		{
			name:    "second appended assistant errors",
			wantErr: true,
			msgs: []Message{
				claudeSubSyncUserMsg("hi"),
				claudeSubSyncAssistantMsg("a1", "c1"),
				claudeSubSyncAssistantMsg("partial one"),
				claudeSubSyncAssistantMsg("partial two"),
			},
		},
		{
			name:    "other edit still errors",
			wantErr: true,
			msgs:    []Message{claudeSubSyncUserMsg("changed"), claudeSubSyncAssistantMsg("a1", "c1")},
		},
		{
			name:    "extra tool call is not a subset",
			wantErr: true,
			msgs:    []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1", "c2", "c3")},
		},
		{
			name:    "duplicate ids are not a subset",
			wantErr: true,
			msgs:    []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1", "c1")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := claudeSubSyncInterruptedBase(t, true)
			delta, err := s.plan(ChatRequest{Messages: tc.msgs})
			if tc.wantErr {
				if !errors.Is(err, errClaudeSubHistoryChanged) {
					t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			tc.check(t, s, delta)
		})
	}
}

func TestClaudeSubSyncInterruptRequiresMark(t *testing.T) {
	tests := []struct {
		name string
		msgs []Message
	}{
		{name: "subset tool calls", msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1")}},
		{name: "assistant omitted", msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncUserMsg("steer")}},
		{name: "appended text-only assistant", msgs: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1", "c2"), claudeSubSyncAssistantMsg("partial")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := claudeSubSyncInterruptedBase(t, false)
			if _, err := s.plan(ChatRequest{Messages: tc.msgs}); !errors.Is(err, errClaudeSubHistoryChanged) {
				t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
			}
		})
	}
}

// TestClaudeSubSyncAssistantIDMultiset proves assistant tool-call identity is
// compared as a multiset, not as set membership.
func TestClaudeSubSyncAssistantIDMultiset(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
	}{
		{name: "reordered ids match", ids: []string{"c2", "c1"}},
		{name: "duplicate incoming id errors", ids: []string{"c1", "c1"}},
		{name: "foreign id errors", ids: []string{"c1", "c3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
			s.commitAssistant(claudeSubSyncAssistantMsg("a1", "c1", "c2"))
			_, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", tc.ids...)}})
			wantErr := tc.name != "reordered ids match"
			if wantErr && !errors.Is(err, errClaudeSubHistoryChanged) {
				t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
			}
			if !wantErr && err != nil {
				t.Fatalf("plan: %v", err)
			}
		})
	}
}

// TestClaudeSubSyncInterruptOldAssistantImmutable proves a completed assistant
// cannot be rewritten when the interrupt happens after a follow-up user was
// sent but before the next assistant was committed.
func TestClaudeSubSyncInterruptOldAssistantImmutable(t *testing.T) {
	s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("u1"))
	a1 := claudeSubSyncAssistantMsg("a1", "c1")
	s.commitAssistant(a1)
	d, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("u1"), a1, claudeSubSyncUserMsg("u2")}})
	if err != nil {
		t.Fatalf("plan follow-up user: %v", err)
	}
	s.commitSent(d)

	// The next assistant call was cancelled before any assistant committed, so
	// the committed a1 is a completed call and must not be interruptible.
	s.markInterrupted()
	if s.interruptible != -1 {
		t.Fatalf("interruptible = %d, want -1", s.interruptible)
	}

	// Unchanged history is still accepted.
	if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("u1"), a1, claudeSubSyncUserMsg("u2")}}); err != nil {
		t.Fatalf("plan unchanged: %v", err)
	}
	// Rewriting the completed assistant's tool calls must error.
	if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("u1"), claudeSubSyncAssistantMsg("a1", "c9"), claudeSubSyncUserMsg("u2")}}); !errors.Is(err, errClaudeSubHistoryChanged) {
		t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
	}
}

// TestClaudeSubSyncInterruptOmittedToolResult proves that omitting an
// interrupted assistant does not let a same-id recorded tool result be rewritten
// into an appended new result.
func TestClaudeSubSyncInterruptOmittedToolResult(t *testing.T) {
	base := func(t *testing.T) *claudeSubSync {
		t.Helper()
		s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
		a1 := claudeSubSyncAssistantMsg("a1", "c1", "c2")
		s.commitAssistant(a1)
		d, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), a1, claudeSubSyncToolMsg("c1", "orig")}})
		if err != nil {
			t.Fatalf("plan tool result: %v", err)
		}
		s.commitSent(d)
		s.markInterrupted()
		return s
	}

	t.Run("omitted assistant with unchanged tool result is accepted", func(t *testing.T) {
		s := base(t)
		delta, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncToolMsg("c1", "orig")}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(delta.User) != 0 || len(delta.ToolResults) != 0 {
			t.Fatalf("delta = %+v, want empty (the tool result was already sent)", delta)
		}
	})

	t.Run("omitted assistant with changed tool result errors", func(t *testing.T) {
		s := base(t)
		if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncToolMsg("c1", "changed")}}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("omitted assistant and tool result together is tolerated", func(t *testing.T) {
		s := base(t)
		if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi")}}); err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(s.entries) != 1 {
			t.Fatalf("entries = %+v, want just the user entry", s.entries)
		}
	})

	t.Run("user before changed tool result errors", func(t *testing.T) {
		s := base(t)
		msgs := []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncUserMsg("steer"), claudeSubSyncToolMsg("c1", "changed")}
		if _, err := s.plan(ChatRequest{Messages: msgs}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("user before unchanged reordered tool result errors", func(t *testing.T) {
		s := base(t)
		msgs := []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncUserMsg("steer"), claudeSubSyncToolMsg("c1", "orig")}
		if _, err := s.plan(ChatRequest{Messages: msgs}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("duplicate same-id suffix tool result errors", func(t *testing.T) {
		s := base(t)
		msgs := []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncToolMsg("c1", "orig"), claudeSubSyncToolMsg("c1", "orig again")}
		if _, err := s.plan(ChatRequest{Messages: msgs}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})
}

// TestClaudeSubSyncInterruptPartialTextOnly proves the one appended D22 partial
// assistant must be text-only.
func TestClaudeSubSyncInterruptPartialTextOnly(t *testing.T) {
	base := func(t *testing.T) *claudeSubSync {
		t.Helper()
		s := claudeSubSyncStartedSync(t, claudeSubSyncUserMsg("hi"))
		s.commitAssistant(claudeSubSyncAssistantMsg("a1", "c1"))
		s.markInterrupted()
		return s
	}

	t.Run("text-only partial is recorded", func(t *testing.T) {
		s := base(t)
		delta, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1"), claudeSubSyncAssistantMsg("partial")}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(delta.User) != 0 || len(delta.ToolResults) != 0 {
			t.Fatalf("delta = %+v, want empty", delta)
		}
		if len(s.entries) != 3 {
			t.Fatalf("entries = %d, want 3", len(s.entries))
		}
	})

	t.Run("image-bearing partial errors", func(t *testing.T) {
		s := base(t)
		partial := Message{Role: MessageRoleAssistant, Content: "partial", Images: []ImageBlock{{MediaType: "image/png", Data: "AAAA"}}}
		if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1"), partial}}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("tool-call-bearing partial errors", func(t *testing.T) {
		s := base(t)
		if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1"), claudeSubSyncAssistantMsg("partial", "c9")}}); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("non-text payload partial errors", func(t *testing.T) {
		tests := []struct {
			name    string
			partial Message
		}{
			{name: "reasoning", partial: Message{Role: MessageRoleAssistant, Content: "partial", ReasoningContent: "thinking"}},
			{name: "name", partial: Message{Role: MessageRoleAssistant, Content: "partial", Name: "helper"}},
			{name: "turn", partial: Message{Role: MessageRoleAssistant, Content: "partial", Turn: 3}},
			{name: "tool call id", partial: Message{Role: MessageRoleAssistant, Content: "partial", ToolCallID: "c9"}},
			{name: "metadata", partial: Message{Role: MessageRoleAssistant, Content: "partial", ProviderMetadata: &MessageProviderMetadata{Anthropic: &AnthropicMessageMetadata{ThinkingSignature: "sig"}}}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				s := base(t)
				if _, err := s.plan(ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hi"), claudeSubSyncAssistantMsg("a1", "c1"), tc.partial}}); !errors.Is(err, errClaudeSubHistoryChanged) {
					t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
				}
			})
		}
	})
}

func TestClaudeSubSyncCanceledCallDoesNotResend(t *testing.T) {
	s := &claudeSubSync{}
	req := ChatRequest{Messages: []Message{claudeSubSyncUserMsg("hello")}}
	delta, err := s.plan(req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// The user line was written, then the call was cancelled: no assistant is
	// committed, but the sent user content is already recorded (D22).
	s.commitSent(delta)
	repeat, err := s.plan(req)
	if err != nil {
		t.Fatalf("repeat plan: %v", err)
	}
	if len(repeat.User) != 0 || len(repeat.ToolResults) != 0 {
		t.Fatalf("repeat delta = %+v, want empty", repeat)
	}
}

func TestClaudeSubSyncHistoryEdits(t *testing.T) {
	imageUser := claudeSubSyncUserImage("hi", ImageBlock{MediaType: "image/png", Data: "AAAA"})
	imageTool := claudeSubSyncToolImage("c1", "shot", ImageBlock{MediaType: "image/png", Data: "BBBB"})

	base := func(t *testing.T) *claudeSubSync {
		t.Helper()
		s := claudeSubSyncStartedSync(t, imageUser)
		assistant := claudeSubSyncAssistantMsg("a1", "c1")
		s.commitAssistant(assistant)
		d1, err := s.plan(ChatRequest{Messages: []Message{imageUser, assistant, imageTool}})
		if err != nil {
			t.Fatalf("setup plan 2: %v", err)
		}
		s.commitSent(d1)
		return s
	}

	tests := []struct {
		name    string
		msgs    []Message
		wantErr bool
	}{
		{
			name: "image removal tolerated",
			msgs: []Message{
				claudeSubSyncUserMsg("hi"),
				claudeSubSyncAssistantMsg("a1", "c1"),
				claudeSubSyncToolMsg("c1", "shot"),
			},
		},
		{
			name:    "user image replacement errors",
			wantErr: true,
			msgs:    []Message{claudeSubSyncUserImage("hi", ImageBlock{MediaType: "image/png", Data: "CCCC"}), claudeSubSyncAssistantMsg("a1", "c1"), imageTool},
		},
		{
			name:    "user image addition errors",
			wantErr: true,
			msgs:    []Message{claudeSubSyncUserImage("hi", ImageBlock{MediaType: "image/png", Data: "AAAA"}, ImageBlock{MediaType: "image/png", Data: "DDDD"}), claudeSubSyncAssistantMsg("a1", "c1"), imageTool},
		},
		{
			name:    "tool image replacement errors",
			wantErr: true,
			msgs:    []Message{imageUser, claudeSubSyncAssistantMsg("a1", "c1"), claudeSubSyncToolImage("c1", "shot", ImageBlock{MediaType: "image/png", Data: "CCCC"})},
		},
		{
			name:    "user text change errors",
			wantErr: true,
			msgs:    []Message{claudeSubSyncUserImage("hi!", imageUser.Images...), claudeSubSyncAssistantMsg("a1", "c1"), imageTool},
		},
		{
			name:    "user name change errors",
			wantErr: true,
			msgs:    []Message{{Role: MessageRoleUser, Content: "hi", Name: "someone", Images: imageUser.Images}, claudeSubSyncAssistantMsg("a1", "c1"), imageTool},
		},
		{
			name:    "tool text change errors",
			wantErr: true,
			msgs:    []Message{imageUser, claudeSubSyncAssistantMsg("a1", "c1"), claudeSubSyncToolImage("c1", "shot!", imageTool.Images...)},
		},
		{
			name:    "tool id change errors",
			wantErr: true,
			msgs:    []Message{imageUser, claudeSubSyncAssistantMsg("a1", "c1"), claudeSubSyncToolImage("c9", "shot", imageTool.Images...)},
		},
		{
			name:    "deleting a message errors",
			wantErr: true,
			msgs:    []Message{imageUser, claudeSubSyncAssistantMsg("a1", "c1")},
		},
		{
			name:    "assistant tool-call id change errors",
			wantErr: true,
			msgs:    []Message{imageUser, claudeSubSyncAssistantMsg("a1", "c9"), imageTool},
		},
		{
			name:    "appended assistant errors",
			wantErr: true,
			msgs:    []Message{imageUser, claudeSubSyncAssistantMsg("a1", "c1"), imageTool, claudeSubSyncAssistantMsg("extra")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := base(t)
			delta, err := s.plan(ChatRequest{Messages: tc.msgs})
			if tc.wantErr {
				if !errors.Is(err, errClaudeSubHistoryChanged) {
					t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if len(delta.User) != 0 || len(delta.ToolResults) != 0 {
				t.Fatalf("delta = %+v, want empty", delta)
			}
		})
	}
}

func TestClaudeSubSyncAdvisor(t *testing.T) {
	advisorReq := func(final string, snapshot ...Message) ChatRequest {
		msgs := []Message{{Role: MessageRoleSystem, Content: "advisor system prompt"}}
		msgs = append(msgs, snapshot...)
		msgs = append(msgs, claudeSubSyncUserMsg(final))
		return ChatRequest{Messages: msgs, AdvisorCacheProfile: true}
	}

	t.Run("first call renders the whole snapshot", func(t *testing.T) {
		var s claudeSubSync
		snapshot := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1")}
		delta, err := s.planAdvisor(advisorReq("files and question", snapshot...))
		if err != nil {
			t.Fatalf("planAdvisor: %v", err)
		}
		want := []string{claudeSubTranscript(snapshot), "files and question"}
		if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, want) {
			t.Fatalf("user = %q, want %q", got, want)
		}
		if !strings.Contains(delta.User[0].Content, "[user]\nq1") || !strings.Contains(delta.User[0].Content, "[assistant]\na1") {
			t.Fatalf("transcript = %q", delta.User[0].Content)
		}
		if s.systemPrompt != "advisor system prompt\n\n"+claudeSubToolNote {
			t.Fatalf("system prompt = %q", s.systemPrompt)
		}
		s.commitAdvisor(snapshot)
		if !s.started {
			t.Fatal("advisor sync not started after commitAdvisor")
		}
	})

	t.Run("grown snapshot sends only the new part", func(t *testing.T) {
		var s claudeSubSync
		first := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1")}
		if _, err := s.planAdvisor(advisorReq("first question", first...)); err != nil {
			t.Fatalf("planAdvisor first: %v", err)
		}
		s.commitAdvisor(first)

		second := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1"), claudeSubSyncUserMsg("q2")}
		delta, err := s.planAdvisor(advisorReq("second question", second...))
		if err != nil {
			t.Fatalf("planAdvisor second: %v", err)
		}
		want := []string{claudeSubTranscript([]Message{claudeSubSyncUserMsg("q2")}), "second question"}
		if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, want) {
			t.Fatalf("user = %q, want %q", got, want)
		}
	})

	t.Run("unchanged snapshot sends only the question", func(t *testing.T) {
		var s claudeSubSync
		snapshot := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1")}
		if _, err := s.planAdvisor(advisorReq("first question", snapshot...)); err != nil {
			t.Fatalf("planAdvisor first: %v", err)
		}
		s.commitAdvisor(snapshot)
		delta, err := s.planAdvisor(advisorReq("second question", snapshot...))
		if err != nil {
			t.Fatalf("planAdvisor second: %v", err)
		}
		if got := claudeSubSyncContents(delta.User); !reflect.DeepEqual(got, []string{"second question"}) {
			t.Fatalf("user = %q, want [second question]", got)
		}
	})

	t.Run("shrunk snapshot errors", func(t *testing.T) {
		var s claudeSubSync
		full := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1"), claudeSubSyncUserMsg("q2")}
		if _, err := s.planAdvisor(advisorReq("first", full...)); err != nil {
			t.Fatalf("planAdvisor first: %v", err)
		}
		s.commitAdvisor(full)
		if _, err := s.planAdvisor(advisorReq("shrunk", claudeSubSyncUserMsg("q1"))); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("same-length snapshot rewrite errors", func(t *testing.T) {
		var s claudeSubSync
		first := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1")}
		if _, err := s.planAdvisor(advisorReq("first", first...)); err != nil {
			t.Fatalf("planAdvisor first: %v", err)
		}
		s.commitAdvisor(first)
		rewritten := []Message{claudeSubSyncUserMsg("q1 CHANGED"), claudeSubSyncAssistantMsg("a1")}
		if _, err := s.planAdvisor(advisorReq("second", rewritten...)); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("prefix rewrite with later append errors", func(t *testing.T) {
		var s claudeSubSync
		first := []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1")}
		if _, err := s.planAdvisor(advisorReq("first", first...)); err != nil {
			t.Fatalf("planAdvisor first: %v", err)
		}
		s.commitAdvisor(first)
		grown := []Message{claudeSubSyncUserMsg("q1 CHANGED"), claudeSubSyncAssistantMsg("a1"), claudeSubSyncUserMsg("q2")}
		if _, err := s.planAdvisor(advisorReq("second", grown...)); !errors.Is(err, errClaudeSubHistoryChanged) {
			t.Fatalf("err = %v, want errClaudeSubHistoryChanged", err)
		}
	})

	t.Run("non-user final message errors", func(t *testing.T) {
		var s claudeSubSync
		req := ChatRequest{Messages: []Message{
			{Role: MessageRoleSystem, Content: "advisor system prompt"},
			claudeSubSyncUserMsg("q1"),
			claudeSubSyncAssistantMsg("a1"),
		}}
		if _, err := s.planAdvisor(req); err == nil {
			t.Fatal("expected an error for a non-user final message")
		}
	})

	t.Run("trailing system message errors", func(t *testing.T) {
		var s claudeSubSync
		req := ChatRequest{Messages: []Message{
			{Role: MessageRoleSystem, Content: "advisor system prompt"},
			claudeSubSyncUserMsg("q1"),
			claudeSubSyncAssistantMsg("a1"),
			claudeSubSyncUserMsg("files and question"),
			{Role: MessageRoleSystem, Content: "late reminder"},
		}}
		if _, err := s.planAdvisor(req); err == nil {
			t.Fatal("expected an error for a trailing system message")
		}
	})
}

func TestClaudeSubSyncSystemPrompt(t *testing.T) {
	tests := []struct {
		name string
		msgs []Message
		want string
	}{
		{
			name: "joins trimmed system messages in order and appends the note",
			msgs: []Message{
				{Role: MessageRoleSystem, Content: "preamble"},
				claudeSubSyncUserMsg("date"),
				{Role: MessageRoleSystem, Content: "  AGENTS block  "},
				{Role: MessageRoleSystem, Content: "   "},
				claudeSubSyncUserMsg("prompt"),
			},
			want: "preamble\n\nAGENTS block\n\n" + claudeSubToolNote,
		},
		{
			name: "only the note when there are no system messages",
			msgs: []Message{claudeSubSyncUserMsg("hi")},
			want: claudeSubToolNote,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubSystemPrompt(tc.msgs); got != tc.want {
				t.Fatalf("system prompt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeSubTranscript(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []Message
		contains []string
		absent   []string
	}{
		{
			name:     "user and assistant text keep their labels",
			msgs:     []Message{claudeSubSyncUserMsg("q1"), claudeSubSyncAssistantMsg("a1")},
			contains: []string{"[user]\nq1", "[assistant]\na1"},
		},
		{
			name:     "tool result is attributed to tool output",
			msgs:     []Message{claudeSubSyncAssistantMsg("", "c1"), claudeSubSyncToolMsg("c1", "file body")},
			contains: []string{"[tool output]\nfile body"},
			absent:   []string{"[assistant]\nfile body"},
		},
		{
			name: "tool-call-only assistant shows name and arguments",
			msgs: []Message{{Role: MessageRoleAssistant, ToolCalls: []ToolCall{
				{ID: "c1", Name: "read", Arguments: map[string]any{"path": "main.go"}},
			}}},
			contains: []string{"read", `"path":"main.go"`},
		},
		{
			name: "raw tool-call arguments are rendered verbatim",
			msgs: []Message{{Role: MessageRoleAssistant, ToolCalls: []ToolCall{
				{ID: "c2", Name: "grep", RawArguments: `{"pattern":"TODO"}`},
			}}},
			contains: []string{"grep", `{"pattern":"TODO"}`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeSubTranscript(tc.msgs)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("transcript = %q, want it to contain %q", got, want)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(got, bad) {
					t.Errorf("transcript = %q, must not contain %q", got, bad)
				}
			}
		})
	}
}

func TestClaudeSubSyncUserBlocks(t *testing.T) {
	msgs := []Message{
		{Role: MessageRoleUser, Content: "hi", Images: []ImageBlock{{MediaType: "image/png", Data: "AAAA"}}},
		{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/jpeg", Data: "BBBB"}}},
		{Role: MessageRoleUser, Images: []ImageBlock{{FilePath: "/tmp/x.png", MediaType: "image/png"}}},
	}
	want := []map[string]any{
		{"type": "text", "text": "hi"},
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AAAA"}},
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": "BBBB"}},
	}
	if got := claudeSubUserBlocks(msgs); !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %#v, want %#v", got, want)
	}
}
