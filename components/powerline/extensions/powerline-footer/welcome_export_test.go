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

// SetWelcomeAfterHeaderMark sets the hook that runs between marking the header active and installing it.
func SetWelcomeAfterHeaderMark(f func()) (restore func()) {
	welcomeAfterHeaderMark = f
	return func() { welcomeAfterHeaderMark = nil }
}

// SetWelcomeBeforeHeaderInstall sets the hook that runs after the header install was checked and before it is made.
func SetWelcomeBeforeHeaderInstall(f func()) (restore func()) {
	welcomeBeforeHeaderInstall = f
	return func() { welcomeBeforeHeaderInstall = nil }
}
