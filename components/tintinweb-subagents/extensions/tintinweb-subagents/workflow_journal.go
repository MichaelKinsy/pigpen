package tintinweb_subagents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"sort"
	"strings"
)

// The record a workflow run leaves so a later run can skip work: a JSON Lines file, one line per settled agent call,
// replayed by prefix. upstream: src/workflow/journal.ts (the header there explains why a prefix and not a lookup).

// workflowJournalEntry is one settled agent call, as replayed. Text is nil for a failed call. upstream: WorkflowJournalEntry.
type workflowJournalEntry struct {
	Index int     `json:"index"`
	Key   string  `json:"key"`
	OK    bool    `json:"ok"`
	Text  *string `json:"text,omitempty"`
	// Resumed marks a call that continued an earlier child (agent({ resume })): a replayed agent leaves no session
	// behind, so a journal carrying one is declined whole.
	Resumed bool `json:"resumed,omitempty"`
}

// workflowJournalKeyInput are the fields that decide what an agent does. phaseIndex and phaseTitle are deliberately absent.
// Schema is the serialized agent({ schema }), when the call asked for one. upstream: JournalKeyInput.
type workflowJournalKeyInput struct {
	Prompt            string
	Label, Model      *string
	AgentType, Effort *string
	Isolation, Gate   *string
	Resume            *string
	Schema            *string
}

// wfJSQuote is JavaScript's JSON.stringify of a string (not encoding/json: it escapes <, > and & and U+2028/2029, which
// JSON.stringify does not, and the key has to equal the original's for a journal to be shared).
func wfJSQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte("0123456789abcdef"[r>>4])
				b.WriteByte("0123456789abcdef"[r&15])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func wfJSQuoteOrNull(s *string) string {
	if s == nil {
		return "null"
	}
	return wfJSQuote(*s)
}

// workflowJournalKey is the stable hash of a call's payload; the field order is fixed here. upstream: journalKey.
func workflowJournalKey(in workflowJournalKeyInput) string {
	parts := []string{wfJSQuote(in.Prompt), wfJSQuoteOrNull(in.Label), wfJSQuoteOrNull(in.Model), wfJSQuoteOrNull(in.AgentType),
		wfJSQuoteOrNull(in.Effort), wfJSQuoteOrNull(in.Isolation), wfJSQuoteOrNull(in.Gate), wfJSQuoteOrNull(in.Resume)}
	// Appended only when present: an unconditional ninth slot would change the canonical form of every journal on disk.
	if in.Schema != nil {
		parts = append(parts, wfJSQuote(*in.Schema))
	}
	sum := sha256.Sum256([]byte("[" + strings.Join(parts, ",") + "]"))
	return hex.EncodeToString(sum[:])[:32]
}

// readWorkflowJournal reads a journal file into position order. It never fails: a missing, truncated or mangled journal means
// nothing to replay. upstream: readJournal.
func readWorkflowJournal(path string) []workflowJournalEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return []workflowJournalEntry{}
	}
	entries := []workflowJournalEntry{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var parsed any
		if json.Unmarshal([]byte(line), &parsed) != nil {
			continue // a half-written final line, or someone editing the file
		}
		if entry, ok := workflowJournalEntryOf(parsed); ok {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Index < entries[j].Index })
	return entries
}

// workflowJournalEntryOf is isEntry: the shape check of one parsed line.
func workflowJournalEntryOf(v any) (workflowJournalEntry, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return workflowJournalEntry{}, false
	}
	index, ok := m["index"].(float64)
	if !ok || index != math.Trunc(index) || index < 0 || index > math.MaxInt32 {
		return workflowJournalEntry{}, false
	}
	key, ok := m["key"].(string)
	if !ok {
		return workflowJournalEntry{}, false
	}
	okField, ok := m["ok"].(bool)
	if !ok {
		return workflowJournalEntry{}, false
	}
	entry := workflowJournalEntry{Index: int(index), Key: key, OK: okField}
	if t, present := m["text"]; present {
		s, isString := t.(string)
		if !isString {
			return workflowJournalEntry{}, false
		}
		entry.Text = &s
	}
	if r, present := m["resumed"]; present {
		b, isBool := r.(bool)
		if !isBool || !b {
			return workflowJournalEntry{}, false
		}
		entry.Resumed = true
	}
	return entry, true
}

// appendWorkflowJournal appends one settled call. A journal that cannot be written costs a future resume, nothing more.
func appendWorkflowJournal(path string, entry workflowJournalEntry) {
	line, err := wfJSONNoEscape(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// wfJSONNoEscape is json.Marshal without the HTML escaping (JSON.stringify writes <, > and & as they are).
func wfJSONNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
