package powerline_footer

import "strings"

// isStaleExtensionContextError matches the message of Pi's stale-context errors. upstream: lifecycle.ts isStaleExtensionContextError.
func isStaleExtensionContextError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "This extension instance is stale") ||
		strings.Contains(err.Error(), "This extension ctx is stale"))
}
