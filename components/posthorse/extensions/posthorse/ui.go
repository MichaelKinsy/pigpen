package posthorse

// Port of ui.ts: the message renderers of the two custom message types.

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// cardHandoffLabel: ui.ts:31.
const cardHandoffLabel = "\n\nHandoff from the previous window:\n"

// renderPosthorseMessage draws one saved message (ui.ts:22-81).
//
// The original also keeps a per-message click state in a WeakMap and answers
// left clicks through a pi-tui MouseRegion (ui.ts:14-17, 74-80). The Go SDK
// has neither a message identity nor a mouse path for message renderers
// (docs/plan/extension-component-kit.md: "renderer views take no mouse"), so
// the expansion is the global one Pi passes in options.Expanded, which is
// also what the original's state resets to on every global change.
func renderPosthorseMessage(ctx sdk.Context, message map[string]any, options sdk.MessageRenderOptions, width int) ([]string, error) {
	return cardMessageRows(ctx.UITheme(), message, options, width), nil
}

// cardMessageRows is renderPosthorseMessage over an explicit theme.
func cardMessageRows(theme sdk.UITheme, message map[string]any, options sdk.MessageRenderOptions, width int) []string {
	expanded := options.Expanded
	window := message["customType"] == "context-window"
	raw := cardTextOf(message["content"])
	preview := raw
	previewRows := 3
	if window {
		at := strings.Index(raw, cardHandoffLabel)
		if at < 0 {
			preview = ""
		} else {
			preview = raw[at+len(cardHandoffLabel):]
		}
		previewRows = 2
	}
	// Pi injects keyText so the hint shows the user's configured keys.
	expandKey := options.KeyText("app.tools.expand")

	padding := min(options.OutputPad, max(0, (width-1)/2))
	inner := max(1, width-padding*2)
	marker := "▸"
	if expanded {
		marker = "▾"
	}
	title := "Checkpoint reminder"
	labelColor := colorWarning
	if window {
		title = "Context window started"
		labelColor = "customMessageLabel"
	}
	heading := tui.TruncateToWidth(theme.Fg(labelColor, theme.Bold(cleanText(marker+" "+title))), inner, tui.DefaultEllipsis, false)

	body := make([]string, 0, 8)
	body = append(body, heading)
	if expanded {
		body = append(body, cardBlock(raw, theme, colorToolOutput)(inner)...)
	} else {
		if window {
			history := cardBlock("Earlier conversation remains in history.", theme, colorMuted)(inner)
			body = append(body, history[:min(2, len(history))]...)
		}
		rows := cardLeadingRows(preview, theme, colorToolOutput, previewRows)(inner)
		body = append(body, rows[:min(previewRows, len(rows))]...)
		body = append(body, cardHint(theme, inner, expandKey))
	}
	return piBox(padding, 1, body, width, func(line string) string { return theme.Bg("customMessageBg", line) })
}

// piBox is pi-tui Box(paddingX, paddingY, bgFn).render(width) over one child
// whose rows are already laid out for the content width (box.js render).
func piBox(paddingX, paddingY int, rows []string, width int, bg func(string) string) []string {
	if len(rows) == 0 {
		return []string{}
	}
	leftPad := strings.Repeat(" ", paddingX)
	apply := func(line string) string {
		if pad := width - tui.VisibleWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		return bg(line)
	}
	out := make([]string, 0, len(rows)+2*paddingY)
	for range paddingY {
		out = append(out, apply(""))
	}
	for _, line := range rows {
		out = append(out, apply(leftPad+line))
	}
	for range paddingY {
		out = append(out, apply(""))
	}
	return out
}

// registerPosthorseMessages registers the renderer of both message types
// (ui.ts:20-85).
func registerPosthorseMessages(e *sdk.Extension) {
	for _, customType := range [...]string{"context-window", "posthorse-reminder"} {
		e.MessageRenderer(customType, renderPosthorseMessage)
	}
}
