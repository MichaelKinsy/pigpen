package ansi

import (
	"fmt"
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// ompTitaniumTheme defines omp v19.1.1's default dark theme ("titanium") from
// packages/tui/src/theme/defaults.json (`theme.dark` default in
// packages/coding-agent/src/modes/settings.ts:65-67).
//
// PiG's extension SDK does not allow extensions or Piglets to register a custom
// theme palette or override host-owned elements (user bubbles, assistant
// markdown), and PiG's built-in `system` theme falls back to 16-color indexed
// tokens when a terminal does not answer OSC 10/11 queries. For everything
// rendered by `internal/ansi` (tool cards, welcome banner, status band,
// composer gutter, working row), `palette` applies omp's titanium palette when
// the host theme is dark or unspecified, respecting the host's color mode
// (`truecolor` vs `256color`) and falling back to the host's `UITheme` on light
// themes.

type rgbTriplet struct {
	r, g, b uint8
}

var titaniumFgColors = map[string]rgbTriplet{
	"accent":              {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"border":              {0x2a, 0x30, 0x38}, // subtleGray   #2a3038
	"borderAccent":        {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"borderMuted":         {0x1f, 0x25, 0x2d}, //              #1f252d
	"success":             {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"error":               {0xff, 0x47, 0x57}, // alertRed     #ff4757
	"warning":             {0xff, 0xb3, 0x47}, // warningAmber #ffb347
	"muted":               {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"dim":                 {0x6b, 0x72, 0x80}, //              #6b7280
	"thinkingText":        {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"customMessageLabel":  {0xd4, 0xc0, 0x90}, // titaniumGold #d4c090
	"toolOutput":          {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"mdHeading":           {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"mdLink":              {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"mdLinkUrl":           {0x00, 0x82, 0xb3}, // deepBlue     #0082b3
	"mdCode":              {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"mdCodeBlock":         {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"mdCodeBlockBorder":   {0x2a, 0x30, 0x38}, // subtleGray   #2a3038
	"mdQuote":             {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"mdQuoteBorder":       {0x2a, 0x30, 0x38}, // subtleGray   #2a3038
	"mdHr":                {0x2a, 0x30, 0x38}, // subtleGray   #2a3038
	"mdListBullet":        {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"toolDiffAdded":       {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"toolDiffRemoved":     {0xff, 0x47, 0x57}, // alertRed     #ff4757
	"toolDiffContext":     {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"link":                {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"syntaxComment":       {0x6b, 0x72, 0x80}, //              #6b7280
	"syntaxKeyword":       {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"syntaxFunction":      {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"syntaxVariable":      {0xe8, 0xec, 0xf4}, // brightAluminum #e8ecf4
	"syntaxString":        {0xd4, 0xc0, 0x90}, // titaniumGold #d4c090
	"syntaxNumber":        {0xff, 0xb3, 0x47}, // warningAmber #ffb347
	"syntaxType":          {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"syntaxOperator":      {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"syntaxPunctuation":   {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"thinkingOff":         {0x4a, 0x50, 0x58}, //              #4a5058
	"thinkingMinimal":     {0x5a, 0x60, 0x68}, //              #5a6068
	"thinkingLow":         {0x6a, 0x70, 0x78}, //              #6a7078
	"thinkingMedium":      {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"thinkingHigh":        {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"thinkingXhigh":       {0xd4, 0xc0, 0x90}, // titaniumGold #d4c090
	"thinkingMax":         {0xd4, 0xc0, 0x90}, // titaniumGold #d4c090
	"bashMode":            {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"statusLineSep":       {0x2a, 0x30, 0x38}, // subtleGray   #2a3038
	"statusLineModel":     {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"statusLinePath":      {0xe8, 0xec, 0xf4}, // brightAluminum #e8ecf4
	"statusLineGitClean":  {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"statusLineGitDirty":  {0xff, 0xb3, 0x47}, // warningAmber #ffb347
	"statusLineContext":   {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"statusLineSpend":     {0xd4, 0xc0, 0x90}, // titaniumGold #d4c090
	"statusLineStaged":    {0x00, 0xff, 0x88}, // readoutGreen #00ff88
	"statusLineDirty":     {0xff, 0xb3, 0x47}, // warningAmber #ffb347
	"statusLineUntracked": {0x9c, 0xa3, 0xb0}, // dimAluminum  #9ca3b0
	"statusLineOutput":    {0x00, 0x82, 0xb3}, // deepBlue     #0082b3
	"statusLineCost":      {0xd4, 0xc0, 0x90}, // titaniumGold #d4c090
	"statusLineSubagents": {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
	"pythonMode":          {0xf0, 0xc0, 0x40}, //              #f0c040
	"btwMode":             {0x00, 0xb4, 0xff}, // electricBlue #00b4ff
}

// titaniumDefaultFgTokens are tokens explicitly set to "" in titanium
// (inherit terminal default foreground via `\x1b[39m`).
var titaniumDefaultFgTokens = map[string]bool{
	"text":              true,
	"toolTitle":         true,
	"userMessageText":   true,
	"customMessageText": true,
}

var titaniumBgColors = map[string]rgbTriplet{
	"selectedBg":      {0x00, 0x82, 0xb3}, // deepBlue     #0082b3
	"userMessageBg":   {0x0f, 0x12, 0x16}, // darkTitanium #0f1216
	"customMessageBg": {0x2a, 0x30, 0x38}, // subtleGray   #2a3038
	"toolPendingBg":   {0x0f, 0x12, 0x16}, // darkTitanium #0f1216
	"toolSuccessBg":   {0x0f, 0x12, 0x16}, // darkTitanium #0f1216
	"toolErrorBg":     {0x1a, 0x0f, 0x10}, //              #1a0f10
	"statusLineBg":    {0x0f, 0x12, 0x16}, // darkTitanium #0f1216
}

// langBrandColors mirrors LANG_BRAND_COLORS in packages/tui/src/theme/theme-class.ts:133-138.
// Applied by langIconStyled so eval-kernel cell headers tint each language with
// its recognizable hue; languages without an entry fall back to "muted".
var langBrandColors = map[string]rgbTriplet{
	"lang.javascript": {0xf7, 0xdf, 0x1e}, // #f7df1e
	"lang.python":     {0x37, 0x76, 0xab}, // #3776ab
	"lang.ruby":       {0xcc, 0x34, 0x2d}, // #cc342d
	"lang.julia":      {0x95, 0x58, 0xb2}, // #9558b2
}

// titaniumSubagentPalette mirrors resolveAgentPalette in
// packages/tui/src/overlays/agent-identity.ts:19-30,73-144 for omp's default
// titanium theme: terminal.ansi slots (blue=accent skipped for distance<0.1,
// green=#00ff88, yellow=#f0c040, magenta=#d06fed, cyan=#13d1cd, brightBlue=#6ec9ff,
// brightGreen=#6bffb0) in arrival order.
var titaniumSubagentPalette = []rgbTriplet{
	{0x00, 0xff, 0x88}, // green (readoutGreen #00ff88)
	{0xf0, 0xc0, 0x40}, // yellow (#f0c040)
	{0xd0, 0x6f, 0xed}, // magenta (#d06fed)
	{0x13, 0xd1, 0xcd}, // cyan (#13d1cd)
	{0x6e, 0xc9, 0xff}, // brightBlue (#6ec9ff)
	{0x6b, 0xff, 0xb0}, // brightGreen (#6bffb0)
}

// Fg styles text with omp's titanium dark palette (or theme.Fg on light themes),
// matching palette.fg so sibling packages like internal/todo share the exact
// same SGR tokens without duplicating palette tables.
func Fg(theme sdk.UITheme, token, text string) string {
	return paletteFromTheme(theme).fg(token, text)
}

// Bold wraps text in bold SGR using theme.
func Bold(theme sdk.UITheme, text string) string {
	return paletteFromTheme(theme).bold(text)
}

// Italic wraps text in italic SGR using theme.
func Italic(theme sdk.UITheme, text string) string {
	return paletteFromTheme(theme).italic(text)
}

// Strikethrough wraps text in strikethrough SGR using theme.
func Strikethrough(theme sdk.UITheme, text string) string {
	return paletteFromTheme(theme).strike(text)
}

func useTitaniumPalette(t sdk.UITheme) bool {
	if t.Appearance() == "light" {
		return false
	}
	if strings.EqualFold(t.Name, "light") {
		return false
	}
	return true
}

func rgbFgAnsi(c rgbTriplet, mode string) string {
	if mode == "256color" {
		return tui.ForegroundAnsi(tui.RgbColorValue{R: float64(c.r), G: float64(c.g), B: float64(c.b)}, tui.TerminalColorMode256)
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b)
}

func rgbBgAnsi(c rgbTriplet, mode string) string {
	if mode == "256color" {
		return tui.BackgroundAnsi(tui.RgbColorValue{R: float64(c.r), G: float64(c.g), B: float64(c.b)}, tui.TerminalColorMode256)
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.r, c.g, c.b)
}

// adjustHsvRGB mirrors packages/utils/src/color.ts adjustHsv(hex, {s, v})
// used by status-line/component.ts:3584 for the compaction threshold / window
// label color (`adjustHsv(rawAccentHex, { s: 0.7, v: 0.75 })`).
func adjustHsvRGB(c rgbTriplet, sScale, vScale float64) rgbTriplet {
	rf := float64(c.r) / 255.0
	gf := float64(c.g) / 255.0
	bf := float64(c.b) / 255.0
	maxC := math.Max(rf, math.Max(gf, bf))
	minC := math.Min(rf, math.Min(gf, bf))
	delta := maxC - minC

	var h float64
	if delta > 0 {
		switch maxC {
		case rf:
			h = 60 * math.Mod((gf-bf)/delta, 6)
		case gf:
			h = 60 * ((bf-rf)/delta + 2)
		default:
			h = 60 * ((rf-gf)/delta + 4)
		}
		if h < 0 {
			h += 360
		}
	}
	var s float64
	if maxC > 0 {
		s = delta / maxC
	}
	v := maxC

	s = math.Max(0, math.Min(1, s*sScale))
	v = math.Max(0, math.Min(1, v*vScale))

	ch := v * s
	x := ch * (1 - math.Abs(math.Mod(h/60.0, 2)-1))
	m := v - ch
	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = ch, x, 0
	case h < 120:
		r1, g1, b1 = x, ch, 0
	case h < 180:
		r1, g1, b1 = 0, ch, x
	case h < 240:
		r1, g1, b1 = 0, x, ch
	case h < 300:
		r1, g1, b1 = x, 0, ch
	default:
		r1, g1, b1 = ch, 0, x
	}
	clampByte := func(f float64) uint8 {
		n := int(math.Round((f + m) * 255.0))
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return uint8(n)
	}
	return rgbTriplet{r: clampByte(r1), g: clampByte(g1), b: clampByte(b1)}
}
