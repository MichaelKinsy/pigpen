package surface

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// hermesEditorBase abstracts *sdk.Editor so unit tests can exercise hermesEditorComponent
// directly without requiring a live PiG host pipe.
type hermesEditorBase interface {
	HandleInput(data string) error
	HandleMouse(event sdk.EditorMouseEvent) error
	Render(width int) ([]string, error)
	SetText(text string) error
	InsertTextAtCursor(text string) error
	AddToHistory(text string) error
	GetText() (string, error)
}

// hermesEditorComponent wraps PiG's default editor and replaces its top horizontal
// border rule with a Hermes skin-colored labeled rule while preserving all content,
// APC cursor markers, bottom border, and autocomplete rows untouched.
type hermesEditorComponent struct {
	st   *state
	base hermesEditorBase
}

var _ sdk.EditorComponent = (*hermesEditorComponent)(nil)

func newHermesEditorComponent(st *state, base hermesEditorBase) *hermesEditorComponent {
	return &hermesEditorComponent{st: st, base: base}
}

func (e *hermesEditorComponent) HandleInput(data string) error {
	if e.base == nil {
		return nil
	}
	return e.base.HandleInput(data)
}

func (e *hermesEditorComponent) HandleMouse(event sdk.EditorMouseEvent) error {
	if e.base == nil {
		return nil
	}
	return e.base.HandleMouse(event)
}

func (e *hermesEditorComponent) SetText(text string) error {
	if e.base == nil {
		return nil
	}
	return e.base.SetText(text)
}

func (e *hermesEditorComponent) GetText() (string, error) {
	if e.base == nil {
		return "", nil
	}
	return e.base.GetText()
}

func (e *hermesEditorComponent) InsertTextAtCursor(text string) error {
	if e.base == nil {
		return nil
	}
	return e.base.InsertTextAtCursor(text)
}

func (e *hermesEditorComponent) AddToHistory(text string) error {
	if e.base == nil {
		return nil
	}
	return e.base.AddToHistory(text)
}

func (e *hermesEditorComponent) Invalidate() error {
	return nil
}

func (e *hermesEditorComponent) Render(width int) ([]string, error) {
	if width <= 0 {
		width = 80
	}
	var baseLines []string
	if e.base != nil {
		rendered, err := e.base.Render(width)
		if err != nil {
			return nil, err
		}
		baseLines = rendered
	}
	topRule := e.renderNamedRule(width)
	if len(baseLines) == 0 {
		return []string{topRule}, nil
	}
	out := make([]string, len(baseLines))
	copy(out, baseLines)
	out[0] = topRule
	return out, nil
}

func (e *hermesEditorComponent) renderNamedRule(width int) string {
	sk := e.st.currentSkin()
	ruleHex := sk.Color("input_rule", sk.Color("banner_border", "#CD7F32"))
	titleHex := sk.Color("banner_title", "#FFD700")

	symbol := strings.TrimSpace(sk.Symbol)
	if symbol == "" {
		symbol = "☤"
	}

	e.st.mu.RLock()
	profile := e.st.paths.ProfileName
	e.st.mu.RUnlock()
	if profile == "" {
		profile = "default"
	}

	var topLabel string
	if e.st.isBusy() {
		badge := e.st.busyBadgeText()
		topLabel = fmt.Sprintf(" %s %s ", symbol, badge)
	} else {
		topLabel = fmt.Sprintf(" %s hermes · %s ", symbol, profile)
	}
	return renderNamedRule(topLabel, width, ruleHex, titleHex)
}

func renderNamedRule(label string, width int, ruleHex, labelHex string) string {
	if width <= 0 {
		return ""
	}
	maxLabelW := width - 2
	if maxLabelW < 1 {
		maxLabelW = 1
	}
	clipped := tui.TruncateToWidth(label, maxLabelW, "…", false)
	labelW := tui.VisibleWidth(clipped)
	if labelW >= width {
		return colorize(labelHex, tui.TruncateToWidth(label, width, "", false))
	}
	leftDash := 2
	if width-labelW < 3 {
		leftDash = 0
	}
	rightDash := width - leftDash - labelW
	if rightDash < 0 {
		rightDash = 0
	}
	rule := colorize(ruleHex, strings.Repeat("─", leftDash)) +
		colorize(labelHex, clipped) +
		colorize(ruleHex, strings.Repeat("─", rightDash))
	if tui.VisibleWidth(rule) > width {
		return tui.TruncateToWidth(rule, width, "", false)
	}
	return rule
}

func (s *state) installEditorComponent(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	_ = ctx.SetEditorComponent(func(base *sdk.Editor) sdk.EditorComponent {
		return newHermesEditorComponent(s, base)
	})
}
