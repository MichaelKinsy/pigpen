package pi_subagents


// WATCHDOG.md standing instructions (watchdog/guidance.ts).

const watchdogGuidanceMaxChars = 8_000

func wdReadOptional(path string) string { panic("not implemented") }

// loadWatchdogGuidance reads fresh on every review: the project file first, then the user file.
func loadWatchdogGuidance(cwd string, enabled bool) string { panic("not implemented") }
