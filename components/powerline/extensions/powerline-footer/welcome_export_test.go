package powerline_footer

// Hooks for the external tests of the welcome lifecycle (welcome_lifecycle_test.go).

// SetWelcomeTimers replaces the welcome's clock and returns the function that restores it.
func SetWelcomeTimers(t welcomeTimers) (restore func()) {
	old := welcomeClock
	welcomeClock = t
	return func() { welcomeClock = old }
}

// SetWelcomeFSHook sets the hook that runs before each filesystem operation of the recent-session discovery.
func SetWelcomeFSHook(f func(op, path string)) (restore func()) {
	welcomeFSHook = f
	return func() { welcomeFSHook = nil }
}
