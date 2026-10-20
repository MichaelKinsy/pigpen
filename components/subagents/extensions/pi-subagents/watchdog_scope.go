package pi_subagents

import "strings"

// The scope artifact (watchdog/scope.ts): the real user prompts the watchdog judges drift against.

const (
	wdMaxScopeEntries    = 8
	wdMaxScopeEntryChars = 2_000
	wdMaxScopeTotalChars = 16_000
)

type watchdogScopeEntry struct {
	Prompt    string
	CreatedAt string
}

type watchdogScope struct{ entries []watchdogScopeEntry }

// addPrompt records a prompt; createdAt "" is now.
func (s *watchdogScope) addPrompt(prompt, createdAt string) {
	normalized := jsTrim(prompt)
	if normalized == "" {
		return
	}
	if createdAt == "" {
		createdAt = isoNow()
	}
	if wdLen(normalized) > wdMaxScopeEntryChars {
		normalized = wdSlice(normalized, 0, wdMaxScopeEntryChars)
	}
	s.entries = append(s.entries, watchdogScopeEntry{Prompt: normalized, CreatedAt: createdAt})
	s.trim()
}

func (s *watchdogScope) reset() { s.entries = nil }

func (s *watchdogScope) snapshot() []watchdogScopeEntry {
	return append([]watchdogScopeEntry{}, s.entries...)
}

func (s *watchdogScope) render() string {
	if len(s.entries) == 0 {
		return ""
	}
	parts := []string{
		"Current scope:",
		"The following real user prompts are the current scope record, newest last. Side questions are additive, not scope drift or cancellation of older objectives; only explicit changes supersede requirements. Use watchdog_warn for evidence-backed reminders of forgotten authorized work, not dependencies still pending or explicit holds. The orchestrator owns task tracking. Flag unauthorized work as category 'scope-drift'.",
	}
	for i, e := range s.entries {
		parts = append(parts, "Scope prompt "+itoa(i+1)+" ("+e.CreatedAt+"):\n"+e.Prompt)
	}
	return strings.Join(parts, "\n\n")
}

func (s *watchdogScope) trim() {
	for len(s.entries) > wdMaxScopeEntries {
		s.entries = s.entries[1:]
	}
	total := 0
	for _, e := range s.entries {
		total += wdLen(e.Prompt)
	}
	for len(s.entries) > 1 && total > wdMaxScopeTotalChars {
		total -= wdLen(s.entries[0].Prompt)
		s.entries = s.entries[1:]
	}
}
