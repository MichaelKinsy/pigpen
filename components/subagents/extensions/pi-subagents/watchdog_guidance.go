package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
)

// WATCHDOG.md standing instructions (watchdog/guidance.ts).

const watchdogGuidanceMaxChars = 8_000

func wdReadOptional(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return jsTrim(string(b))
}

// loadWatchdogGuidance reads fresh on every review: the project file first, then the user file.
func loadWatchdogGuidance(cwd string, enabled bool) string {
	if !enabled {
		return ""
	}
	var sections []string
	for _, dir := range []string{getProjectConfigDir(cwd), agentDir()} {
		if s := wdReadOptional(filepath.Join(dir, "WATCHDOG.md")); s != "" {
			sections = append(sections, s)
		}
	}
	return wdSlice(strings.Join(sections, "\n\n"), 0, watchdogGuidanceMaxChars)
}
