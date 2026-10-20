package tintinweb_subagents

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// uiTheme is the part of the host's theme the subagent surfaces use. upstream: ui/agent-widget.ts Theme
// (fg, bold) plus agent-color.ts AgentNameTheme.getColorMode and the tool renderer's theme.getBgAnsi.
type uiTheme interface {
	Fg(color, text string) string
	Bold(text string) string
	// GetColorMode is "truecolor" or "256color"; "" reads as truecolor, as the original's optional method does.
	GetColorMode() string
	// GetBgAnsi is the escape sequence of a background token.
	GetBgAnsi(color string) string
}

// sdkTheme adapts the host palette (sdk.UITheme, upstream ctx.ui.theme) to uiTheme.
type sdkTheme struct{ t sdk.UITheme }

func (s sdkTheme) Fg(color, text string) string { return s.t.Fg(color, text) }
func (s sdkTheme) Bold(text string) string      { return s.t.Bold(text) }
func (s sdkTheme) GetColorMode() string         { return s.t.GetColorMode() }
func (s sdkTheme) GetBgAnsi(color string) string {
	out, err := s.t.GetBgAnsi(color)
	if err != nil {
		return ""
	}
	return out
}
