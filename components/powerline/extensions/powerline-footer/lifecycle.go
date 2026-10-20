package powerline_footer

import "strings"

// upstream: lifecycle.ts.

// shouldShowStartupWelcome: the welcome is terminal art. RPC hosts such as VS Code and Pi Desktop render custom UI as dialogs,
// so only the terminal UI shows it, and only on startup.
func shouldShowStartupWelcome(reason string, welcomeEnabled bool, mode string) bool {
	return reason == "startup" && welcomeEnabled && mode == "tui"
}

// isStaleExtensionContextError recognizes the errors Pi raises when a handler uses a context of an extension instance that was
// replaced (reload). The Go SDK has no such error: a Context is not bound to an instance and a call on a torn-down connection
// fails with the transport's error, so nothing in the port produces this text; the function exists for the check the original
// makes, which is a no-op here.
func isStaleExtensionContextError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "This extension instance is stale") ||
		strings.Contains(err.Error(), "This extension ctx is stale"))
}
