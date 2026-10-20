package provider

// claudeSubEntryMatches reports whether a request message still matches the
// recorded entry for its position. Assistant text is not compared (steiner may
// normalise it); user and tool messages are compared by digest and their images
// must be a subset of the images recorded as sent.
func claudeSubEntryMatches(entry claudeSubEntry, m Message) bool {
	if entry.Role != m.Role {
		return false
	}
	if m.Role == MessageRoleAssistant {
		return claudeSubSameIDs(entry.ToolCallIDs, claudeSubToolCallIDs(m))
	}
	if m.Role == MessageRoleTool && entry.ToolCallID != m.ToolCallID {
		return false
	}
	return entry.Digest == claudeSubDigest(m) && claudeSubImagesSubset(claudeSubImageFingerprints(m), entry.Images)
}

// claudeSubEntriesFromMessages returns the sync entry of each message in msgs.
func claudeSubEntriesFromMessages(msgs []Message) []claudeSubEntry {
	entries := make([]claudeSubEntry, 0, len(msgs))
	for _, m := range msgs {
		entries = append(entries, claudeSubEntryFromMessage(m))
	}
	return entries
}

func claudeSubEntryFromMessage(m Message) claudeSubEntry {
	return claudeSubEntry{
		Role:        m.Role,
		Digest:      claudeSubDigest(m),
		Images:      claudeSubImageFingerprints(m),
		ToolCallIDs: claudeSubToolCallIDs(m),
		ToolCallID:  m.ToolCallID,
	}
}

func claudeSubToolCallIDs(m Message) []string {
	if len(m.ToolCalls) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		ids = append(ids, c.ID)
	}
	return ids
}

// claudeSubTextOnlyAssistant reports whether an assistant message carries only
// text content. The one D22 partial assistant steiner keeps must not smuggle
// any other Message field into the recorded transcript; every field of Message
// other than Content must be empty, and the role must be assistant.
func claudeSubTextOnlyAssistant(m Message) bool {
	return m.Role == MessageRoleAssistant &&
		m.ReasoningContent == "" &&
		m.Name == "" &&
		m.ToolCallID == "" &&
		len(m.ToolCalls) == 0 &&
		len(m.Images) == 0 &&
		m.Turn == 0 &&
		m.ProviderMetadata == nil
}

// claudeSubEntrySnapshotEqual reports whether a recorded advisor-snapshot entry
// still matches the snapshot message at its position (role and content digest).
func claudeSubEntrySnapshotEqual(entry claudeSubEntry, m Message) bool {
	return entry.Role == m.Role && entry.Digest == claudeSubDigest(m)
}

// claudeSubSameIDs reports whether two tool-call id slices hold the same
// multiset: [c1,c2] and [c1,c1] must not compare equal.
func claudeSubSameIDs(a, b []string) bool {
	ca, cb := claudeSubIDCounts(a), claudeSubIDCounts(b)
	if len(ca) != len(cb) {
		return false
	}
	for id, n := range ca {
		if cb[id] != n {
			return false
		}
	}
	return true
}

// claudeSubSubsetIDs reports whether every id in sub is present in super at
// least as many times as in sub (multiset subset).
func claudeSubSubsetIDs(sub, super []string) bool {
	sc := claudeSubIDCounts(super)
	for id, n := range claudeSubIDCounts(sub) {
		if sc[id] < n {
			return false
		}
	}
	return true
}

// claudeSubImagesSubset reports whether cur is a multiset subset of recorded:
// removing sent images is tolerated, adding or replacing them is not.
func claudeSubImagesSubset(cur, recorded []string) bool {
	if len(cur) == 0 {
		return true
	}
	counts := claudeSubIDCounts(recorded)
	for _, f := range cur {
		if counts[f] == 0 {
			return false
		}
		counts[f]--
	}
	return true
}

func claudeSubIDCounts(ids []string) map[string]int {
	counts := make(map[string]int, len(ids))
	for _, id := range ids {
		counts[id]++
	}
	return counts
}
