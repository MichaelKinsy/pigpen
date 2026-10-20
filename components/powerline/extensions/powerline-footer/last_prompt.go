package powerline_footer

import (
	"os"
	"strings"
)

// The last-prompt line below the editor. upstream: index.ts renderLastPromptLines (3091-3115), showLastPrompt (1229, 1861),
// lastUserPrompt (1228, 1845, 1995) and the widget installed in installPowerlineWidgets (3179-3187).

const (
	widgetLastPrompt = "powerline-last-prompt"
	// lastPromptColor is getFgAnsiCode("sep"): colors.ts THEME.sep, ANSI 256 gray.
	lastPromptColor = "\x1b[38;5;244m"
)

// lastPromptState is the original's lastUserPrompt, showLastPrompt and lastPromptRenderCache.
type lastPromptState struct {
	prompt string
	show   bool
	cache  *lastPromptCache
}

type lastPromptCache struct {
	source  string
	compact string
	width   int
	color   string
	lines   []string
}

func newLastPromptState() lastPromptState { return lastPromptState{show: true} }

// collapseSpaces is text.replace(/\s+/g, " ").trim().
func collapseSpaces(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		if isJSSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return jsTrim(b.String())
}

// render is renderLastPromptLines(width). bashModeActive cannot be true yet (bash mode is not ported), so the original's
// `bashModeActive ||` guard has no counterpart.
func (s *lastPromptState) render(width int) []string {
	if !s.show || s.prompt == "" {
		return nil
	}
	color := lastPromptColor
	if c := s.cache; c != nil && c.source == s.prompt && c.width == width && c.color == color {
		return c.lines
	}
	var compact string
	if s.cache != nil && s.cache.source == s.prompt {
		compact = s.cache.compact
	} else {
		compact = collapseSpaces(s.prompt)
	}
	prefix := " " + color + "↳" + ansiReset + " "
	available := width - visibleWidth(prefix)
	var lines []string
	if compact != "" && available >= 10 {
		lines = []string{truncateToWidth(prefix+color+truncateToWidth(compact, available, "…")+ansiReset, width, "…")}
	}
	s.cache = &lastPromptCache{source: s.prompt, compact: compact, width: width, color: color, lines: lines}
	return lines
}

// showLastPromptSetting is `settings.showLastPrompt !== false`.
func showLastPromptSetting(settings map[string]any) bool { return settings["showLastPrompt"] != false }

// lastPromptWidgetEnabled is editorPerf.options.widgets && editorPerf.options.lastPrompt (editor-performance.ts
// readEditorPerfOptions): both are on unless the profiler is enabled (POWERLINE_DEBUG_PERF=1) and turns them off.
func lastPromptWidgetEnabled(env func(string) string) bool {
	if env("POWERLINE_DEBUG_PERF") != "1" {
		return true
	}
	return env("POWERLINE_PERF_WIDGETS") != "0" && env("POWERLINE_PERF_LAST_PROMPT") != "0"
}

func osEnv(name string) string { return os.Getenv(name) }
