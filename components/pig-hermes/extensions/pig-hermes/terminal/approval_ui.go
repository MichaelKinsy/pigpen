package terminal

import (
	"fmt"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

const (
	approvalLinesPerPage = 10
	approvalWarnGoldHex  = "#FFD700"
	approvalDimHex       = "#B8860B"
	approvalTextHex      = "#FFF8DC"
	approvalAccentHex    = "#FFBF00"
	approvalANSIReset    = "\x1b[0m"
)

type approvalChoiceOption struct {
	key   string
	label string
}

var approvalModalOptions = []approvalChoiceOption{
	{key: "once", label: approvalChoiceOnce},
	{key: "session", label: approvalChoiceSession},
	{key: "always", label: approvalChoiceAlways},
	{key: "deny", label: approvalChoiceDeny},
	{key: "tell", label: approvalChoiceTell},
}

// approvalResult is the JSON-serializable result returned by approvalPromptComponent.
type approvalResult struct {
	Choice string `json:"choice"`
	Reason string `json:"reason,omitempty"`
}

// approvalPromptComponent renders Hermes's double-bordered dangerous-command approval
// modal as an sdk.RemoteComponent matching upstream ui-tui/src/components/prompts.tsx.
type approvalPromptComponent struct {
	command    string
	reason     string
	cursor     int
	page       int
	tellMode   bool
	tellInput  string
	invalidate func()
}

var (
	_ sdk.RemoteComponent            = (*approvalPromptComponent)(nil)
	_ sdk.RemoteComponentInvalidator = (*approvalPromptComponent)(nil)
)

func newApprovalPromptComponent(command, reason string) *approvalPromptComponent {
	return &approvalPromptComponent{
		command: sanitizeApprovalDisplay(strings.TrimSpace(command)),
		reason:  sanitizeApprovalDisplay(strings.TrimSpace(reason)),
	}
}

// sanitizeApprovalDisplay expands tabs to 2 spaces and replaces every control rune
// (< 0x20 except '\n', 0x7f, and C1 0x80..0x9f) with a visible escape so terminal
// control sequences in a proposed command cannot spoof the approval preview.
func sanitizeApprovalDisplay(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("  ")
		case r == '\n':
			b.WriteByte('\n')
		case r == '\x1b':
			b.WriteString("␛")
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			fmt.Fprintf(&b, "\\x%02x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *approvalPromptComponent) SetInvalidate(fn func()) {
	c.invalidate = fn
}

func (c *approvalPromptComponent) notifyChanged() {
	if c.invalidate != nil {
		c.invalidate()
	}
}

func (c *approvalPromptComponent) Render(width int) []string {
	if width < 24 {
		width = 24
	}
	innerWidth := width - 2 // inside '║' ... '║'
	contentWidth := innerWidth - 2
	if contentWidth < 10 {
		contentWidth = 10
	}

	gold := approvalColor(approvalWarnGoldHex)
	accent := approvalColor(approvalAccentHex)
	text := approvalColor(approvalTextHex)
	dim := approvalColor(approvalDimHex)

	header := "⚠ approval required"
	if c.reason != "" {
		header += " · " + c.reason
	}
	headerClipped := " " + approvalTruncate(header, max(innerWidth-4, 1)) + " "
	headerVis := approvalVisibleWidth(headerClipped)
	leftEq := 1
	rightEq := innerWidth - leftEq - headerVis
	if rightEq < 0 {
		rightEq = 0
	}

	topLine := gold + "╔" + strings.Repeat("═", leftEq) + headerClipped + strings.Repeat("═", rightEq) + "╗" + approvalANSIReset
	botLine := gold + "╚" + strings.Repeat("═", innerWidth) + "╝" + approvalANSIReset
	sepLine := gold + "╠" + strings.Repeat("═", innerWidth) + "╣" + approvalANSIReset

	boxRow := func(colored string) string {
		clipped := approvalTruncateANSI(colored, contentWidth)
		pad := contentWidth - approvalVisibleWidth(clipped)
		if pad < 0 {
			pad = 0
		}
		return gold + "║" + approvalANSIReset + " " + clipped + strings.Repeat(" ", pad) + " " + gold + "║" + approvalANSIReset
	}

	wrappedCmd := wrapApprovalLines(c.command, contentWidth)
	totalLines := len(wrappedCmd)
	totalPages := (totalLines + approvalLinesPerPage - 1) / approvalLinesPerPage
	if totalPages < 1 {
		totalPages = 1
	}
	page := c.page % totalPages
	if page < 0 {
		page = 0
	}
	startIdx := page * approvalLinesPerPage
	endIdx := startIdx + approvalLinesPerPage
	if endIdx > totalLines {
		endIdx = totalLines
	}

	lines := make([]string, 0, approvalLinesPerPage+12)
	lines = append(lines, topLine)
	for _, cmdLine := range wrappedCmd[startIdx:endIdx] {
		lines = append(lines, boxRow(text+cmdLine+approvalANSIReset))
	}
	if totalLines > approvalLinesPerPage {
		pager := fmt.Sprintf("Lines %d-%d of %d · v for more", startIdx+1, endIdx, totalLines)
		lines = append(lines, boxRow(dim+pager+approvalANSIReset))
	}
	lines = append(lines, sepLine)

	if c.tellMode {
		lines = append(lines, boxRow(accent+"Tell Hermes what to do instead:"+approvalANSIReset))
		lines = append(lines, boxRow(text+"> "+c.tellInput+"█"+approvalANSIReset))
		lines = append(lines, boxRow(dim+"Enter submit · Esc back"+approvalANSIReset))
	} else {
		for i, opt := range approvalModalOptions {
			if i == c.cursor {
				lines = append(lines, boxRow("\x1b[1m"+gold+"▸ "+opt.label+approvalANSIReset))
			} else {
				lines = append(lines, boxRow(text+"  "+opt.label+approvalANSIReset))
			}
		}
		hint := "↑/↓ or j/k move · 1-5 select · Enter confirm · Esc deny"
		if totalLines > approvalLinesPerPage {
			hint = "v page · " + hint
		}
		lines = append(lines, boxRow(dim+hint+approvalANSIReset))
	}

	lines = append(lines, botLine)
	return lines
}

func (c *approvalPromptComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "" {
		return sdk.RemoteComponentResult{}, nil
	}

	// Ctrl+C always denies immediately (legacy or Kitty protocol).
	if tui.MatchesKey(data, tui.Key.Ctrl("c")) {
		return sdk.RemoteComponentResult{
			Done:  true,
			Value: approvalResult{Choice: "deny"},
		}, nil
	}

	if c.tellMode {
		return c.handleTellInput(data)
	}

	switch {
	case tui.MatchesKey(data, tui.Key.Escape):
		return sdk.RemoteComponentResult{
			Done:  true,
			Value: approvalResult{Choice: "deny"},
		}, nil
	case tui.MatchesKey(data, tui.Key.Enter):
		return c.confirmChoice(c.cursor)
	case tui.MatchesKey(data, tui.Key.Up) || data == "k" || data == "K":
		c.cursor = (c.cursor - 1 + len(approvalModalOptions)) % len(approvalModalOptions)
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	case tui.MatchesKey(data, tui.Key.Down) || data == "j" || data == "J":
		c.cursor = (c.cursor + 1) % len(approvalModalOptions)
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	case data == "v" || data == "V":
		c.page++
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	case data == "1":
		return c.confirmChoice(0)
	case data == "2":
		return c.confirmChoice(1)
	case data == "3":
		return c.confirmChoice(2)
	case data == "4":
		return c.confirmChoice(3)
	case data == "5":
		return c.confirmChoice(4)
	}
	return sdk.RemoteComponentResult{}, nil
}

func (c *approvalPromptComponent) confirmChoice(idx int) (sdk.RemoteComponentResult, error) {
	if idx < 0 || idx >= len(approvalModalOptions) {
		return sdk.RemoteComponentResult{
			Done:  true,
			Value: approvalResult{Choice: "deny"},
		}, nil
	}
	key := approvalModalOptions[idx].key
	if key == "tell" {
		c.cursor = idx
		c.tellMode = true
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	}
	return sdk.RemoteComponentResult{
		Done:  true,
		Value: approvalResult{Choice: key},
	}, nil
}

func (c *approvalPromptComponent) handleTellInput(data string) (sdk.RemoteComponentResult, error) {
	switch {
	case tui.MatchesKey(data, tui.Key.Escape):
		c.tellMode = false
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	case tui.MatchesKey(data, tui.Key.Enter):
		return sdk.RemoteComponentResult{
			Done: true,
			Value: approvalResult{
				Choice: "tell",
				Reason: strings.TrimSpace(c.tellInput),
			},
		}, nil
	case tui.MatchesKey(data, tui.Key.Backspace):
		if len(c.tellInput) > 0 {
			_, size := utf8.DecodeLastRuneInString(c.tellInput)
			c.tellInput = c.tellInput[:len(c.tellInput)-size]
			c.notifyChanged()
		}
		return sdk.RemoteComponentResult{}, nil
	case tui.MatchesKey(data, tui.Key.Ctrl("u")):
		c.tellInput = ""
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	}

	if ch, ok := tui.DecodePrintableKey(data); ok && ch != "" {
		c.tellInput += ch
		c.notifyChanged()
		return sdk.RemoteComponentResult{}, nil
	}

	// Ignore CSI/escape sequences in text input mode.
	if strings.HasPrefix(data, "\x1b") {
		return sdk.RemoteComponentResult{}, nil
	}

	var added strings.Builder
	for _, r := range data {
		if r == '\r' || r == '\n' {
			continue
		}
		if r >= 0x20 && r != 0x7f {
			added.WriteRune(r)
		}
	}
	if added.Len() > 0 {
		c.tellInput += added.String()
		c.notifyChanged()
	}
	return sdk.RemoteComponentResult{}, nil
}

func decodeApprovalResult(raw any) (choice string, tellReason string, ok bool) {
	switch v := raw.(type) {
	case approvalResult:
		return v.Choice, v.Reason, v.Choice != ""
	case *approvalResult:
		if v == nil {
			return "", "", false
		}
		return v.Choice, v.Reason, v.Choice != ""
	case map[string]any:
		ch, _ := v["choice"].(string)
		rs, _ := v["reason"].(string)
		return ch, rs, ch != ""
	case string:
		if strings.TrimSpace(v) != "" {
			return parseApprovalChoice(v), "", true
		}
	}
	return "", "", false
}

func wrapApprovalLines(text string, width int) []string {
	if width <= 0 {
		width = 60
	}
	if strings.TrimSpace(text) == "" {
		return []string{"(empty command)"}
	}
	var out []string
	for _, rawLine := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if rawLine == "" {
			out = append(out, "")
			continue
		}
		rem := rawLine
		for len(rem) > 0 {
			if approvalVisibleWidth(rem) <= width {
				out = append(out, rem)
				break
			}
			// Try word-boundary wrap first.
			cutByte := 0
			lastSpaceByte := -1
			w := 0
			for i, r := range rem {
				rw := tui.VisibleWidth(string(r))
				if w+rw > width {
					break
				}
				w += rw
				cutByte = i + utf8.RuneLen(r)
				if r == ' ' || r == '\t' {
					lastSpaceByte = i
				}
			}
			if cutByte == 0 {
				_, size := utf8.DecodeRuneInString(rem)
				cutByte = size
			}
			if lastSpaceByte > 0 {
				out = append(out, strings.TrimRight(rem[:lastSpaceByte], " \t"))
				rem = strings.TrimLeft(rem[lastSpaceByte+1:], " \t")
			} else {
				out = append(out, rem[:cutByte])
				rem = rem[cutByte:]
			}
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func approvalColor(hex string) string {
	s := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(s) != 6 {
		return ""
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func approvalStripANSI(s string) string {
	return tui.StripTerminalSequences(s)
}

func approvalVisibleWidth(s string) int {
	return tui.VisibleWidth(s)
}

func approvalTruncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if tui.VisibleWidth(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return tui.TruncateToWidth(s, 1, "", false)
	}
	return tui.TruncateToWidth(s, maxWidth, "…", false)
}

func approvalTruncateANSI(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if tui.VisibleWidth(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return tui.TruncateToWidth(s, 1, "", false)
	}
	return tui.TruncateToWidth(s, maxWidth, "…", false)
}
