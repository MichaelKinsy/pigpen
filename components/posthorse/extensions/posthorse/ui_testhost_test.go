package posthorse

// Test harness of the renderers twins (twin_renderers_ui_test.go): a card and a
// message component over the fake host, standing in for Pi's
// ToolExecutionComponent and CustomMessageComponent shells (which are host
// code), so the rows the Go renderers return are composed the way the host
// composes them.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// uiPalette is a host theme palette. toolOutput differs per variant so a
// theme change is visible in the rows.
func uiPalette(name, toolOutput string) map[string]any {
	return map[string]any{
		"name": name,
		"foregrounds": map[string]any{
			"toolOutput": toolOutput, "muted": "\x1b[38;5;244m", "dim": "\x1b[38;5;240m", "error": "\x1b[38;5;196m",
			"warning": "\x1b[38;5;214m", "toolTitle": "\x1b[38;5;33m", "customMessageLabel": "\x1b[38;5;99m",
		},
		"backgrounds": map[string]any{"customMessageBg": "\x1b[48;5;53m"},
		"mode":        "256color",
	}
}

var (
	uiDark  = uiPalette("dark", "\x1b[38;5;250m")
	uiLight = uiPalette("light", "\x1b[38;5;236m")
)

// uiHost is a fake host running an extension that carries the four cards and
// both message renderers.
type uiHost struct {
	t   *testing.T
	h   *Host
	seq int
}

var uiTools = [...]string{"notes", "history", "get_context_remaining", "new_context"}

// uiExtension registers the four tools with their cards and both message
// renderers, the way the posthorse tools do.
func uiExtension() *sdk.Extension {
	ext := sdk.New("posthorse-ui")
	for _, name := range uiTools {
		def := sdk.ToolDefinition{
			Name: name, Label: name, Description: name,
			Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}},
			Execute:    func(sdk.Context, map[string]any) (any, error) { return sdk.ToolResult{Content: "ok"}, nil },
		}
		applyToolCards(&def, name)
		ext.RegisterTool(def)
	}
	registerPosthorseMessages(ext)
	return ext
}

func newUIHost(t *testing.T) *uiHost {
	t.Helper()
	return uiHostFor(t, uiExtension())
}

// uiHostFor starts a fake host for ext and gives it Pi's theme and the
// expand keybinding, as the interactive host does in its state snapshot.
func uiHostFor(t *testing.T, ext *sdk.Extension) *uiHost {
	t.Helper()
	u := &uiHost{t: t, h: StartHost(t, ext, HostOptions{})}
	u.setTheme(uiDark)
	u.state(map[string]any{
		"keybindings": map[string]any{
			"definitions":  map[string]any{"app.tools.expand": map[string]any{"defaultKeys": "ctrl+o", "description": "Expand tools"}},
			"userBindings": map[string]any{},
		},
	})
	return u
}

// state sends a state snapshot notification.
func (u *uiHost) state(state map[string]any) {
	u.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": state}}})
}

// setTheme is Pi's initTheme(...) followed by the theme push to extensions.
func (u *uiHost) setTheme(palette map[string]any) {
	u.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "theme_change", "args": palette}})
}

// uiResult is the tool result shape of renderResult: text content, details.
func uiResult(text string, details any) map[string]any {
	r := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	if details != nil {
		r["details"] = details
	}
	return r
}

// uiCard is Pi's ToolExecutionComponent over one tool.
type uiCard struct {
	u                *uiHost
	name, id         string
	args             map[string]any
	result           map[string]any
	isError          bool
	isPartial        bool
	executionStarted bool
	argsComplete     bool
	expanded         bool
	showImages       bool
	outputPad        int
}

// card is `new ToolExecutionComponent(name, "tool-1", args, {}, definition, ui, cwd)`.
func (u *uiHost) card(name string, args map[string]any) *uiCard {
	u.seq++
	return &uiCard{u: u, name: name, id: fmt.Sprintf("card-%d", u.seq), args: args, isPartial: true, showImages: true, outputPad: 1}
}

func (c *uiCard) updateArgs(args map[string]any) { c.args = args }
func (c *uiCard) markExecutionStarted()          { c.executionStarted = true }
func (c *uiCard) setExpanded(expanded bool)      { c.expanded = expanded }
func (c *uiCard) setShowImages(show bool)        { c.showImages = show }

// updateResult is `updateResult(result, isPartial)`; the result carries isError.
func (c *uiCard) updateResult(result map[string]any, isError bool, isPartial bool) {
	c.result = result
	c.isError = isError
	c.isPartial = isPartial
}

// click is the left click of createResultRegion: the shell (host code) toggles
// the expansion once a result exists and reports it handled.
func (c *uiCard) click() bool {
	if c.result == nil {
		return false
	}
	c.expanded = !c.expanded
	return true
}

func (c *uiCard) context() map[string]any {
	return map[string]any{
		"toolCallId": "tool-1", "cwd": c.u.h.opts.Cwd, "executionStarted": c.executionStarted, "argsComplete": c.argsComplete,
		"isPartial": c.isPartial, "expanded": c.expanded, "showImages": c.showImages, "isError": c.isError && c.result != nil,
		"outputPad": c.outputPad,
	}
}

// render is Container(Spacer(1), Box(outputPad, 1, bg)(call, result)).render(width).
func (c *uiCard) render(width int) []string {
	t := c.u.t
	t.Helper()
	inner := max(1, width-c.outputPad*2)
	rows, failure := c.u.h.RenderTool(c.name, ToolRender{Card: c.id, Phase: "call", Args: c.args, Context: c.context(), Width: inner})
	if failure != "" {
		t.Fatalf("renderCall %s: %s", c.name, failure)
	}
	if c.result != nil {
		more, failure := c.u.h.RenderTool(c.name, ToolRender{
			Card: c.id, Phase: "result", Args: c.args, Result: c.result,
			Options: map[string]any{"expanded": c.expanded, "isPartial": c.isPartial}, Context: c.context(), Width: inner,
		})
		if failure != "" {
			t.Fatalf("renderResult %s: %s", c.name, failure)
		}
		rows = append(rows, more...)
	}
	out := []string{""}
	return append(out, piBox(c.outputPad, 1, rows, width, func(s string) string { return s })...)
}

// uiMessage is Pi's CustomMessageComponent over one custom message.
type uiMessage struct {
	u          *uiHost
	message    map[string]any
	expanded   bool
	outputPad  int
	customType string
}

func (u *uiHost) message(customType string, content any, outputPad int) *uiMessage {
	return &uiMessage{
		u: u, customType: customType, outputPad: outputPad,
		message: map[string]any{"role": "custom", "customType": customType, "display": true, "timestamp": float64(0), "content": content},
	}
}

func (m *uiMessage) setExpanded(expanded bool) { m.expanded = expanded }

// render is Container(Spacer(1), renderer component).render(width).
func (m *uiMessage) render(width int) []string {
	t := m.u.t
	t.Helper()
	raw, failure := m.u.h.roundTrip(map[string]any{"method": "render_message", "tool": m.customType, "args": map[string]any{
		"message": m.message, "options": map[string]any{"expanded": m.expanded, "outputPad": m.outputPad}, "width": width,
	}})
	if failure != "" {
		t.Fatalf("render_message %s: %s", m.customType, failure)
	}
	var got struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("render_message answer %s: %v", raw, err)
	}
	return append([]string{""}, got.Lines...)
}

// uiComponent is what the tests draw: a card or a message.
type uiComponent interface {
	render(width int) []string
	setExpanded(expanded bool)
}

// uiLines renders at width, asserts every row fits, and returns the rows
// without terminal sequences or trailing blanks (renderers.test.ts lines()).
func uiLines(t *testing.T, c uiComponent, width int) []string {
	t.Helper()
	rendered := c.render(width)
	out := make([]string, len(rendered))
	for i, line := range rendered {
		if w := tui.VisibleWidth(line); w > width {
			t.Fatalf("row exceeds %d columns: %d: %q", width, w, line)
		}
		out[i] = strings.TrimRightFunc(tui.StripTerminalSequences(line), uiSpace)
	}
	return out
}

func uiText(t *testing.T, c uiComponent, width int) string {
	t.Helper()
	return strings.Join(uiLines(t, c, width), "\n")
}
