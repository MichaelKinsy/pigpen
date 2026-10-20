package powerline_footer

// upstream: lifecycle.ts. The welcome is terminal art. RPC hosts such as VS Code and Pi Desktop render custom UI as dialogs, so
// only the terminal UI shows it.
func shouldShowStartupWelcome(reason string, welcomeEnabled bool, mode string) bool {
	return reason == "startup" && welcomeEnabled && mode == "tui"
}
