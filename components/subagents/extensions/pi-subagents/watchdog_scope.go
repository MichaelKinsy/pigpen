package pi_subagents


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
func (s *watchdogScope) addPrompt(prompt, createdAt string) { panic("not implemented") }

func (s *watchdogScope) reset() { panic("not implemented") }

func (s *watchdogScope) snapshot() []watchdogScopeEntry { panic("not implemented") }

func (s *watchdogScope) render() string { panic("not implemented") }

func (s *watchdogScope) trim() { panic("not implemented") }
