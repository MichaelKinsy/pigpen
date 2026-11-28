package surface

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

// IndicatorStyle names a Hermes busy indicator animation style.
const (
	IndicatorKaomoji = "kaomoji"
	IndicatorEmoji   = "emoji"
	IndicatorUnicode = "unicode"
	IndicatorASCII   = "ascii"
)

var validIndicatorStyles = []string{
	IndicatorKaomoji,
	IndicatorEmoji,
	IndicatorUnicode,
	IndicatorASCII,
}

var kaomojiFrames = []string{
	"(◔_◔)",
	"(⌐■_■)",
	"(◉_◉)",
	"٩(๑❛ᴗ❛๑)۶",
	"(•_•)",
	"( •_•)>⌐■-■",
	"(￣▽￣)",
	"(✧ω✧)",
	"(ง •̀_•́)ง",
	"(ᵔᴥᵔ)",
	"(⊙_⊙)",
	"(◠‿◠)",
	"(¬‿¬)",
	"(｡◕‿◕｡)",
	"(=^･ω･^=)",
}

var hermesBusyVerbs = []string{
	"pondering",
	"contemplating",
	"analyzing",
	"synthesizing",
	"reasoning",
	"deliberating",
	"crafting",
	"orchestrating",
	"illuminating",
	"dispatching",
}

var emojiFrames = []string{
	"☤ ",
	"🌀",
	"🤔",
	"✨",
	"🍵",
	"🔮",
}

var unicodeFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

var asciiFrames = []string{
	"|", "/", "-", "\\",
}

// NormalizeIndicatorStyle validates and normalizes an indicator style name.
func NormalizeIndicatorStyle(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", IndicatorKaomoji:
		return IndicatorKaomoji, true
	case IndicatorEmoji:
		return IndicatorEmoji, true
	case IndicatorUnicode:
		return IndicatorUnicode, true
	case IndicatorASCII:
		return IndicatorASCII, true
	default:
		return "", false
	}
}

// BuiltinIndicatorStyles returns the canonical list of Hermes busy indicator styles.
func BuiltinIndicatorStyles() []string {
	out := make([]string, len(validIndicatorStyles))
	copy(out, validIndicatorStyles)
	return out
}

// IndicatorFrames returns the frame slice for the given indicator style.
func IndicatorFrames(style string) []string {
	norm, ok := NormalizeIndicatorStyle(style)
	if !ok {
		norm = IndicatorKaomoji
	}
	switch norm {
	case IndicatorEmoji:
		return append([]string(nil), emojiFrames...)
	case IndicatorUnicode:
		return append([]string(nil), unicodeFrames...)
	case IndicatorASCII:
		return append([]string(nil), asciiFrames...)
	default:
		return append([]string(nil), kaomojiFrames...)
	}
}

// FormatWorkingMessage formats the busy working message for the given style and step.
func FormatWorkingMessage(style string, step int) string {
	if step < 0 {
		step = 0
	}
	frames := IndicatorFrames(style)
	frame := frames[step%len(frames)]
	verb := hermesBusyVerbs[step%len(hermesBusyVerbs)]
	return fmt.Sprintf("%s %s…", strings.TrimSpace(frame), verb)
}

// ── ANSI Truecolor & Display Width Helpers ───────────────────────────────────

const ansiReset = "\x1b[0m"

func hexToANSI(hex string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(hex), "#"))
	if len(s) != 6 {
		return ""
	}
	r, errR := strconv.ParseUint(s[0:2], 16, 8)
	g, errG := strconv.ParseUint(s[2:4], 16, 8)
	b, errB := strconv.ParseUint(s[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func colorize(hex, text string) string {
	code := hexToANSI(hex)
	if code == "" || text == "" {
		return text
	}
	return code + text + ansiReset
}

func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func runeWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	// Combining marks
	if (r >= 0x0300 && r <= 0x036F) || (r >= 0x1AB0 && r <= 0x1AFF) ||
		(r >= 0x1DC0 && r <= 0x1DFF) || (r >= 0x20D0 && r <= 0x20FF) ||
		(r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xFE20 && r <= 0xFE2F) {
		return 0
	}
	// Wide emoji / CJK ranges
	if (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1FAFF) {
		return 2
	}
	return 1
}

func visibleWidth(s string) int {
	plain := stripANSI(s)
	w := 0
	for _, r := range plain {
		w += runeWidth(r)
	}
	return w
}

func truncatePlain(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if visibleWidth(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		for _, r := range s {
			if runeWidth(r) <= 1 {
				return string(r)
			}
			break
		}
		return "…"
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > maxWidth-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteRune('…')
	return b.String()
}

func padPlainRight(s string, targetWidth int) string {
	if targetWidth <= 0 {
		return ""
	}
	plain := truncatePlain(s, targetWidth)
	pad := targetWidth - visibleWidth(plain)
	if pad > 0 {
		return plain + strings.Repeat(" ", pad)
	}
	return plain
}

func truncateANSI(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if visibleWidth(s) <= maxWidth {
		return s
	}
	var b strings.Builder
	w := 0
	hasANSI := false
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				b.WriteString(s[i : j+1])
				hasANSI = true
				i = j + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runeWidth(r)
		if maxWidth > 1 && w+rw > maxWidth-1 {
			b.WriteRune('…')
			break
		}
		if maxWidth == 1 && w+rw > maxWidth {
			break
		}
		b.WriteRune(r)
		w += rw
		i += size
	}
	if hasANSI {
		b.WriteString(ansiReset)
	}
	return b.String()
}

// ── Meter & Formatting Helpers ───────────────────────────────────────────────

// ContextMeterColor returns the Hermes threshold hex color for a usage percentage.
// Thresholds: <50% #8FBC8F, 50-80% #FFD700, 81-94% #FF8C00, >=95% #FF6B6B.
func ContextMeterColor(pct int) string {
	switch {
	case pct >= 95:
		return "#FF6B6B"
	case pct >= 81:
		return "#FF8C00"
	case pct >= 50:
		return "#FFD700"
	default:
		return "#8FBC8F"
	}
}

func renderMeterBar(pct int, cells int) string {
	if cells <= 0 {
		cells = 10
	}
	clamped := pct
	if clamped < 0 {
		clamped = 0
	}
	if clamped > 100 {
		clamped = 100
	}
	filled := (clamped*cells + 50) / 100
	if clamped > 0 && filled == 0 {
		filled = 1
	}
	if filled > cells {
		filled = cells
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", cells-filled)
	return fmt.Sprintf("[%s] %d%%", bar, clamped)
}

func formatCompactTokens(total int) string {
	switch {
	case total >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(total)/1_000_000.0)
	case total >= 1_000:
		return fmt.Sprintf("%.1fk", float64(total)/1_000.0)
	default:
		return strconv.Itoa(total)
	}
}

func shortenCwd(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if cwd == home {
			return "~"
		}
		if strings.HasPrefix(cwd, home+string(filepath.Separator)) {
			return "~" + cwd[len(home):]
		}
	}
	return cwd
}

func (s *state) snapshotCwd() string {
	s.mu.RLock()
	cwd := s.cwd
	s.mu.RUnlock()
	if cwd != "" {
		return cwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func (s *state) snapshotContextPct() (int, bool) {
	s.mu.RLock()
	has := s.hasContextPct
	pct := s.contextPct
	s.mu.RUnlock()
	return pct, has
}

func (s *state) updateContextMetrics(ctx sdk.Context) {
	if cwd := strings.TrimSpace(ctx.Cwd()); cwd != "" {
		s.mu.Lock()
		s.cwd = cwd
		s.mu.Unlock()
	}
	if cu, err := ctx.GetContextUsage(); err == nil && cu != nil && cu.Percent != nil {
		pct := int(*cu.Percent + 0.5)
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		s.mu.Lock()
		s.contextPct = pct
		s.hasContextPct = true
		s.mu.Unlock()
	}
}

func (s *state) currentIndicatorStyle() string {
	s.mu.RLock()
	style := s.indicatorStyle
	s.mu.RUnlock()
	norm, ok := NormalizeIndicatorStyle(style)
	if !ok {
		return IndicatorKaomoji
	}
	return norm
}

func (s *state) nextWorkingMessage() string {
	s.mu.Lock()
	style := s.indicatorStyle
	if style == "" {
		style = IndicatorKaomoji
	}
	step := s.indicatorStep
	s.indicatorStep++
	s.mu.Unlock()
	return FormatWorkingMessage(style, step)
}

// ── 1. Hermes 2-Column Bordered Banner Header ────────────────────────────────

// renderHermesHeader renders the Hermes TUI header for the given terminal width.
// Wide terminals (>=76 cols) get a rounded Unicode 2-column bordered banner;
// narrow terminals (<76 cols) get a compact 2-line responsive banner.
func renderHermesHeader(st *state, width int) []string {
	if width <= 0 {
		width = 80
	}
	sk := st.currentSkin()
	borderHex := sk.Color("banner_border", "#CD7F32")
	titleHex := sk.Color("banner_title", "#FFD700")
	accentHex := sk.Color("banner_accent", "#FFBF00")
	textHex := sk.Color("banner_text", "#FFF8DC")
	dimHex := sk.Color("banner_dim", "#B8860B")
	warnHex := sk.Color("ui_warn", "#ffa726")

	st.mu.RLock()
	profile := st.paths.ProfileName
	toolset := st.toolset
	skillsDir := st.paths.SkillsDir
	st.mu.RUnlock()
	if profile == "" {
		profile = "default"
	}
	if toolset == "" {
		toolset = "hermes-cli"
	}

	model := "default"
	mcpCount := 0
	yolo := false
	if st.cfg != nil {
		if strings.TrimSpace(st.cfg.Model) != "" {
			model = strings.TrimSpace(st.cfg.Model)
		}
		for _, srv := range st.cfg.MCPServers {
			if srv.Enabled {
				mcpCount++
			}
		}
		yolo = st.cfg.IsYolo()
	}

	cwd := shortenCwd(st.snapshotCwd())
	toolCount := len(ResolveToolsetTools(toolset))
	skillsCount := len(discoverSkills(skillsDir))
	memPct := st.memPercent()

	// Narrow responsive banner (< 76 cols): 2 lines, never wraps or clips.
	if width < 76 {
		line1Plain := fmt.Sprintf("%s Hermes Agent · pig-hermes · %s (%s)", sk.Symbol, model, profile)
		if yolo {
			line1Plain += " ⚠ YOLO"
		}
		line2Plain := fmt.Sprintf("Tools:%d (%s) · Skills:%d · MCP:%d · Mem:%d%%", toolCount, toolset, skillsCount, mcpCount, memPct)
		return []string{
			colorize(titleHex, truncatePlain(line1Plain, width)),
			colorize(dimHex, truncatePlain(line2Plain, width)),
		}
	}

	// Wide 2-column rounded Unicode box (>= 76 cols).
	innerWidth := width - 2 // inside '│' ... '│'
	leftWidth := (innerWidth - 3) / 2
	rightWidth := innerWidth - 3 - leftWidth

	// Top border with title
	titleLabel := " Hermes Agent · pig-hermes "
	titleVis := visibleWidth(titleLabel)
	leftDash := 2
	rightDash := innerWidth - leftDash - titleVis
	if rightDash < 1 {
		rightDash = 1
	}
	topLine := colorize(borderHex, "╭"+strings.Repeat("─", leftDash)) +
		colorize(titleHex, titleLabel) +
		colorize(borderHex, strings.Repeat("─", rightDash)+"╮")

	// Left column rows
	leftRows := []struct {
		plain string
		hex   string
	}{
		{fmt.Sprintf("%s  Winged PiG · Caduceus", sk.Symbol), accentHex},
		{fmt.Sprintf("Model:   %s", model), textHex},
		{fmt.Sprintf("Profile: %s", profile), textHex},
		{fmt.Sprintf("CWD:     %s", cwd), dimHex},
	}
	if yolo {
		leftRows = append(leftRows, struct {
			plain string
			hex   string
		}{"⚠ YOLO mode (approvals bypassed)", warnHex})
	} else {
		leftRows = append(leftRows, struct {
			plain string
			hex   string
		}{fmt.Sprintf("Skin:    %s", sk.Name), dimHex})
	}

	// Right column rows
	memBar := renderMeterBar(memPct, 10)
	rightRows := []struct {
		plain string
		hex   string
	}{
		{fmt.Sprintf("Available Tools: %s (%d)", toolset, toolCount), textHex},
		{fmt.Sprintf("Skills:          %d discovered", skillsCount), textHex},
		{fmt.Sprintf("MCP Servers:     %d configured", mcpCount), textHex},
		{fmt.Sprintf("Memory:          %s", memBar), ContextMeterColor(memPct)},
		{"/help · /skin · /indicator · /status", dimHex},
	}

	rowCount := len(leftRows)
	if len(rightRows) > rowCount {
		rowCount = len(rightRows)
	}

	lines := make([]string, 0, rowCount+2)
	lines = append(lines, topLine)
	sep := colorize(borderHex, " │ ")
	vBar := colorize(borderHex, "│")

	for i := range rowCount {
		var lText, lHex, rText, rHex string
		if i < len(leftRows) {
			lText, lHex = leftRows[i].plain, leftRows[i].hex
		}
		if i < len(rightRows) {
			rText, rHex = rightRows[i].plain, rightRows[i].hex
		}
		lCell := colorize(lHex, padPlainRight(" "+lText, leftWidth))
		rCell := colorize(rHex, padPlainRight(rText, rightWidth))
		lines = append(lines, vBar+lCell+sep+rCell+vBar)
	}

	bottomLine := colorize(borderHex, "╰"+strings.Repeat("─", innerWidth)+"╯")
	lines = append(lines, bottomLine)
	return lines
}

// ── 2. Hermes Dynamic Footer with Context-Usage Meter ────────────────────────

// renderHermesFooter renders the Hermes dynamic footer for the given width.
// Width tiers:
//   - < 52:   compact `☤ {profile} │ [█████░░░░░] {pct}%`
//   - 52-75:  `☤ {profile} │ {skin} │ [█████░░░░░] {pct}% │ Σ{tokens}[ │ YOLO]`
//   - >= 76:  full left status + right-aligned key hints (`/` cmds · `@` files · `!` shell)
func renderHermesFooter(st *state, width int, extStatuses map[string]string) []string {
	if width <= 0 {
		width = 80
	}
	sk := st.currentSkin()
	strongHex := sk.Color("status_bar_strong", "#FFD700")
	textHex := sk.Color("status_bar_text", "#C0C0C0")
	dimHex := sk.Color("status_bar_dim", "#8A7A4A")
	warnHex := sk.Color("ui_warn", "#ffa726")

	st.mu.RLock()
	profile := st.paths.ProfileName
	skinName := st.skin
	st.mu.RUnlock()
	if profile == "" {
		profile = "default"
	}
	if skinName == "" {
		skinName = sk.Name
	}

	pct, hasCtx := st.snapshotContextPct()
	if !hasCtx {
		pct = st.memPercent()
	}
	meterText := renderMeterBar(pct, 10)
	meterHex := ContextMeterColor(pct)

	totalTokens := 0
	for _, rec := range usage.Default().Snapshot() {
		totalTokens += rec.InputTokens + rec.OutputTokens
	}
	tokenText := "Σ" + formatCompactTokens(totalTokens)
	yolo := st.cfg != nil && st.cfg.IsYolo()

	div := colorize(dimHex, " │ ")
	profileSeg := colorize(strongHex, fmt.Sprintf("☤ %s", profile))
	skinSeg := colorize(textHex, skinName)
	meterSeg := colorize(meterHex, meterText)
	tokSeg := colorize(textHex, tokenText)
	yoloSeg := colorize(warnHex, "YOLO")

	var extraStatuses []string
	if len(extStatuses) > 0 {
		keys := make([]string, 0, len(extStatuses))
		for k, v := range extStatuses {
			if k == "hermes" || strings.TrimSpace(v) == "" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			extraStatuses = append(extraStatuses, strings.TrimSpace(extStatuses[k]))
		}
	}

	if width < 52 {
		line := profileSeg + div + meterSeg
		if yolo {
			line += div + yoloSeg
		}
		for _, stText := range extraStatuses {
			line += div + stText
		}
		return []string{truncateANSI(line, width)}
	}

	left := profileSeg + div + skinSeg + div + meterSeg + div + tokSeg
	if yolo {
		left += div + yoloSeg
	}
	for _, stText := range extraStatuses {
		left += div + stText
	}

	if width < 76 {
		return []string{truncateANSI(left, width)}
	}

	hintsPlain := "/ cmds · @ files · ! shell"
	hintsSeg := colorize(dimHex, hintsPlain)
	leftW := visibleWidth(left)
	hintsW := visibleWidth(hintsPlain)
	gap := width - leftW - hintsW
	if gap >= 2 {
		return []string{left + strings.Repeat(" ", gap) + hintsSeg}
	}
	return []string{truncateANSI(left, width)}
}

func (s *state) installFooterRenderer(ctx sdk.Context) {
	if !ctx.HasUI() {
		return
	}
	_ = ctx.SetFooterRenderer(func(width int) []string {
		return renderHermesFooter(s, width, ctx.FooterData().ExtensionStatuses())
	})
}

// ── 3. /indicator and /banner Slash Commands ─────────────────────────────────

func (s *state) cmdBanner(ctx sdk.Context, args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "off" || arg == "hide" || arg == "clear" {
		if ctx.HasUI() {
			_ = ctx.SetWidget("hermes_banner", nil)
		}
		s.show(ctx, "Hermes summary banner hidden.")
		return nil
	}
	s.updateContextMetrics(ctx)
	width := ctx.Width()
	if width <= 0 {
		width = 96
	}
	if arg == "pin" || arg == "widget" {
		if ctx.HasUI() {
			_ = ctx.SetWidgetRenderer("hermes_banner", func(w int) []string {
				return renderHermesHeader(s, w)
			})
		}
		s.show(ctx, "Hermes summary banner pinned as widget (use /banner off to hide).")
		return nil
	}
	s.show(ctx, strings.Join(renderHermesHeader(s, width), "\n"))
	return nil
}

func (s *state) cmdIndicator(ctx sdk.Context, args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "" || arg == "status" || arg == "list" {
		cur := s.currentIndicatorStyle()
		var b strings.Builder
		fmt.Fprintf(&b, "Active Busy Indicator: %s (%s)\n", cur, FormatWorkingMessage(cur, 0))
		fmt.Fprintf(&b, "Available Styles:\n")
		for _, name := range validIndicatorStyles {
			marker := " "
			if name == cur {
				marker = "*"
			}
			fmt.Fprintf(&b, "  %s %-10s %s\n", marker, name, FormatWorkingMessage(name, 0))
		}
		s.show(ctx, strings.TrimRight(b.String(), "\n"))
		return nil
	}

	norm, ok := NormalizeIndicatorStyle(arg)
	if !ok {
		s.show(ctx, fmt.Sprintf("Unknown indicator style %q. Available: %s", arg, strings.Join(validIndicatorStyles, ", ")))
		return nil
	}

	s.mu.Lock()
	s.indicatorStyle = norm
	s.indicatorStep = 0
	s.mu.Unlock()

	if s.paths.ConfigFile != "" {
		_ = config.SetKey(s.paths.ConfigFile, "display.indicator_style", norm)
	}
	s.installFooterRenderer(ctx)
	s.show(ctx, fmt.Sprintf("Busy indicator style set to %s (%s)", norm, FormatWorkingMessage(norm, 0)))
	return nil
}
