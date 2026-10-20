package powerline_footer

// Red stub: the last-prompt line. upstream: index.ts renderLastPromptLines.

type lastPromptState struct {
	prompt string
	show   bool
}

func newLastPromptState() lastPromptState { return lastPromptState{} }

func (s *lastPromptState) render(width int) []string { return nil }

func showLastPromptSetting(settings map[string]any) bool { return false }

func lastPromptWidgetEnabled(env func(string) string) bool { return false }

func collapseSpaces(text string) string { return "" }
