package prompt

import (
	"strings"
	"sync"
)

// RenderDateCwdReminder renders omp's per-turn date/cwd reminder XML block.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:18-20
func RenderDateCwdReminder(date, cwd string) string {
	text := strings.ReplaceAll(dateCwdReminderTemplate, "{{date}}", date)
	text = strings.ReplaceAll(text, "{{cwd}}", NormalizePromptPath(cwd))
	return strings.TrimSpace(formatPrompt(text))
}

// DateCwdReminderInjector attaches omp's deterministic `<system-reminder>` carrying
// the local date and cwd onto the first user message (keeping the system prompt
// byte-stable for prompt caching) and onto any subsequent new user message when
// the date or cwd changes.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:50-130
type DateCwdReminderInjector struct {
	mu              sync.Mutex
	rootKey         string
	currentReminder string
	// injectedByKey maps a user message's stable key (timestamp + raw text) to the reminder
	// originally attached to it, so earlier turns remain byte-identical across replays
	// even after date rollover or cwd change.
	injectedByKey map[string]string
}

// NewDateCwdReminderInjector creates a fresh injector for a session.
func NewDateCwdReminderInjector() *DateCwdReminderInjector {
	return &DateCwdReminderInjector{
		injectedByKey: map[string]string{},
	}
}

// Reset clears per-session injection state (e.g. on session_start).
func (d *DateCwdReminderInjector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rootKey = ""
	d.currentReminder = ""
	d.injectedByKey = map[string]string{}
}

// Transform returns a new message slice with the date/cwd reminder attached without
// mutating incoming maps, or (messages, false) when nothing needs changing.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:58-129
func (d *DateCwdReminderInjector) Transform(messages []any, date, cwd string) ([]any, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	reminder := RenderDateCwdReminder(date, cwd)
	firstIdx := -1
	var firstMsg map[string]any
	for i, m := range messages {
		if msg, ok := m.(map[string]any); ok && msg["role"] == "user" {
			firstIdx = i
			firstMsg = msg
			break
		}
	}
	if firstIdx < 0 {
		return messages, false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	firstKey := userMessageKey(firstMsg)
	if d.rootKey != firstKey {
		// New conversation root (first turn or post-compaction summary):
		// prune keys no longer present, keep reminders on retained turns, and tag the new root.
		present := map[string]bool{}
		for _, m := range messages {
			if msg, ok := m.(map[string]any); ok && msg["role"] == "user" {
				present[userMessageKey(msg)] = true
			}
		}
		for k := range d.injectedByKey {
			if !present[k] {
				delete(d.injectedByKey, k)
			}
		}
		d.rootKey = firstKey
		d.currentReminder = reminder
		if _, already := d.injectedByKey[firstKey]; !already && !messageStartsWithReminder(firstMsg, reminder) {
			d.injectedByKey[firstKey] = reminder
		}
	} else if d.currentReminder != reminder {
		// Date or cwd changed: attach to the newest unseen user message so prior bytes stay untouched.
		for i := len(messages) - 1; i >= 0; i-- {
			msg, ok := messages[i].(map[string]any)
			if !ok || msg["role"] != "user" {
				continue
			}
			key := userMessageKey(msg)
			if _, seen := d.injectedByKey[key]; !seen {
				d.injectedByKey[key] = reminder
				break
			}
		}
		d.currentReminder = reminder
	}

	changed := false
	out := make([]any, len(messages))
	for i, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok || msg["role"] != "user" {
			out[i] = m
			continue
		}
		key := userMessageKey(msg)
		rem, hasRem := d.injectedByKey[key]
		if !hasRem || messageStartsWithReminder(msg, rem) {
			out[i] = m
			continue
		}
		out[i] = prependReminderToUserMessage(msg, rem)
		changed = true
	}
	if !changed {
		return messages, false
	}
	return out, true
}

func userMessageKey(msg map[string]any) string {
	var b strings.Builder
	if ts, ok := msg["timestamp"]; ok {
		b.WriteString(anyString(ts))
	}
	b.WriteByte('\x00')
	switch c := msg["content"].(type) {
	case string:
		b.WriteString(c)
	case []any:
		for _, raw := range c {
			block, _ := raw.(map[string]any)
			if block == nil {
				continue
			}
			b.WriteString(anyString(block["type"]))
			b.WriteByte(':')
			if t, ok := block["text"].(string); ok {
				b.WriteString(t)
			} else if d, ok := block["data"].(string); ok {
				if len(d) > 32 {
					d = d[:32]
				}
				b.WriteString(d)
			}
			b.WriteByte('\x01')
		}
	}
	return b.String()
}

func messageStartsWithReminder(msg map[string]any, reminder string) bool {
	switch c := msg["content"].(type) {
	case string:
		return strings.HasPrefix(c, reminder)
	case []any:
		if len(c) == 0 {
			return false
		}
		first, _ := c[0].(map[string]any)
		return first != nil && first["type"] == "text" && first["text"] == reminder
	default:
		return false
	}
}

func prependReminderToUserMessage(msg map[string]any, reminder string) map[string]any {
	copied := make(map[string]any, len(msg))
	for k, v := range msg {
		copied[k] = v
	}
	switch c := msg["content"].(type) {
	case string:
		copied["content"] = reminder + "\n\n" + c
	case []any:
		blocks := make([]any, 0, len(c)+1)
		blocks = append(blocks, map[string]any{"type": "text", "text": reminder})
		blocks = append(blocks, c...)
		copied["content"] = blocks
	default:
		copied["content"] = reminder
	}
	return copied
}
