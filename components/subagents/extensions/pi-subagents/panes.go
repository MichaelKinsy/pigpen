package pi_subagents

// panesEnabled reports whether subagents run in Herdr Desktop panes: the setting is on, the Herdr terminal environment
// is present, and the platform is not Windows.
func panesEnabled(s subagentSettings, getenv func(string) string, goos string) bool {
	return s.herdrPanes != nil && *s.herdrPanes && goos != "windows" &&
		getenv("HC_TERMINAL_SOCKET") != "" && getenv("HC_TERMINAL_ID") != ""
}
